package wlcore

import (
	"encoding/binary"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// newSocketpairConns crea un par de *net.UnixConn conectados con un
// socketpair SOCK_STREAM, para simular al compositor sin un socket
// Wayland real.
func newSocketpairConns(t *testing.T) (client, server *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	toConn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "socketpair")
		nc, err := net.FileConn(f)
		f.Close()
		if err != nil {
			t.Fatalf("FileConn: %v", err)
		}
		uc, ok := nc.(*net.UnixConn)
		if !ok {
			t.Fatalf("FileConn no devolvió un *net.UnixConn")
		}
		return uc
	}
	client = toConn(fds[0])
	server = toConn(fds[1])
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

// rawMessage construye un mensaje Wayland crudo: header (objectID,
// size<<16|opcode) + body. Lo usan los tests que simulan al compositor
// escribiendo bytes directamente.
func rawMessage(objectID uint32, opcode uint16, body []byte) []byte {
	total := 8 + len(body)
	buf := make([]byte, 8, total)
	binary.NativeEndian.PutUint32(buf[0:4], objectID)
	binary.NativeEndian.PutUint32(buf[4:8], uint32(total)<<16|uint32(opcode))
	return append(buf, body...)
}
