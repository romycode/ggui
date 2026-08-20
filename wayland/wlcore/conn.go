package wlcore

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// id 1 is wl_display: NewID doesn't assign it, Connect() constructs it.
	displayID = 1
	// Split of the id space: client at the bottom, server at the top.
	// maxClientID documents the limit but NewID doesn't check it — reaching
	// 250M live client ids at once isn't a realistic case for a desktop
	// client, and freeIDs recycles them long before getting close.
	maxClientID  = 0xFEFFFFFF
	serverIDBase = 0xFF000000
)

type Conn struct {
	sock *net.UnixConn

	// objects, nextID and freeIDs, same as in/fds/oob: no lock, because
	// the whole Conn API is used from a single goroutine (see "Who pumps"
	// in wlcore.md).
	objects map[uint32]Proxy
	nextID  uint32
	freeIDs []uint32 // ids returned by wl_display.delete_id

	display *Display // object 1, built in Connect()
	onError func(objectID, code uint32, msg string)

	errOnce sync.Once
	done    chan struct{}
	err     error

	in  readBuf // bytes read, unprocessed
	fds fdQueue // fds received, not yet consumed by Proxy.Dispatch
	oob []byte  // ancillary data buffer, reused on every recvmsg
}

func newConn(sock *net.UnixConn) *Conn {
	return &Conn{
		sock:    sock,
		objects: make(map[uint32]Proxy),
		nextID:  displayID, // the first NewID() returns 2
		in:      readBuf{data: make([]byte, readBufSize)},
		oob:     make([]byte, unix.CmsgSpace(4*maxFDsPerRead)),
		done:    make(chan struct{}),
	}
}

func (c *Conn) Register(p Proxy) {
	c.objects[p.ID()] = p
}

func (c *Conn) Lookup(id uint32) Proxy {
	return c.objects[id]
}

// NewID recycles before growing: without this, a long session with frame
// callbacks exhausts the id space within a few hours.
func (c *Conn) NewID() uint32 {
	if n := len(c.freeIDs); n > 0 {
		id := c.freeIDs[n-1]
		c.freeIDs = c.freeIDs[:n-1]
		return id
	}
	c.nextID++
	return c.nextID
}

// Display returns object 1, already registered by Connect().
func (c *Conn) Display() *Display { return c.display }

// fatal sets the terminal error the first time, closes done and the
// socket — that unblocks whoever is parked in ReadMsgUnix.
func (c *Conn) fatal(err error) {
	if err == nil {
		return
	}
	c.errOnce.Do(func() {
		c.err = err
		close(c.done)
		c.sock.Close()
	})
}

func (c *Conn) Done() <-chan struct{} { return c.done }
func (c *Conn) Err() error            { return c.err } // valid after Done()

// Destroy is the runtime behind the generated Destroy(): it always clears
// the listener, and frees the id right away if it was server-owned — the
// client-owned id isn't freed until delete_id arrives. Exported (unlike
// clearListener) because the generated code that calls it lives in
// whatever extension package, not just wlcore — an unexported method
// would be literally impossible to call from xdgshell, wlrlayershell,
// etc. (see "Naming conventions" in waygenerator.md).
func (c *Conn) Destroy(p Proxy) {
	p.clearListener()
	if id := p.ID(); id >= serverIDBase {
		delete(c.objects, id)
	}
}

// release frees a client id. Only called by the internal handler for
// wl_display.delete_id, meaning the id comes from the compositor: untrusted
// input, like everything that gets decoded. Three guards before touching
// anything, because all three cases would break the connection silently:
//   - object 1 is never freed: without wl_display there's neither an error
//     path nor delete_id, the client goes blind.
//   - server ids aren't recycled by the client, they aren't its to recycle.
//   - a repeated delete_id would push the same id twice into freeIDs and
//     NewID would hand it out to two live objects at once.
func (c *Conn) release(id uint32) {
	if id == displayID || id >= serverIDBase {
		return
	}
	if _, ok := c.objects[id]; !ok {
		return
	}
	delete(c.objects, id)
	c.freeIDs = append(c.freeIDs, id)
}

type ProtocolError struct {
	ObjectID uint32
	Code     uint32
	Message  string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wlcore: object %d: %s (code %d)", e.ObjectID, e.Message, e.Code)
}

func dial() (*net.UnixConn, error) {
	if s, ok := os.LookupEnv("WAYLAND_SOCKET"); ok {
		// Always remove it from the environment, and before validating it:
		// otherwise any child process we launch inherits the variable and
		// shares the stream with us — even if the value was garbage and we
		// bail out on the error below.
		os.Unsetenv("WAYLAND_SOCKET")

		fd, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("wlcore: invalid WAYLAND_SOCKET: %q", s)
		}
		syscall.CloseOnExec(fd)

		f := os.NewFile(uintptr(fd), "wayland")
		defer f.Close() // FileConn duplicates the fd and puts its own in non-blocking mode
		nc, err := net.FileConn(f)
		if err != nil {
			return nil, err
		}
		uc, ok := nc.(*net.UnixConn)
		if !ok {
			nc.Close()
			return nil, fmt.Errorf("wlcore: WAYLAND_SOCKET is not a unix socket")
		}
		return uc, nil
	}

	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	path := name
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, errors.New("wlcore: neither WAYLAND_SOCKET nor XDG_RUNTIME_DIR")
		}
		path = filepath.Join(dir, name)
	}
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
}

// Connect builds object 1 by hand — it's the only one that doesn't go
// through NewID() — and hooks up the internal listener before starting the
// loop, so as not to miss an early error.
func Connect() (*Conn, error) {
	sock, err := dial()
	if err != nil {
		return nil, err
	}
	c := newConn(sock)

	c.display = newDisplay(displayID, 1, c)
	c.Register(c.display)
	c.display.listener = DisplayListener{
		Error: func(objectID, code uint32, msg string) {
			if c.onError != nil {
				c.onError(objectID, code, msg)
			}
			c.fatal(&ProtocolError{ObjectID: objectID, Code: code, Message: msg})
		},
		DeleteID: c.release,
	}

	return c, nil
}

var ErrClosed = errors.New("wlcore: connection closed by the client")

// Close closes the connection. Idempotent, and safe to call with the
// connection already down: errOnce keeps the first error, so a deferred
// Close() doesn't mask the real failure.
func (c *Conn) Close() error {
	c.fatal(ErrClosed)
	return nil
}

// OnError registers the callback invoked when the compositor sends
// wl_display.error. It fully replaces Display's listener, which the user
// must not touch. Like any SetListener, it has to be called before the
// first Dispatch()/Run() so as not to miss an early error.
func (c *Conn) OnError(f func(objectID, code uint32, msg string)) { c.onError = f }

// Roundtrip creates the sync and pumps until its done arrives. It can't be
// called from within a listener (reentrant dispatch) nor from a goroutine
// other than the one pumping.
func (c *Conn) Roundtrip() error {
	cb, err := c.display.Sync()
	if err != nil {
		return err
	}
	done := false
	cb.SetListener(CallbackListener{Done: func(uint32) { done = true }})
	for !done {
		if err := c.Dispatch(); err != nil {
			return err
		}
	}
	return nil
}
