package wlcore

import "testing"

type fakeBoundProxy struct {
	ProxyBase
	cleared bool
}

func (p *fakeBoundProxy) Dispatch(uint16, *Decoder) error { return nil }

// The factory does what the generated one will do: it builds the type on
// top of the given ProxyBase and hooks up OnClear. clearListener() comes
// promoted.
var fakeInterface = Interface[*fakeBoundProxy]{
	Name:       "wl_fake",
	MaxVersion: 3,
	New: func(b ProxyBase) *fakeBoundProxy {
		p := &fakeBoundProxy{ProxyBase: b}
		p.OnClear = func() { p.cleared = true }
		return p
	},
}

func TestBindNegotiatesMinVersion(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	reg := newRegistry(2, 1, c)
	c.Register(reg)

	// the global advertises v10, the binding only supports up to v3
	obj, err := reg.Bind(7, 10, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if obj.Version() != 3 {
		t.Fatalf("Version() = %d, want 3 (min(10,3))", obj.Version())
	}

	buf := make([]byte, 64)
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
	if name != 7 || iface != "wl_fake" || version != 3 {
		t.Fatalf("got name=%d iface=%q version=%d, want 7 wl_fake 3", name, iface, version)
	}
	if id != obj.ID() {
		t.Fatalf("sent id = %d, want %d (obj.ID())", id, obj.ID())
	}
}

func TestBindRegistersObjectBeforeSending(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	reg := newRegistry(2, 1, c)
	c.Register(reg)

	obj, err := reg.Bind(1, 1, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Lookup(obj.ID()) != Proxy(obj) {
		t.Fatal("Bind() should register the object")
	}
}

// destroy() reaches the type's OnClear through the clearListener()
// promoted from ProxyBase, without the type implementing anything.
func TestDestroyUsesPromotedClearListener(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	reg := newRegistry(2, 1, c)
	c.Register(reg)

	obj, err := reg.Bind(1, 1, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	c.Destroy(obj)
	if !obj.cleared {
		t.Fatal("destroy() should have run the object's OnClear")
	}
}
