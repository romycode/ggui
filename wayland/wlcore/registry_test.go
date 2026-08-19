package wlcore

import "testing"

type fakeBoundProxy struct {
	ProxyBase
}

func (p *fakeBoundProxy) Dispatch(uint16, *Decoder) error { return nil }
func (p *fakeBoundProxy) clearListener()                  {}

var fakeInterface = Interface[*fakeBoundProxy]{
	Name:       "wl_fake",
	MaxVersion: 3,
	New:        func(b ProxyBase) *fakeBoundProxy { return &fakeBoundProxy{ProxyBase: b} },
}

func TestBindNegotiatesMinVersion(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	c.Register(reg)

	// el global anuncia v10, el binding solo soporta hasta v3
	obj, err := Bind(reg, 7, 10, fakeInterface)
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
		t.Fatalf("id enviado = %d, want %d (obj.ID())", id, obj.ID())
	}
}

func TestBindRegistersObjectBeforeSending(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	c.Register(reg)

	obj, err := Bind(reg, 1, 1, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Lookup(obj.ID()) != Proxy(obj) {
		t.Fatal("Bind() debería registrar el objeto")
	}
}
