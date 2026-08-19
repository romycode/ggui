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

func TestDestroyClearsListener(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	c.destroy(p)
	if !p.listenerCleared {
		t.Fatal("destroy() debería llamar a clearListener()")
	}
}
