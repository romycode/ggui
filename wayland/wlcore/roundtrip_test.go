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
		n, err := server.Read(buf) // consume the wl_display.sync
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
		t.Fatalf("server simulation: %v", err)
	}
}

// The compositor sends wl_display.error and the sync's done in the same
// write: both messages arrive in a single Dispatch and the done still
// gets delivered, so if Dispatch didn't look at c.err, Roundtrip would
// say everything went fine after a protocol error.
func TestRoundtripReportsErrorBundledWithCallbackDone(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
	c.display = disp
	c.Register(disp)
	disp.listener = DisplayListener{
		Error: func(objectID, code uint32, msg string) {
			c.fatal(&ProtocolError{ObjectID: objectID, Code: code, Message: msg})
		},
		DeleteID: c.release,
	}

	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		n, err := server.Read(buf) // consume the wl_display.sync
		if err != nil {
			serverErr <- err
			return
		}
		d := &Decoder{buf: buf[8:n]}
		cbID := d.ID()

		errBody := NewEncoder().ID(3).Uint32(7).String("boom").Bytes()
		out := rawMessage(displayID, opEvtDisplayError, errBody)
		out = append(out, rawMessage(cbID, opEvtCallbackDone, NewEncoder().Uint32(0).Bytes())...)
		_, err = server.Write(out) // both, in a single write
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
		t.Fatalf("server simulation: %v", err)
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
		server.Read(buf) // consume the sync and don't respond
		server.Close()   // force a read error on the next Dispatch
	}()

	if err := c.Roundtrip(); err == nil {
		t.Fatal("Roundtrip should propagate the error if the connection drops before done")
	}
}
