package wlcore

import "testing"

func TestDestroyClientIDStaysZombieUntilRelease(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)} // 5 < serverIDBase
	c.Register(p)

	c.Destroy(p)
	if c.Lookup(5) == nil {
		t.Fatal("destroyed client id should still be in objects (zombie) until delete_id")
	}

	c.release(5)
	if c.Lookup(5) != nil {
		t.Fatal("after release, the id should have disappeared from objects")
	}
	if len(c.freeIDs) != 1 || c.freeIDs[0] != 5 {
		t.Fatalf("freeIDs = %v, want [5]", c.freeIDs)
	}
}

func TestDestroyServerIDRemovedImmediately(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(serverIDBase+1, 1, c)}
	c.Register(p)

	c.Destroy(p)
	if c.Lookup(serverIDBase+1) != nil {
		t.Fatal("server id should disappear from objects immediately")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want empty (server ids are not recycled)", c.freeIDs)
	}
}

// The id arrives in a delete_id from the compositor: it has to be treated as hostile.
func TestReleaseIgnoresDisplayID(t *testing.T) {
	c := newConn(nil)
	c.display = newDisplay(displayID, 1, c)
	c.Register(c.display)

	c.release(displayID)

	if c.Lookup(displayID) != Proxy(c.display) {
		t.Fatal("delete_id(1) should not unregister wl_display")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want empty (id 1 is not recycled)", c.freeIDs)
	}
}

func TestReleaseIgnoresServerIDs(t *testing.T) {
	c := newConn(nil)
	id := uint32(serverIDBase + 1)
	c.Register(&fakeProxy{ProxyBase: NewProxyBase(id, 1, c)})

	c.release(id)

	if c.Lookup(id) == nil {
		t.Fatal("delete_id for a server id should not touch objects")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want empty (server ids don't belong to the client)", c.freeIDs)
	}
}

func TestReleaseIgnoresRepeatedDeleteID(t *testing.T) {
	c := newConn(nil)
	c.Register(&fakeProxy{ProxyBase: NewProxyBase(5, 1, c)})

	c.release(5)
	c.release(5) // duplicate: the id is no longer alive
	c.release(9) // never registered

	if len(c.freeIDs) != 1 || c.freeIDs[0] != 5 {
		t.Fatalf("freeIDs = %v, want [5] (no duplicates or made-up ids)", c.freeIDs)
	}
	if got := c.NewID(); got != 5 {
		t.Fatalf("NewID() = %d, want 5", got)
	}
	if got := c.NewID(); got != 2 {
		t.Fatalf("second NewID() = %d, want 2 (freeIDs already empty)", got)
	}
}

func TestDestroyClearsListener(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	c.Destroy(p)
	if !p.listenerCleared {
		t.Fatal("destroy() should call clearListener()")
	}
}
