// Package wltest is a minimal fake Wayland compositor for tests.
//
// It is the far end of a socketpair: a test hands the client side to code that
// expects a *wlcore.Conn, then reads the requests the client wrote and writes
// events by hand. It is not a real compositor and knows no protocol beyond the
// wire format, which is all the tests that use it need.
//
// The package imports testing and is meant to be imported from _test.go files
// only.
package wltest

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/wlcore"
)

// readTimeout bounds how long ReadRequest waits for the client to send
// something before it fails the test.
const readTimeout = 2 * time.Second

// Compositor is the far end of the socketpair a test Conn talks to.
type Compositor struct {
	t    *testing.T
	conn *net.UnixConn
}

// NewConn connects a wlcore.Conn to a Compositor over a socketpair. Both ends
// are closed when the test ends.
//
// Connect accepts WAYLAND_SOCKET, so no exported constructor is needed in
// wlcore. NewConn sets that variable with t.Setenv, so a test that calls it
// cannot be parallel.
func NewConn(t *testing.T) (*wlcore.Conn, *Compositor) {
	t.Helper()

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	f := os.NewFile(uintptr(fds[1]), "compositor")
	nc, err := net.FileConn(f)
	f.Close()
	if err != nil {
		t.Fatalf("FileConn: %v", err)
	}
	server := nc.(*net.UnixConn)

	t.Setenv("WAYLAND_SOCKET", strconv.Itoa(fds[0]))
	conn, err := wlcore.Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		server.Close()
	})
	return conn, &Compositor{t: t, conn: server}
}

// Conn returns the compositor's end of the socketpair, for a test that needs
// to do more than the helpers do: close it to simulate a compositor that hangs
// up, drain it, or set its buffer sizes.
func (c *Compositor) Conn() *net.UnixConn { return c.conn }

// ReadRequest returns the next request the client sent, and fails the test if
// none arrives within two seconds. It must be called from the test goroutine;
// use TryReadRequest from any other.
func (c *Compositor) ReadRequest() (objectID uint32, opcode uint16, body []byte) {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	objectID, opcode, body, err := c.TryReadRequest()
	if err != nil {
		c.t.Fatalf("%v", err)
	}
	return objectID, opcode, body
}

// TryReadRequest is ReadRequest for a goroutine that has to stop quietly when
// the connection ends, instead of failing the test from outside it. It blocks
// until a request arrives or the connection fails, and reports the failure as
// an error.
func (c *Compositor) TryReadRequest() (objectID uint32, opcode uint16, body []byte, err error) {
	var hdr [8]byte
	if _, err := readFull(c.conn, hdr[:]); err != nil {
		return 0, 0, nil, fmt.Errorf("reading request header: %w", err)
	}
	objectID = binary.NativeEndian.Uint32(hdr[0:4])
	sizeOp := binary.NativeEndian.Uint32(hdr[4:8])
	body = make([]byte, int(sizeOp>>16)-8)
	if _, err := readFull(c.conn, body); err != nil {
		return 0, 0, nil, fmt.Errorf("reading request body: %w", err)
	}
	return objectID, uint16(sizeOp & 0xffff), body, nil
}

func readFull(c *net.UnixConn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Send writes one event with uint32 arguments.
func (c *Compositor) Send(objectID uint32, opcode uint16, args ...uint32) {
	c.t.Helper()
	msg := make([]byte, 8+4*len(args))
	binary.NativeEndian.PutUint32(msg[0:4], objectID)
	binary.NativeEndian.PutUint32(msg[4:8], uint32(len(msg))<<16|uint32(opcode))
	for i, a := range args {
		binary.NativeEndian.PutUint32(msg[8+4*i:], a)
	}
	if _, err := c.conn.Write(msg); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// AnswerSync reads a wl_display.sync request and replies wl_callback.done, the
// round trip a real compositor makes for a Roundtrip.
func (c *Compositor) AnswerSync() {
	c.t.Helper()
	objectID, opcode, body := c.ReadRequest()
	if objectID != 1 || opcode != 0 || len(body) != 4 {
		c.t.Fatalf("expected wl_display.sync, got object %d opcode %d (%d body bytes)", objectID, opcode, len(body))
	}
	c.Send(binary.NativeEndian.Uint32(body), 0 /* wl_callback.done */, 0)
}
