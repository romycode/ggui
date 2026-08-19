package wlcore

import (
	"errors"
	"net"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDialWaylandDisplayRelativeToXDGRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-test"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	os.Unsetenv("WAYLAND_SOCKET")

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()

	uc, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	uc.Close()
	<-accepted
}

func TestDialWaylandDisplayAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-abs"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("WAYLAND_DISPLAY", sockPath)
	os.Unsetenv("XDG_RUNTIME_DIR")
	os.Unsetenv("WAYLAND_SOCKET")

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()

	uc, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	uc.Close()
	<-accepted
}

func TestDialMissingXDGRuntimeDir(t *testing.T) {
	os.Unsetenv("WAYLAND_SOCKET")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	os.Unsetenv("XDG_RUNTIME_DIR")

	if _, err := dial(); err == nil {
		t.Fatal("dial() sin XDG_RUNTIME_DIR debería fallar")
	}
}

func TestDialWaylandSocket(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	other := os.NewFile(uintptr(fds[1]), "other")
	defer other.Close()

	t.Setenv("WAYLAND_SOCKET", strconv.Itoa(fds[0]))

	uc, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer uc.Close()

	if _, ok := os.LookupEnv("WAYLAND_SOCKET"); ok {
		t.Fatal("dial() debería limpiar WAYLAND_SOCKET del entorno")
	}
}

func TestConnectWiresDeleteIDToRelease(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-connect"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-connect")
	os.Unsetenv("WAYLAND_SOCKET")

	serverDone := make(chan *net.UnixConn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serverDone <- conn.(*net.UnixConn)
	}()

	c, err := Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.sock.Close()

	server := <-serverDone
	defer server.Close()

	if c.Display().ID() != displayID {
		t.Fatalf("Display().ID() = %d, want %d", c.Display().ID(), displayID)
	}

	// consume el 2 y registra un objeto con él: release() solo libera ids
	// que estén vivos en la tabla.
	id := c.NewID()
	c.Register(&fakeProxy{ProxyBase: NewProxyBase(id, 1, c)})

	body := NewEncoder().ID(2).Bytes()
	if _, err := server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body)); err != nil {
		t.Fatal(err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if got := c.NewID(); got != 2 {
		t.Fatalf("NewID() tras delete_id = %d, want 2 (reciclado)", got)
	}
}

func TestConnectWiresErrorToFatal(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-connect-err"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-connect-err")
	os.Unsetenv("WAYLAND_SOCKET")

	serverDone := make(chan *net.UnixConn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serverDone <- conn.(*net.UnixConn)
	}()

	c, err := Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.sock.Close()

	var gotObj uint32
	c.OnError(func(objectID, code uint32, msg string) { gotObj = objectID })

	server := <-serverDone
	defer server.Close()

	body := NewEncoder().ID(1).Uint32(2).String("bad").Bytes()
	if _, err := server.Write(rawMessage(displayID, opEvtDisplayError, body)); err != nil {
		t.Fatal(err)
	}
	// Dispatch tiene que devolver el error aunque el mensaje se decodificara
	// bien: el listener de error registró el fatal por dentro.
	err = c.Dispatch()
	var dispatchErr *ProtocolError
	if !errors.As(err, &dispatchErr) {
		t.Fatalf("Dispatch() = %v, want *ProtocolError", err)
	}

	if gotObj != 1 {
		t.Fatalf("onError no se llamó con objectID=1, got %d", gotObj)
	}
	var protoErr *ProtocolError
	if !errors.As(c.Err(), &protoErr) {
		t.Fatalf("Err() = %v, want *ProtocolError", c.Err())
	}
}
