package wlcore

import "testing"

func TestRegistryBindRawWireFormat(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	reg := newRegistry(2, 1, c)
	c.Register(reg)

	if err := reg.bindRaw(1, "wl_compositor", 4, 6); err != nil {
		t.Fatalf("bindRaw: %v", err)
	}

	buf := make([]byte, 128)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	d := &Decoder{buf: buf[8:n]}
	name := d.Uint32()
	iface := d.String()
	version := d.Uint32()
	id := d.ID()
	if err := d.Err(); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != 1 || iface != "wl_compositor" || version != 4 || id != 6 {
		t.Fatalf("got name=%d iface=%q version=%d id=%d, want 1 wl_compositor 4 6",
			name, iface, version, id)
	}
}

func TestRegistryDispatchGlobal(t *testing.T) {
	c := newConn(nil)
	reg := newRegistry(2, 1, c)
	var gotName, gotVersion uint32
	var gotIface string
	reg.SetListener(RegistryListener{
		Global: func(name uint32, iface string, version uint32) {
			gotName, gotIface, gotVersion = name, iface, version
		},
	})

	body := NewEncoder().Uint32(3).String("wl_shm").Uint32(1).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobal, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotName != 3 || gotIface != "wl_shm" || gotVersion != 1 {
		t.Fatalf("got %d %q %d, want 3 wl_shm 1", gotName, gotIface, gotVersion)
	}
}

func TestRegistryDispatchGlobalRemove(t *testing.T) {
	c := newConn(nil)
	reg := newRegistry(2, 1, c)
	var got uint32
	reg.SetListener(RegistryListener{GlobalRemove: func(name uint32) { got = name }})

	body := NewEncoder().Uint32(3).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobalRemove, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestRegistryDispatchWithoutListenerDoesNotPanic(t *testing.T) {
	c := newConn(nil)
	reg := newRegistry(2, 1, c)

	body := NewEncoder().Uint32(3).String("wl_shm").Uint32(1).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobal, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch global: %v", err)
	}
	body = NewEncoder().Uint32(3).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobalRemove, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch global_remove: %v", err)
	}
}

func TestRegistryDispatchUnknownOpcode(t *testing.T) {
	c := newConn(nil)
	reg := newRegistry(2, 1, c)
	if err := reg.Dispatch(99, c.newDecoder(nil)); err == nil {
		t.Fatal("opcode desconocido debería devolver error")
	}
}

func TestRegistryClearListener(t *testing.T) {
	c := newConn(nil)
	reg := newRegistry(2, 1, c)
	called := false
	reg.SetListener(RegistryListener{GlobalRemove: func(uint32) { called = true }})
	reg.clearListener()

	body := NewEncoder().Uint32(3).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobalRemove, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if called {
		t.Fatal("tras clearListener, GlobalRemove no debería llamarse")
	}
}
