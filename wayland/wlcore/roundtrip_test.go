package wlcore

import "testing"

func TestRoundtripBlocksUntilCallbackDone(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.display = disp
	c.Register(disp)

	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		n, err := server.Read(buf) // consume el wl_display.sync
		if err != nil {
			serverErr <- err
			return
		}
		d := &Decoder{buf: buf[8:n]}
		cbID := d.ID()
		body := NewEncoder().Uint32(0).Bytes()
		_, err = server.Write(rawMessage(cbID, opEvtCallbackDone, body))
		serverErr <- err
	}()

	if err := c.Roundtrip(); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("simulación de servidor: %v", err)
	}
}

func TestRoundtripPropagatesDispatchError(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.display = disp
	c.Register(disp)

	go func() {
		buf := make([]byte, 32)
		server.Read(buf) // consume el sync y no responde
		server.Close()   // fuerza el error de lectura en el siguiente Dispatch
	}()

	if err := c.Roundtrip(); err == nil {
		t.Fatal("Roundtrip debería propagar el error si la conexión se cae antes del done")
	}
}
