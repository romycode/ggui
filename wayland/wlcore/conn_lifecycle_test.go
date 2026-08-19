package wlcore

import "testing"

func TestDestroyClientIDStaysZombieUntilRelease(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)} // 5 < serverIDBase
	c.Register(p)

	c.destroy(p)
	if c.Lookup(5) == nil {
		t.Fatal("id de cliente destruido debería seguir en objects (zombie) hasta delete_id")
	}

	c.release(5)
	if c.Lookup(5) != nil {
		t.Fatal("tras release, el id debería haber desaparecido de objects")
	}
	if len(c.freeIDs) != 1 || c.freeIDs[0] != 5 {
		t.Fatalf("freeIDs = %v, want [5]", c.freeIDs)
	}
}

func TestDestroyServerIDRemovedImmediately(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(serverIDBase+1, 1, c)}
	c.Register(p)

	c.destroy(p)
	if c.Lookup(serverIDBase+1) != nil {
		t.Fatal("id de servidor debería desaparecer de objects en el acto")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want vacío (los ids de servidor no se reciclan)", c.freeIDs)
	}
}

// El id llega en un delete_id del compositor: hay que tratarlo como hostil.
func TestReleaseIgnoresDisplayID(t *testing.T) {
	c := newConn(nil)
	c.display = newDisplay(displayID, 1, c)
	c.Register(c.display)

	c.release(displayID)

	if c.Lookup(displayID) != Proxy(c.display) {
		t.Fatal("delete_id(1) no debería desregistrar wl_display")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want vacío (el id 1 no se recicla)", c.freeIDs)
	}
}

func TestReleaseIgnoresServerIDs(t *testing.T) {
	c := newConn(nil)
	id := uint32(serverIDBase + 1)
	c.Register(&fakeProxy{ProxyBase: NewProxyBase(id, 1, c)})

	c.release(id)

	if c.Lookup(id) == nil {
		t.Fatal("delete_id de un id de servidor no debería tocar objects")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want vacío (los ids de servidor no son del cliente)", c.freeIDs)
	}
}

func TestReleaseIgnoresRepeatedDeleteID(t *testing.T) {
	c := newConn(nil)
	c.Register(&fakeProxy{ProxyBase: NewProxyBase(5, 1, c)})

	c.release(5)
	c.release(5) // duplicado: el id ya no está vivo
	c.release(9) // nunca estuvo registrado

	if len(c.freeIDs) != 1 || c.freeIDs[0] != 5 {
		t.Fatalf("freeIDs = %v, want [5] (sin duplicados ni ids inventados)", c.freeIDs)
	}
	if got := c.NewID(); got != 5 {
		t.Fatalf("NewID() = %d, want 5", got)
	}
	if got := c.NewID(); got != 2 {
		t.Fatalf("segundo NewID() = %d, want 2 (freeIDs ya vacío)", got)
	}
}

func TestDestroyClearsListener(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	c.destroy(p)
	if !p.listenerCleared {
		t.Fatal("destroy() debería llamar a clearListener()")
	}
}
