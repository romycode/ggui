package wlcore

import "testing"

// fakeProxy es el Proxy mínimo para tests: registra los opcodes que recibe
// y si clearListener() se llamó. Se reutiliza en tareas posteriores.
type fakeProxy struct {
	ProxyBase
	dispatched      []uint16
	listenerCleared bool
}

func (p *fakeProxy) Dispatch(opcode uint16, d *Decoder) error {
	p.dispatched = append(p.dispatched, opcode)
	return nil
}

func (p *fakeProxy) clearListener() { p.listenerCleared = true }

func TestConnNewIDMonotonic(t *testing.T) {
	c := newConn(nil)
	if got := c.NewID(); got != 2 {
		t.Fatalf("primer NewID() = %d, want 2 (1 es displayID)", got)
	}
	if got := c.NewID(); got != 3 {
		t.Fatalf("segundo NewID() = %d, want 3", got)
	}
}

func TestConnNewIDReusesFreedBeforeGrowing(t *testing.T) {
	c := newConn(nil)
	c.NewID() // 2
	c.NewID() // 3
	c.freeIDs = append(c.freeIDs, 2)
	if got := c.NewID(); got != 2 {
		t.Fatalf("NewID() con freeIDs = %d, want 2 (reciclado)", got)
	}
	if got := c.NewID(); got != 4 {
		t.Fatalf("NewID() tras agotar freeIDs = %d, want 4", got)
	}
}

func TestConnRegisterAndLookup(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	if got := c.Lookup(5); got != Proxy(p) {
		t.Fatalf("Lookup(5) = %v, want %v", got, p)
	}
	if c.Lookup(999) != nil {
		t.Fatalf("Lookup de id no registrado debería ser nil")
	}
}
