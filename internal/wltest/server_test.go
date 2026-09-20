package wltest

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

// The tests below drive Server with a minimal client built from the real
// bindings. The test goroutine is that client's pump: it calls Roundtrip
// and Dispatch itself, which is exactly the single-goroutine contract
// wlcore asks for.

type globalInfo struct{ name, version uint32 }

type sizeHint struct{ w, h int32 }

// testClient is the smallest Wayland client that can exercise the fake: a
// registry, the three globals a window needs, an xdg toplevel and shm
// buffers.
type testClient struct {
	t    *testing.T
	conn *wlcore.Conn

	registry   *wlcore.Registry
	globals    map[string]globalInfo
	compositor *wlcore.Compositor
	shm        *wlcore.Shm
	wm         *xdgshell.WmBase
	seat       *wlcore.Seat

	formats []wlcore.ShmFormat
	caps    wlcore.SeatCapability

	surface *wlcore.Surface
	xdg     *xdgshell.Surface
	top     *xdgshell.Toplevel

	configures []sizeHint
	serial     uint32
	configured bool
	closed     bool
}

func newTestClient(t *testing.T, s *Server) *testClient {
	t.Helper()
	c := &testClient{t: t, conn: s.Conn(), globals: map[string]globalInfo{}}
	reg, err := c.conn.Display().GetRegistry()
	if err != nil {
		t.Fatalf("get_registry: %v", err)
	}
	c.registry = reg
	reg.SetListener(wlcore.RegistryListener{Global: func(name uint32, iface string, version uint32) {
		c.globals[iface] = globalInfo{name, version}
	}})
	c.roundtrip()
	return c
}

func (c *testClient) roundtrip() {
	c.t.Helper()
	if err := c.conn.Roundtrip(); err != nil {
		c.t.Fatalf("roundtrip: %v", err)
	}
}

// bind takes every global the fake announced, so a test that omits one
// simply ends up with a nil field.
func (c *testClient) bind() {
	c.t.Helper()
	if g, ok := c.globals["wl_compositor"]; ok {
		v, err := c.registry.Bind(g.name, g.version, wlcore.CompositorInterface)
		if err != nil {
			c.t.Fatalf("bind wl_compositor: %v", err)
		}
		c.compositor = v
	}
	if g, ok := c.globals["wl_shm"]; ok {
		v, err := c.registry.Bind(g.name, g.version, wlcore.ShmInterface)
		if err != nil {
			c.t.Fatalf("bind wl_shm: %v", err)
		}
		v.SetListener(wlcore.ShmListener{Format: func(f wlcore.ShmFormat) {
			c.formats = append(c.formats, f)
		}})
		c.shm = v
	}
	if g, ok := c.globals["xdg_wm_base"]; ok {
		v, err := c.registry.Bind(g.name, g.version, xdgshell.WmBaseInterface)
		if err != nil {
			c.t.Fatalf("bind xdg_wm_base: %v", err)
		}
		v.SetListener(xdgshell.WmBaseListener{Ping: func(serial uint32) { v.Pong(serial) }})
		c.wm = v
	}
	if g, ok := c.globals["wl_seat"]; ok {
		v, err := c.registry.Bind(g.name, g.version, wlcore.SeatInterface)
		if err != nil {
			c.t.Fatalf("bind wl_seat: %v", err)
		}
		c.seat = v
	}
	c.roundtrip()
}

// openWindow does the xdg handshake up to the first empty commit, which is
// what makes the fake send its initial configure.
func (c *testClient) openWindow(title string) {
	c.t.Helper()
	surf, err := c.compositor.CreateSurface()
	if err != nil {
		c.t.Fatalf("create_surface: %v", err)
	}
	c.surface = surf

	xs, err := c.wm.GetXdgSurface(surf)
	if err != nil {
		c.t.Fatalf("get_xdg_surface: %v", err)
	}
	c.xdg = xs
	xs.SetListener(xdgshell.SurfaceListener{Configure: func(serial uint32) {
		c.serial, c.configured = serial, true
	}})

	top, err := xs.GetToplevel()
	if err != nil {
		c.t.Fatalf("get_toplevel: %v", err)
	}
	c.top = top
	top.SetListener(xdgshell.ToplevelListener{
		Configure: func(w, h int32, _ []byte) { c.configures = append(c.configures, sizeHint{w, h}) },
		Close:     func() { c.closed = true },
	})
	if err := top.SetTitle(title); err != nil {
		c.t.Fatalf("set_title: %v", err)
	}
	if err := surf.Commit(); err != nil {
		c.t.Fatalf("commit: %v", err)
	}
	c.roundtrip()
}

func (c *testClient) ack() {
	c.t.Helper()
	if !c.configured {
		c.t.Fatal("no configure to ack")
	}
	if err := c.xdg.AckConfigure(c.serial); err != nil {
		c.t.Fatalf("ack_configure: %v", err)
	}
}

// clientBuffer is one shm buffer the client owns: the memfd, the mapping it
// paints into and the wl_buffer the fake sees.
type clientBuffer struct {
	t        *testing.T
	fd       int
	data     []byte
	px       []uint32
	pool     *wlcore.ShmPool
	buf      *wlcore.Buffer
	releases int
	freed    bool
}

func (c *testClient) newBuffer(w, h int32) *clientBuffer {
	c.t.Helper()
	stride := w * 4
	size := int(stride) * int(h)

	fd, err := unix.MemfdCreate("wltest-client", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		c.t.Fatalf("memfd_create: %v", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		c.t.Fatalf("ftruncate: %v", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		c.t.Fatalf("mmap: %v", err)
	}
	pool, err := c.shm.CreatePool(fd, int32(size))
	if err != nil {
		c.t.Fatalf("create_pool: %v", err)
	}
	buf, err := pool.CreateBuffer(0, w, h, stride, wlcore.ShmFormatArgb8888)
	if err != nil {
		c.t.Fatalf("create_buffer: %v", err)
	}
	cb := &clientBuffer{
		t:    c.t,
		fd:   fd,
		data: data,
		px:   unsafe.Slice((*uint32)(unsafe.Pointer(&data[0])), size/4),
		pool: pool,
		buf:  buf,
	}
	buf.SetListener(wlcore.BufferListener{Release: func() { cb.releases++ }})
	c.t.Cleanup(cb.free)
	return cb
}

func (b *clientBuffer) fill(v uint32) {
	for i := range b.px {
		b.px[i] = v
	}
}

// free drops the client's own mapping and descriptor. The fake keeps its
// own copy of both, so a buffer stays readable afterwards.
func (b *clientBuffer) free() {
	if b.freed {
		return
	}
	b.freed = true
	unix.Munmap(b.data)
	unix.Close(b.fd)
	b.data, b.px = nil, nil
}

// destroyAll tears the buffer down the way a resizing client does: the
// wl_buffer, then the wl_shm_pool, then its own memory. Both object ids go
// back to wlcore's free list, so whatever the client creates next reuses
// them.
func (b *clientBuffer) destroyAll() {
	b.t.Helper()
	if err := b.buf.Destroy(); err != nil {
		b.t.Fatalf("wl_buffer.destroy: %v", err)
	}
	if err := b.pool.Destroy(); err != nil {
		b.t.Fatalf("wl_shm_pool.destroy: %v", err)
	}
	b.free()
}

// clientPool is one shm pool the client keeps for the whole test and carves
// buffers out of. With a single long-lived pool, the only object ids that
// churn are the buffers' own (plus the sync callbacks' that Roundtrip uses),
// which is what a test about buffer id reuse needs: a test that also created
// and destroyed a pool per buffer could not say which kind of object an
// id would come back as, because that depends on when the client dispatches
// each delete_id.
type clientPool struct {
	t    *testing.T
	fd   int
	data []byte
	px   []uint32
	pool *wlcore.ShmPool
	next int // next unused byte offset
}

func (c *testClient) newPool(size int) *clientPool {
	c.t.Helper()
	fd, err := unix.MemfdCreate("wltest-client-pool", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		c.t.Fatalf("memfd_create: %v", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		c.t.Fatalf("ftruncate: %v", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		c.t.Fatalf("mmap: %v", err)
	}
	pool, err := c.shm.CreatePool(fd, int32(size))
	if err != nil {
		c.t.Fatalf("create_pool: %v", err)
	}
	p := &clientPool{
		t:    c.t,
		fd:   fd,
		data: data,
		px:   unsafe.Slice((*uint32)(unsafe.Pointer(&data[0])), size/4),
		pool: pool,
	}
	c.t.Cleanup(func() {
		unix.Munmap(p.data)
		unix.Close(p.fd)
	})
	return p
}

// newBuffer carves the next w*h buffer out of the pool and returns it with
// the client's view of its pixels. Offsets only grow, so two buffers never
// share memory even when one reuses the other's object id.
func (p *clientPool) newBuffer(w, h int32) (*wlcore.Buffer, []uint32) {
	p.t.Helper()
	stride := w * 4
	n := int(stride) * int(h)
	off := p.next
	p.next += n
	buf, err := p.pool.CreateBuffer(int32(off), w, h, stride, wlcore.ShmFormatArgb8888)
	if err != nil {
		p.t.Fatalf("create_buffer: %v", err)
	}
	return buf, p.px[off/4 : (off+n)/4]
}

func fillPixels(px []uint32, v uint32) {
	for i := range px {
		px[i] = v
	}
}

// openFDs counts this process's open descriptors, which is how a leaked
// pool shows up: one descriptor and one mapping per pool the fake can no
// longer reach.
func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot count open descriptors: %v", err)
	}
	return len(entries)
}

func awaitCommit(t *testing.T, s *Server) Commit {
	t.Helper()
	select {
	case c := <-s.Commits():
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no commit arrived")
	}
	return Commit{}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

func TestServerAnnouncesGlobals(t *testing.T) {
	s := NewServer(t, Options{Seat: true})
	c := newTestClient(t, s)

	for _, want := range []string{"wl_compositor", "wl_shm", "xdg_wm_base", "wl_seat"} {
		if _, ok := c.globals[want]; !ok {
			t.Errorf("the registry did not announce %s", want)
		}
	}
	c.bind()
	if len(c.formats) < 2 {
		t.Errorf("wl_shm announced %v, want at least argb8888 and xrgb8888", c.formats)
	}
	if errs := s.Errors(); len(errs) != 0 {
		t.Errorf("the fake recorded errors on a clean run: %v", errs)
	}
}

func TestServerWithoutSeatAnnouncesNoSeat(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	if _, ok := c.globals["wl_seat"]; ok {
		t.Error("wl_seat was announced with Options.Seat false")
	}
}

func TestServerOmitsGlobal(t *testing.T) {
	s := NewServer(t, Options{Seat: true, Omit: []string{"wl_shm"}})
	c := newTestClient(t, s)
	if _, ok := c.globals["wl_shm"]; ok {
		t.Error("wl_shm was announced although it is in Omit")
	}
	if _, ok := c.globals["wl_compositor"]; !ok {
		t.Error("Omit removed more than it was asked to")
	}
}

func TestServerConfiguresAfterTheFirstEmptyCommit(t *testing.T) {
	s := NewServer(t, Options{Width: 800, Height: 600})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")

	if len(c.configures) != 1 {
		t.Fatalf("got %d xdg_toplevel.configure events, want 1", len(c.configures))
	}
	if c.configures[0] != (sizeHint{800, 600}) {
		t.Errorf("configure said %v, want 800x600", c.configures[0])
	}
	if !c.configured {
		t.Error("no xdg_surface.configure followed the toplevel one")
	}
	want := []string{"wl_compositor.create_surface", "xdg_wm_base.get_xdg_surface", "xdg_surface.get_toplevel", "xdg_toplevel.set_title", "wl_surface.commit"}
	got := s.Requests()
	for _, w := range want {
		if !contains(got, w) {
			t.Errorf("Requests() has no %q; got %v", w, got)
		}
	}
	if s.Title() != "w" {
		t.Errorf("Title() = %q, want %q", s.Title(), "w")
	}
}

func TestServerConfigureOnDemandAndTwoToplevelConfigures(t *testing.T) {
	s := NewServer(t, Options{ManualConfigure: true})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")

	if len(c.configures) != 0 {
		t.Fatalf("ManualConfigure still configured: %v", c.configures)
	}

	s.ConfigureToplevel(100, 100)
	s.ConfigureToplevel(200, 150)
	s.ConfigureSurface()
	c.roundtrip()

	if len(c.configures) != 2 || c.configures[1] != (sizeHint{200, 150}) {
		t.Errorf("got %v, want two toplevel configures ending at 200x150", c.configures)
	}
	if !c.configured {
		t.Error("the xdg_surface.configure never arrived")
	}

	s.Configure(320, 240)
	c.roundtrip()
	if len(c.configures) != 3 || c.configures[2] != (sizeHint{320, 240}) {
		t.Errorf("got %v, want a third configure at 320x240", c.configures)
	}
}

func TestServerRecordsAttachBeforeAckConfigure(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")

	// No ack: the attach that follows is the protocol violation.
	b := c.newBuffer(4, 4)
	if err := c.surface.Attach(b.buf, 0, 0); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := c.surface.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	c.roundtrip()

	if !containsSubstring(s.Errors(), "ack_configure") {
		t.Errorf("Errors() = %v, want one naming ack_configure", s.Errors())
	}
}

func TestServerAcceptsAttachAfterAckConfigure(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")
	c.ack()

	b := c.newBuffer(4, 4)
	b.fill(0xff102030)
	c.surface.Attach(b.buf, 0, 0)
	c.surface.DamageBuffer(0, 0, 4, 4)
	c.surface.Commit()
	c.roundtrip()

	if errs := s.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	got := awaitCommit(t, s)
	if got.Width != 4 || got.Height != 4 {
		t.Errorf("commit was %dx%d, want 4x4", got.Width, got.Height)
	}
	for i, px := range got.Pixels {
		if px != 0xff102030 {
			t.Fatalf("pixel %d is %#08x, want %#08x", i, px, 0xff102030)
		}
	}
	if got.At.IsZero() {
		t.Error("Commit.At was never set")
	}
}

// The pixels come out of the mapping at call time, with no surface and no
// xdg handshake in the way.
func TestServerBuffersReadThePoolWithNoSurface(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()

	b := c.newBuffer(3, 2)
	b.fill(0x11223344)
	c.roundtrip()

	bufs := s.Buffers()
	if len(bufs) != 1 {
		t.Fatalf("Buffers() = %v, want one", bufs)
	}
	if bufs[0].Width != 3 || bufs[0].Height != 2 || bufs[0].Stride != 12 {
		t.Errorf("buffer is %dx%d stride %d, want 3x2 stride 12", bufs[0].Width, bufs[0].Height, bufs[0].Stride)
	}
	if !bufs[0].Live || s.LiveBuffers() != 1 {
		t.Errorf("buffer is not live: %+v, LiveBuffers=%d", bufs[0], s.LiveBuffers())
	}
	if len(bufs[0].Pixels) != 6 {
		t.Fatalf("got %d pixels, want 6", len(bufs[0].Pixels))
	}
	for i, px := range bufs[0].Pixels {
		if px != 0x11223344 {
			t.Fatalf("pixel %d is %#08x, want %#08x", i, px, 0x11223344)
		}
	}

	// The read is live: repainting without any request changes what
	// Buffers() reports.
	b.fill(0x55667788)
	if got := s.Buffers()[0].Pixels[0]; got != 0x55667788 {
		t.Errorf("after repainting, pixel 0 is %#08x, want %#08x", got, 0x55667788)
	}
}

func TestServerBufferStopsBeingLiveOnDestroy(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()

	a := c.newBuffer(2, 2)
	c.newBuffer(2, 2)
	c.roundtrip()
	if s.LiveBuffers() != 2 {
		t.Fatalf("LiveBuffers() = %d, want 2", s.LiveBuffers())
	}

	if err := a.buf.Destroy(); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	c.roundtrip()
	if s.LiveBuffers() != 1 {
		t.Errorf("LiveBuffers() = %d after one destroy, want 1", s.LiveBuffers())
	}
	if bufs := s.Buffers(); len(bufs) != 2 || bufs[0].Live {
		t.Errorf("Buffers() = %v, want two entries with the first dead", bufs)
	}
}

// A resizing window destroys its buffers and builds new ones, and wlcore hands
// the freed ids straight back out. The fake's bookkeeping has to survive that:
// one entry per buffer ever created, with the right liveness, however many
// times an id changes hands.
//
// The reuse is made certain rather than likely. An id comes back to the client
// only once it has dispatched the server's delete_id for it, and wlcore hands
// recycled ids out most-recently-freed first. Each cycle therefore destroys
// TWO buffers and creates two: after the roundtrip the free list ends in those
// two buffer ids, with at most one sync callback's id on top of them, so at
// least one of the next two buffers must take a buffer id it has seen. Creating
// one buffer per cycle would leave it to chance whether that id was a buffer's
// or a callback's.
func TestServerBuffersSurviveObjectIDReuse(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()
	pool := c.newPool(4096)

	const cycles = 8
	const (
		survivorPixel = 0x0a0b0c0d
		lastAPixel    = 0x0e0f1011
		lastBPixel    = 0x12131415
	)

	// One buffer that is never destroyed, so the counts below cannot pass by
	// everything simply being dead.
	keep, keepPx := pool.newBuffer(2, 2)
	fillPixels(keepPx, survivorPixel)
	seen := map[uint32]bool{keep.ID(): true}
	c.roundtrip()

	reusedCycles := 0
	for range cycles {
		a, _ := pool.newBuffer(1, 1)
		b, _ := pool.newBuffer(1, 1)
		if seen[a.ID()] || seen[b.ID()] {
			reusedCycles++
		}
		seen[a.ID()], seen[b.ID()] = true, true
		c.roundtrip()

		if err := a.Destroy(); err != nil {
			t.Fatalf("wl_buffer.destroy: %v", err)
		}
		if err := b.Destroy(); err != nil {
			t.Fatalf("wl_buffer.destroy: %v", err)
		}
		c.roundtrip() // the delete_ids come back and wlcore recycles the ids
	}

	// Every cycle after the first has two recycled ids to draw on.
	if reusedCycles != cycles-1 {
		t.Fatalf("a buffer id came back in %d of the %d cycles that could reuse one; the free list is not behaving as the test assumes",
			reusedCycles, cycles-1)
	}

	// The last two stay alive and at least one takes a recycled id. That is
	// the case a history keyed by object id gets wrong: the dead entry and the
	// live one share a key, so the live buffer is reported twice.
	lastA, lastAPx := pool.newBuffer(1, 1)
	lastB, lastBPx := pool.newBuffer(1, 1)
	if !seen[lastA.ID()] && !seen[lastB.ID()] {
		t.Fatalf("the surviving buffers got the fresh ids %d and %d, so this test no longer covers a live buffer on a recycled id",
			lastA.ID(), lastB.ID())
	}
	fillPixels(lastAPx, lastAPixel)
	fillPixels(lastBPx, lastBPixel)
	c.roundtrip()

	created := 1 + 2*cycles + 2
	bufs := s.Buffers()
	if len(bufs) != created {
		t.Errorf("Buffers() has %d entries after %d buffers were created, want one each", len(bufs), created)
	}
	if n := s.LiveBuffers(); n != 3 {
		t.Errorf("LiveBuffers() = %d, want 3: the buffer that was never destroyed and the last two", n)
	}
	live := map[uint32]int{}
	for _, b := range bufs {
		if b.Live {
			live[b.Pixels[0]]++
		}
	}
	if len(live) != 3 || live[survivorPixel] != 1 || live[lastAPixel] != 1 || live[lastBPixel] != 1 {
		t.Errorf("the live buffers read %v, want exactly one each of %#08x, %#08x and %#08x",
			live, survivorPixel, lastAPixel, lastBPixel)
	}
}

// Every pool the fake mapped has to be unmapped and closed at cleanup,
// including the ones whose object id the client later reused: a resizing
// window cycles a pool per resize, and a lost descriptor per cycle
// exhausts the process.
func TestServerClosesEveryPoolItMapped(t *testing.T) {
	const cycles = 40

	before := openFDs(t)
	t.Run("cycles", func(t *testing.T) {
		s := NewServer(t, Options{})
		c := newTestClient(t, s)
		c.bind()
		for range cycles {
			b := c.newBuffer(1, 1)
			c.roundtrip()
			b.destroyAll()
			c.roundtrip()
		}
	})

	if after := openFDs(t); after > before+4 {
		t.Errorf("%d descriptors open after %d pool cycles, %d before: the fake kept pools it could no longer reach",
			after, cycles, before)
	}
}

// Cleanup runs when the test goroutine has stopped pumping, so the fake
// can be stuck in a write to a socket nobody is draining. It must still
// come back: the socket is closed before the mutex is taken, which is what
// breaks the write.
func TestServerStopDoesNotHangOnABlockedWrite(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()

	// Small buffers in both directions, so a few kilobytes of events fill
	// the socket instead of a few hundred.
	rc, err := c.conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	rc.Control(func(fd uintptr) {
		unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, 2048)
	})
	if err := s.sock.SetWriteBuffer(2048); err != nil {
		t.Fatalf("SO_SNDBUF: %v", err)
	}

	// A backlog the fake has to answer, with nobody dispatching: every
	// sync costs it a done and a delete_id. A couple of hundred is far
	// more than a 2 KiB send buffer holds (each tiny message costs a whole
	// skb), and few enough that the client's own writes still fit.
	for range 200 {
		if _, err := c.conn.Display().Sync(); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	// The precondition, asserted rather than assumed: the fake is parked in a
	// write and is holding its mutex, which is exactly when an accessor cannot
	// get in. Probe until one blocks, so the test cannot pass by the fake
	// simply never having got stuck (a slow or fast host, a socket buffer that
	// turned out bigger than asked for).
	stuck := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !stuck; {
		probe := make(chan struct{})
		go func() {
			s.Requests() // blocks while the fake holds its mutex; returns after stop
			close(probe)
		}()
		select {
		case <-probe:
			time.Sleep(20 * time.Millisecond) // it answered: not stuck yet
		case <-time.After(150 * time.Millisecond):
			stuck = true
		}
	}
	if !stuck {
		t.Fatal("the fake never parked in a blocked write, so this test proves nothing about stop")
	}

	done := make(chan struct{})
	go func() {
		s.stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Server.stop did not return: the reading goroutine is blocked in a write while holding the mutex, and nothing closes the socket to break it")
	}
}

func TestServerFrameCallbackWaitsForVsync(t *testing.T) {
	const vsync = 150 * time.Millisecond
	s := NewServer(t, Options{Vsync: vsync})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")
	c.ack()

	b := c.newBuffer(2, 2)
	done := 0
	cb, err := c.surface.Frame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	cb.SetListener(wlcore.CallbackListener{Done: func(uint32) { done++ }})
	c.surface.Attach(b.buf, 0, 0)
	start := time.Now()
	c.surface.Commit()
	c.roundtrip()

	if done != 0 {
		t.Fatalf("the frame callback fired after %v, before the vsync", time.Since(start))
	}
	awaitCommit(t, s)

	// Now block until the callback lands.
	for done == 0 {
		if err := c.conn.Dispatch(); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < vsync {
		t.Errorf("the frame callback took %v, want at least %v", elapsed, vsync)
	}
}

func TestServerReleaseImmediately(t *testing.T) {
	s := NewServer(t, Options{ReleaseMode: ReleaseImmediately})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")
	c.ack()

	b := c.newBuffer(2, 2)
	c.surface.Attach(b.buf, 0, 0)
	c.surface.Commit()
	c.roundtrip()
	awaitCommit(t, s)

	if b.releases != 1 {
		t.Errorf("got %d releases after one commit, want 1", b.releases)
	}
}

func TestServerReleaseOnNextCommit(t *testing.T) {
	s := NewServer(t, Options{ReleaseMode: ReleaseOnNextCommit})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")
	c.ack()

	first := c.newBuffer(2, 2)
	second := c.newBuffer(2, 2)

	c.surface.Attach(first.buf, 0, 0)
	c.surface.Commit()
	c.roundtrip()
	awaitCommit(t, s)
	if first.releases != 0 {
		t.Fatalf("the first buffer was released with nothing to replace it")
	}

	c.surface.Attach(second.buf, 0, 0)
	c.surface.Commit()
	c.roundtrip()
	awaitCommit(t, s)
	if first.releases != 1 {
		t.Errorf("the first buffer got %d releases after the second commit, want 1", first.releases)
	}
	if second.releases != 0 {
		t.Errorf("the second buffer was released while it is still on screen")
	}
}

func TestServerKeyboardInputReachesTheClient(t *testing.T) {
	s := NewServer(t, Options{Seat: true})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")
	c.ack()

	kbd, err := keyboard.New(c.conn, c.seat)
	if err != nil {
		t.Fatalf("keyboard.New: %v", err)
	}
	var events []keyboard.Event
	var focus []bool
	kbd.OnKey = func(ev keyboard.Event) { events = append(events, ev) }
	kbd.OnFocus = func(su *wlcore.Surface) { focus = append(focus, su != nil) }
	kbd.OnError = func(err error) { t.Errorf("keyboard: %v", err) }
	c.seat.SetListener(wlcore.SeatListener{Capabilities: func(caps wlcore.SeatCapability) {
		kbd.SetCapabilities(caps)
	}})

	// A bound seat re-announces its capabilities on the next round trip
	// only if we ask for them again, so drive it explicitly.
	kbd.SetCapabilities(wlcore.SeatCapabilityKeyboard | wlcore.SeatCapabilityPointer)
	c.roundtrip() // the keymap fd arrives here

	if kbd.Keymap() == nil {
		t.Fatal("the fake never sent a usable keymap")
	}

	const keyA = 30 // KEY_A
	s.Key(keyA, true)
	s.Key(keyA, false)
	c.roundtrip()

	if len(focus) == 0 || !focus[0] {
		t.Errorf("the client never got keyboard focus: %v", focus)
	}
	if len(events) != 2 {
		t.Fatalf("got %d key events, want 2: %v", len(events), events)
	}
	if events[0].State != keyboard.Pressed || events[0].Evdev != keyA {
		t.Errorf("first event is %+v, want a press of %d", events[0], keyA)
	}
	if events[0].Text != "a" {
		t.Errorf("the press typed %q, want %q", events[0].Text, "a")
	}
	if events[1].State != keyboard.Released {
		t.Errorf("second event is %v, want a release", events[1].State)
	}
}

func TestServerPointerInputReachesTheClient(t *testing.T) {
	s := NewServer(t, Options{Seat: true})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")
	c.ack()

	ptr, err := pointer.New(c.conn, c.seat)
	if err != nil {
		t.Fatalf("pointer.New: %v", err)
	}
	var events []pointer.Event
	ptr.OnEvent = func(ev pointer.Event) { events = append(events, ev) }
	ptr.OnError = func(err error) { t.Errorf("pointer: %v", err) }
	ptr.SetCapabilities(wlcore.SeatCapabilityPointer)
	c.roundtrip()

	const btnLeft = 0x110
	s.PointerMotion(12, 34)
	s.PointerButton(btnLeft, true)
	s.PointerButton(btnLeft, false)
	c.roundtrip()

	var sawPosition, sawDown, sawUp bool
	for _, ev := range events {
		switch ev.Kind {
		case pointer.Position:
			if ev.X == 12 && ev.Y == 34 {
				sawPosition = true
			}
		case pointer.ButtonDown:
			sawDown = ev.Button == btnLeft
		case pointer.ButtonUp:
			sawUp = ev.Button == btnLeft
		}
	}
	if !sawPosition {
		t.Errorf("no position event at 12,34: %v", events)
	}
	if !sawDown || !sawUp {
		t.Errorf("the button press and release did not arrive: %v", events)
	}
}

func TestServerCloseToplevel(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")

	s.CloseToplevel()
	c.roundtrip()
	if !c.closed {
		t.Error("xdg_toplevel.close never reached the client")
	}
}

func TestServerPingIsAnswered(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()

	s.Ping(77)
	c.roundtrip() // delivers the ping, and the pong leaves with the next sync
	c.roundtrip() // ... which this one waits for the fake to have read
	if !contains(s.Requests(), "xdg_wm_base.pong") {
		t.Errorf("Requests() = %v, want a pong", s.Requests())
	}
}

func TestServerRecordsUnknownObject(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)

	// 0x0fffffff is in neither the client's nor the server's id range and
	// nothing ever created it.
	if err := c.conn.Send(0x0fffffff, 0, wlcore.NewEncoder()); err != nil {
		t.Fatalf("send: %v", err)
	}
	c.roundtrip()

	if !containsSubstring(s.Errors(), "unknown object") {
		t.Errorf("Errors() = %v, want one naming an unknown object", s.Errors())
	}
}

func TestServerRequestsAreRecordedInOrder(t *testing.T) {
	s := NewServer(t, Options{})
	c := newTestClient(t, s)
	c.bind()
	c.openWindow("w")

	got := s.Requests()
	order := []string{"wl_display.get_registry", "wl_registry.bind", "wl_compositor.create_surface", "xdg_wm_base.get_xdg_surface", "xdg_surface.get_toplevel", "xdg_toplevel.set_title", "wl_surface.commit"}
	i := 0
	for _, r := range got {
		if i < len(order) && r == order[i] {
			i++
		}
	}
	if i != len(order) {
		t.Errorf("Requests() = %v\ndid not contain %v in order (stopped at %q)", got, order, order[i])
	}
}

// The fake keeps its own copy of the keyboard package's live multigroup keymap,
// because a package may not reach into another's testdata at run time. A copy
// drifts silently, and a fake that serves a keymap the keyboard tests no longer
// exercise proves nothing: this makes the drift a failure.
func TestTheKeymapCopyIsIdenticalToTheKeyboardPackages(t *testing.T) {
	const name = "live-multigroup.xkb"
	ours, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading the fake's copy: %v", err)
	}
	theirs, err := os.ReadFile("../../keyboard/testdata/" + name)
	if err != nil {
		t.Fatalf("reading the keyboard package's original: %v", err)
	}
	if !bytes.Equal(ours, theirs) {
		t.Errorf("internal/wltest/testdata/%s (%d bytes) differs from keyboard/testdata/%s (%d bytes): copy it again",
			name, len(ours), name, len(theirs))
	}
}
