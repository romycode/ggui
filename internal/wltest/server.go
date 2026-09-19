package wltest

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/wlcore"
)

// ReleaseMode says when the fake sends wl_buffer.release for a buffer the
// client committed. Real compositors differ here, and a client that only
// works against one of the two is broken, so both are expressible.
type ReleaseMode int

const (
	// ReleaseImmediately releases the buffer as soon as the commit has
	// been snapshotted: the compositor copied the pixels out and does not
	// need the memory any more.
	ReleaseImmediately ReleaseMode = iota
	// ReleaseOnNextCommit keeps the committed buffer until another commit
	// replaces it, which is what a compositor that scans out the client's
	// memory does. A client with a two-buffer pool stalls under this mode
	// if it forgets to wait for the release.
	ReleaseOnNextCommit
)

// DefaultVsync is how long the fake waits before answering a frame
// callback when Options.Vsync is zero: one frame at 60Hz.
const DefaultVsync = 16 * time.Millisecond

// Default window size for the automatic first configure.
const (
	defaultWidth  = 640
	defaultHeight = 480
)

// commitBuffer is how many commits Commits() holds. A test that stops
// reading does not stall the fake: the oldest commit is dropped so the
// newest is always the one waiting.
const commitBuffer = 64

// Options configures a Server. The zero value is a compositor with no
// seat, releasing buffers immediately, at 640x480 and 60Hz.
type Options struct {
	// Seat announces wl_seat, with both the keyboard and the pointer
	// capability. Without it the client gets no input devices, which is a
	// valid session a window layer has to survive.
	Seat bool
	// ReleaseMode picks when wl_buffer.release is sent.
	ReleaseMode ReleaseMode
	// Vsync is how long a frame callback waits before firing. Zero means
	// DefaultVsync.
	Vsync time.Duration
	// Omit lists wayland interface names the registry must not announce,
	// so a client that requires one can be tested failing.
	Omit []string
	// Width and Height are the size of the automatic first configure.
	// Zero means 640x480.
	Width, Height int32
	// ManualConfigure stops the fake from configuring the window by
	// itself after the first empty commit, leaving the timing to the test
	// through Configure.
	ManualConfigure bool
}

// Commit is one wl_surface.commit that carried a buffer, with the pixels
// copied out of the mapped pool at the moment the fake processed it.
type Commit struct {
	// Width and Height are the committed buffer's size in pixels.
	Width, Height int32
	// Pixels is a tight copy of the buffer, width*height words in row
	// order: the pool's stride padding is not included.
	Pixels []uint32
	// At is when the fake processed the commit.
	At time.Time
}

// BufferInfo is what the fake knows about one wl_buffer the client
// created, including its pixels as they are right now.
type BufferInfo struct {
	// ID is the wl_buffer's object id, which is what wl_surface.attach
	// names.
	ID uint32
	// Width, Height, Stride and Format are the wl_shm_pool.create_buffer
	// arguments.
	Width, Height, Stride int32
	// Format is the wl_shm format enum value.
	Format uint32
	// Live is false once the client sent wl_buffer.destroy.
	Live bool
	// Pixels is a tight copy of the buffer read from the mapped pool at
	// the moment Buffers() was called, so it follows the client's
	// painting without any request in between.
	Pixels []uint32
}

// Server is a scripted fake compositor: it speaks enough of wl_compositor,
// wl_shm, wl_seat and xdg_shell for a real client to open a window, paint
// into it and receive input, and it records what the client did so a test
// can assert on it.
//
// It owns the compositor end of the socketpair and runs its own reading
// goroutine, so a test that uses a Server must never call the low-level
// [Compositor.ReadRequest] on the same connection. Everything exported
// here is safe to call from the test goroutine while that loop runs.
//
// The test goroutine is the client's pump: it has to call Roundtrip or
// Dispatch, or the events the fake writes pile up in the socket unread.
//
// One window only: the fake follows the first wl_surface, xdg_surface and
// xdg_toplevel the client creates, which is what the window layer opens.
// Buffers are tracked for all of them.
type Server struct {
	conn *wlcore.Conn
	sock *net.UnixConn
	opts Options

	commits    chan Commit
	readerDone chan struct{}

	mu       sync.Mutex
	closed   bool
	timeBase time.Time
	serial   uint32
	timers   []*time.Timer

	objects map[uint32]string
	globals []globalEntry

	pools       map[uint32]*shmPool
	buffers     map[uint32]*bufferState
	bufferOrder []uint32

	wmBase      uint32
	surface     uint32
	xdgSurface  uint32
	toplevel    uint32
	seat        uint32
	seatVersion uint32
	keyboard    uint32
	pointerID   uint32

	title string
	appID string

	requests []string
	errors   []string

	// Surface double-buffered state, applied on commit.
	pendingAttach bool
	pendingBuffer uint32
	currentBuffer uint32
	heldBuffer    uint32
	pendingFrames []uint32

	configureSent bool
	acked         bool
	liveSerials   map[uint32]bool

	keymapFD   int
	keymapSize uint32

	keyboardFocus bool
	pointerFocus  bool
	pointerX      float64
	pointerY      float64
}

// globalEntry is one advertised global.
type globalEntry struct {
	name    uint32
	iface   string
	version uint32
}

// NewServer starts a fake compositor and the client connection that talks
// to it. Both ends, the reading goroutine, the pool mappings and the
// keymap descriptor are released with t.Cleanup.
//
// It calls t.Setenv through NewConn, so a test that uses it cannot be
// parallel.
func NewServer(t *testing.T, opts Options) *Server {
	t.Helper()

	if opts.Vsync == 0 {
		opts.Vsync = DefaultVsync
	}
	if opts.Width == 0 {
		opts.Width = defaultWidth
	}
	if opts.Height == 0 {
		opts.Height = defaultHeight
	}

	conn, comp := NewConn(t)
	sock := comp.Conn()
	// ReadRequest leaves an absolute deadline behind on this socket. The
	// loop below must not inherit one, and sets none of its own.
	sock.SetReadDeadline(time.Time{})

	s := &Server{
		conn:        conn,
		sock:        sock,
		opts:        opts,
		commits:     make(chan Commit, commitBuffer),
		readerDone:  make(chan struct{}),
		timeBase:    time.Now(),
		objects:     map[uint32]string{displayID: "wl_display"},
		pools:       map[uint32]*shmPool{},
		buffers:     map[uint32]*bufferState{},
		liveSerials: map[uint32]bool{},
		keymapFD:    -1,
	}
	s.globals = s.buildGlobals()

	go s.run()
	t.Cleanup(s.stop)
	return s
}

// buildGlobals lists what the registry announces, minus Options.Omit.
func (s *Server) buildGlobals() []globalEntry {
	// The versions are deliberately modest: every event these imply is one
	// the fake actually sends.
	all := []globalEntry{
		{iface: "wl_compositor", version: 4},
		{iface: "wl_shm", version: 1},
		{iface: "xdg_wm_base", version: 3},
	}
	if s.opts.Seat {
		all = append(all, globalEntry{iface: "wl_seat", version: 7})
	}

	omitted := map[string]bool{}
	for _, name := range s.opts.Omit {
		omitted[name] = true
	}

	var out []globalEntry
	for _, g := range all {
		if omitted[g.iface] {
			continue
		}
		g.name = uint32(len(out) + 1)
		out = append(out, g)
	}
	return out
}

// Conn returns the client end of the connection, a *wlcore.Conn the code
// under test drives as if it had come from wlcore.Connect.
func (s *Server) Conn() *wlcore.Conn { return s.conn }

// Commits returns the channel every committed frame is delivered on. It
// holds the last commitBuffer frames; when it is full the oldest is
// dropped rather than the fake blocking, so a slow test never stalls the
// compositor and the newest frame is always the one waiting.
func (s *Server) Commits() <-chan Commit { return s.commits }

// Requests returns every request the client has sent, as
// "interface.request", in arrival order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Errors returns the protocol violations the fake noticed, in order: an
// attach before the first ack_configure, an ack of a serial that was never
// sent, a request on an object that does not exist, a buffer that does not
// fit its pool. An empty result is the claim that the client behaved.
func (s *Server) Errors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.errors...)
}

// Buffers returns one entry per wl_buffer the client ever created, in
// creation order, with the pixels read from the mapped pool right now.
func (s *Server) Buffers() []BufferInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]BufferInfo, 0, len(s.bufferOrder))
	for _, id := range s.bufferOrder {
		b := s.buffers[id]
		if b == nil {
			continue
		}
		out = append(out, BufferInfo{
			ID:     b.id,
			Width:  b.width,
			Height: b.height,
			Stride: b.stride,
			Format: b.format,
			Live:   b.live,
			Pixels: b.snapshot(),
		})
	}
	return out
}

// LiveBuffers counts the buffers the client has not destroyed yet.
func (s *Server) LiveBuffers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, id := range s.bufferOrder {
		if b := s.buffers[id]; b != nil && b.live {
			n++
		}
	}
	return n
}

// Title returns the last xdg_toplevel.set_title the client sent.
func (s *Server) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// AppID returns the last xdg_toplevel.set_app_id the client sent.
func (s *Server) AppID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appID
}

// Configure sends one xdg_toplevel.configure followed by the
// xdg_surface.configure that ends the sequence, which is the whole handshake
// a client waits for. Use ConfigureToplevel and ConfigureSurface when a
// test needs several toplevel configures inside one sequence.
func (s *Server) Configure(width, height int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configureToplevel(width, height)
	s.configureSurface()
}

// ConfigureToplevel sends one xdg_toplevel.configure and nothing else. The
// client must not act on it until a ConfigureSurface closes the sequence.
// states are xdg_toplevel.state values, sent as the event's array.
func (s *Server) ConfigureToplevel(width, height int32, states ...uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configureToplevel(width, height, states...)
}

// ConfigureSurface ends a configure sequence with xdg_surface.configure and
// returns the serial the client has to ack.
func (s *Server) ConfigureSurface() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configureSurface()
}

// Ping sends xdg_wm_base.ping. The pong the client owes back shows up in
// Requests as "xdg_wm_base.pong"; a compositor marks a client that does
// not answer as unresponsive, so a window layer has to.
func (s *Server) Ping(serial uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wmBase == 0 {
		s.errorf("Ping with no bound xdg_wm_base")
		return
	}
	s.send(s.wmBase, evtWmBasePing, serial)
}

// CloseToplevel asks the client to close, as a title bar's close button
// does, by sending xdg_toplevel.close.
func (s *Server) CloseToplevel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toplevel == 0 {
		s.errorf("CloseToplevel with no xdg_toplevel")
		return
	}
	s.send(s.toplevel, evtToplevelClose)
}

// stop tears the fake down: it stops the timers, unmaps every pool, closes
// the descriptors it owns and waits for the reading goroutine to notice
// the socket is gone.
func (s *Server) stop() {
	s.mu.Lock()
	s.closed = true
	for _, tm := range s.timers {
		tm.Stop()
	}
	s.timers = nil
	for _, p := range s.pools {
		p.close()
	}
	s.pools = map[uint32]*shmPool{}
	if s.keymapFD >= 0 {
		unix.Close(s.keymapFD)
		s.keymapFD = -1
	}
	s.mu.Unlock()

	// Closing the socket is what unblocks the read in the loop. NewConn's
	// own cleanup closes it again afterwards; the second Close just fails.
	s.sock.Close()
	<-s.readerDone
}

// run is the reading loop. It owns nothing: every message is handled under
// the mutex, so an accessor called from the test goroutine sees a whole
// request applied or none of it.
func (s *Server) run() {
	defer close(s.readerDone)
	r := newWireReader(s.sock)
	defer r.closeFDs()

	for {
		id, opcode, body, err := r.next()
		if err != nil {
			return
		}
		s.handle(r, id, opcode, body)
	}
}

// errorf records a protocol violation. The caller holds the mutex.
func (s *Server) errorf(format string, a ...any) {
	s.errors = append(s.errors, fmt.Sprintf(format, a...))
}

// nowMS is the millisecond timestamp events carry.
func (s *Server) nowMS() uint32 {
	return uint32(time.Since(s.timeBase) / time.Millisecond)
}

func (s *Server) nextSerial() uint32 {
	s.serial++
	return s.serial
}

// send writes one event. The caller holds the mutex, which is what keeps
// the loop's events and the test goroutine's injections from interleaving
// mid-message on the socket.
func (s *Server) send(objectID uint32, opcode uint16, args ...uint32) {
	e := wlcore.NewEncoder()
	for _, a := range args {
		e.Uint32(a)
	}
	s.sendEncoded(objectID, opcode, e, -1)
}

// sendEncoded writes one event whose arguments are already encoded,
// optionally with a file descriptor attached.
func (s *Server) sendEncoded(objectID uint32, opcode uint16, e *wlcore.Encoder, fd int) {
	body := e.Bytes()
	msg := make([]byte, 8, 8+len(body))
	binary.NativeEndian.PutUint32(msg[0:4], objectID)
	binary.NativeEndian.PutUint32(msg[4:8], uint32(8+len(body))<<16|uint32(opcode))
	msg = append(msg, body...)

	var err error
	if fd >= 0 {
		_, _, err = s.sock.WriteMsgUnix(msg, unix.UnixRights(fd), nil)
	} else {
		_, err = s.sock.Write(msg)
	}
	if err != nil && !s.closed {
		s.errorf("writing event %d on object %d: %v", opcode, objectID, err)
	}
}

// deleteID forgets a client id and tells the client it may reuse it, the
// way libwayland does for every destroyed resource.
func (s *Server) deleteID(id uint32) {
	delete(s.objects, id)
	s.send(displayID, evtDisplayDeleteID, id)
}

// deliver hands a commit to the test, dropping the oldest one rather than
// blocking if nobody is reading.
func (s *Server) deliver(c Commit) {
	for {
		select {
		case s.commits <- c:
			return
		default:
		}
		select {
		case <-s.commits:
		default:
			// The consumer emptied it underneath us; try the send again.
		}
	}
}

// scheduleFrame answers a wl_surface.frame callback one vsync later, which
// is what makes a real client's frame clock advance.
func (s *Server) scheduleFrame(callback uint32) {
	tm := time.AfterFunc(s.opts.Vsync, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return
		}
		s.send(callback, evtCallbackDone, s.nowMS())
		s.deleteID(callback)
	})
	s.timers = append(s.timers, tm)
}
