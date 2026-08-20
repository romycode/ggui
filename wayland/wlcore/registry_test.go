package wlcore

import "testing"

type fakeBoundProxy struct {
	ProxyBase
	cleared bool
}

func (p *fakeBoundProxy) Dispatch(uint16, *Decoder) error { return nil }

// La factory hace lo que hará la generada: monta el tipo sobre el ProxyBase
// que le dan y engancha OnClear. clearListener() viene promocionado.
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

	// el global anuncia v10, el binding solo soporta hasta v3
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
		t.Fatalf("id enviado = %d, want %d (obj.ID())", id, obj.ID())
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
		t.Fatal("Bind() debería registrar el objeto")
	}
}

// destroy() llega al OnClear del tipo a través del clearListener()
// promocionado desde ProxyBase, sin que el tipo implemente nada.
func TestDestroyUsesPromotedClearListener(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	reg := newRegistry(2, 1, c)
	c.Register(reg)

	obj, err := reg.Bind(1, 1, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	c.destroy(obj)
	if !obj.cleared {
		t.Fatal("destroy() debería haber ejecutado el OnClear del objeto")
	}
}
