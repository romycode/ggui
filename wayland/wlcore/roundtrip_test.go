package wlcore

import (
	"errors"
	"testing"
)

func TestRoundtripBlocksUntilCallbackDone(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
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

// El compositor manda wl_display.error y el done del sync en el mismo write:
// los dos mensajes entran en un solo Dispatch y el done llega igualmente, así
// que si Dispatch no mirase c.err, Roundtrip diría que todo fue bien después
// de un error de protocolo.
func TestRoundtripReportsErrorBundledWithCallbackDone(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
	c.display = disp
	c.Register(disp)
	disp.SetListener(DisplayListener{
		Error: func(objectID, code uint32, msg string) {
			c.fatal(&ProtocolError{ObjectID: objectID, Code: code, Message: msg})
		},
		DeleteID: c.release,
	})

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

		errBody := NewEncoder().ID(3).Uint32(7).String("boom").Bytes()
		out := rawMessage(displayID, opEvtDisplayError, errBody)
		out = append(out, rawMessage(cbID, opEvtCallbackDone, NewEncoder().Uint32(0).Bytes())...)
		_, err = server.Write(out) // los dos, en un único write
		serverErr <- err
	}()

	err := c.Roundtrip()
	var protoErr *ProtocolError
	if !errors.As(err, &protoErr) {
		t.Fatalf("Roundtrip() = %v, want *ProtocolError", err)
	}
	if protoErr.ObjectID != 3 || protoErr.Code != 7 || protoErr.Message != "boom" {
		t.Fatalf("ProtocolError = %+v, want {3 7 boom}", protoErr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("simulación de servidor: %v", err)
	}
}

func TestRoundtripPropagatesDispatchError(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
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
