package wlcore

import "testing"

func TestDisplaySyncRegistersAndSendsRequest(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
	c.display = disp
	c.Register(disp)

	cb, err := disp.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if cb.ID() != 2 {
		t.Fatalf("cb.ID() = %d, want 2", cb.ID())
	}
	if c.Lookup(2) != Proxy(cb) {
		t.Fatal("Sync() no registró el callback")
	}

	buf := make([]byte, 32)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	d := &Decoder{buf: buf[8:n]}
	if got := d.ID(); got != 2 {
		t.Fatalf("id enviado = %d, want 2", got)
	}
}

func TestDisplayGetRegistryRegistersAndSendsRequest(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
	c.display = disp
	c.Register(disp)

	reg, err := disp.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	if c.Lookup(reg.ID()) != Proxy(reg) {
		t.Fatal("GetRegistry() no registró el registry")
	}

	buf := make([]byte, 32)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayDispatchError(t *testing.T) {
	c := newConn(nil)
	disp := newDisplay(displayID, 1, c)
	var gotObj, gotCode uint32
	var gotMsg string
	disp.SetListener(DisplayListener{Error: func(objectID, code uint32, msg string) {
		gotObj, gotCode, gotMsg = objectID, code, msg
	}})

	body := NewEncoder().ID(5).Uint32(1).String("boom").Bytes()
	if err := disp.Dispatch(opEvtDisplayError, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotObj != 5 || gotCode != 1 || gotMsg != "boom" {
		t.Fatalf("got %d %d %q, want 5 1 boom", gotObj, gotCode, gotMsg)
	}
}

func TestDisplayDispatchDeleteID(t *testing.T) {
	c := newConn(nil)
	disp := newDisplay(displayID, 1, c)
	var got uint32
	disp.SetListener(DisplayListener{DeleteID: func(id uint32) { got = id }})

	body := NewEncoder().ID(42).Bytes()
	if err := disp.Dispatch(opEvtDisplayDeleteID, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestDisplayDispatchWithoutListenerDoesNotPanic(t *testing.T) {
	c := newConn(nil)
	disp := newDisplay(displayID, 1, c)

	body := NewEncoder().ID(5).Uint32(1).String("boom").Bytes()
	if err := disp.Dispatch(opEvtDisplayError, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	body = NewEncoder().ID(42).Bytes()
	if err := disp.Dispatch(opEvtDisplayDeleteID, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch delete_id: %v", err)
	}
}

func TestDisplayDispatchUnknownOpcode(t *testing.T) {
	c := newConn(nil)
	disp := newDisplay(displayID, 1, c)
	if err := disp.Dispatch(99, c.newDecoder(nil)); err == nil {
		t.Fatal("opcode desconocido debería devolver error")
	}
}

func TestDisplayClearListener(t *testing.T) {
	c := newConn(nil)
	disp := newDisplay(displayID, 1, c)
	called := false
	disp.SetListener(DisplayListener{DeleteID: func(uint32) { called = true }})
	disp.clearListener()

	body := NewEncoder().ID(42).Bytes()
	if err := disp.Dispatch(opEvtDisplayDeleteID, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if called {
		t.Fatal("tras clearListener, DeleteID no debería llamarse")
	}
}
