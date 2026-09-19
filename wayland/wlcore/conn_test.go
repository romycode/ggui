package wlcore

import (
	"testing"

	"golang.org/x/sys/unix"
)

// fakeProxy is the minimal Proxy for tests: it records the opcodes it
// receives and whether clearListener() was called. Reused in later tasks.
type fakeProxy struct {
	ProxyBase
	dispatched      []uint16
	listenerCleared bool
}

func (p *fakeProxy) Dispatch(opcode uint16, d *Decoder) error {
	p.dispatched = append(p.dispatched, opcode)
	return nil
}

func (p *fakeProxy) clearListener() { p.listenerCleared = true }

func TestConnNewIDMonotonic(t *testing.T) {
	c := newConn(nil)
	if got := c.NewID(); got != 2 {
		t.Fatalf("first NewID() = %d, want 2 (1 is displayID)", got)
	}
	if got := c.NewID(); got != 3 {
		t.Fatalf("second NewID() = %d, want 3", got)
	}
}

func TestConnNewIDReusesFreedBeforeGrowing(t *testing.T) {
	c := newConn(nil)
	c.NewID() // 2
	c.NewID() // 3
	c.freeIDs = append(c.freeIDs, 2)
	if got := c.NewID(); got != 2 {
		t.Fatalf("NewID() with freeIDs = %d, want 2 (recycled)", got)
	}
	if got := c.NewID(); got != 4 {
		t.Fatalf("NewID() after exhausting freeIDs = %d, want 4", got)
	}
}

func TestConnRegisterAndLookup(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	if got := c.Lookup(5); got != Proxy(p) {
		t.Fatalf("Lookup(5) = %v, want %v", got, p)
	}
	if c.Lookup(999) != nil {
		t.Fatalf("Lookup of an unregistered id should be nil")
	}
}

// An event loop that waits on the socket next to an eventfd needs the
// socket's descriptor; what it gets has to be the connection's own socket.
func TestSyscallConnExposesTheSocketDescriptor(t *testing.T) {
	c, _ := newDispatchTestConn(t)

	rc, err := c.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var typ int
	var sockErr error
	if err := rc.Control(func(fd uintptr) {
		typ, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE)
	}); err != nil {
		t.Fatalf("Control: %v", err)
	}
	if sockErr != nil {
		t.Fatalf("SO_TYPE on the descriptor: %v", sockErr)
	}
	if typ != unix.SOCK_STREAM {
		t.Errorf("descriptor has socket type %d, want SOCK_STREAM (%d)", typ, unix.SOCK_STREAM)
	}
}
