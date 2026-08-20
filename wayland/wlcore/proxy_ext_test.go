// Package wlcore_test verifies from OUTSIDE wlcore what no internal test
// can verify: that an extension package (xdgshell, wlrlayershell...) can
// satisfy wlcore.Proxy. clearListener() isn't exported, so the only thing
// that makes it possible is that *ProxyBase implements it and it comes
// promoted through embedding.
package wlcore_test

import (
	"testing"

	"github.com/romycode/ggui/wayland/wlcore"
)

type extListener struct {
	Ping func(uint32)
}

// extProxy is the skeleton of what the generator would emit for an
// interface in another package: it embeds ProxyBase, has its own
// listener, and its constructor hooks up OnClear.
type extProxy struct {
	wlcore.ProxyBase
	listener extListener
}

var _ wlcore.Proxy = (*extProxy)(nil)

func newExtProxy(b wlcore.ProxyBase) *extProxy {
	p := &extProxy{ProxyBase: b}
	p.OnClear = func() { p.listener = extListener{} }
	return p
}

func (p *extProxy) SetListener(l extListener) { p.listener = l }

func (p *extProxy) Dispatch(opcode uint16, d *wlcore.Decoder) error { return nil }

func TestExtensionPackageCanImplementProxy(t *testing.T) {
	p := newExtProxy(wlcore.NewProxyBase(7, 2, nil))
	if p.ID() != 7 || p.Version() != 2 {
		t.Fatalf("ID()=%d Version()=%d, want 7 2", p.ID(), p.Version())
	}

	called := false
	p.SetListener(extListener{Ping: func(uint32) { called = true }})
	p.OnClear()
	if p.listener.Ping != nil {
		t.Fatal("OnClear should leave the listener at its zero value")
	}
	if called {
		t.Fatal("nobody should have called the listener")
	}

	// And it works as an Interface[T] for Bind, which requires T Proxy.
	iface := wlcore.Interface[*extProxy]{Name: "ext_thing", MaxVersion: 3, New: newExtProxy}
	if iface.New(wlcore.NewProxyBase(9, 1, nil)).ID() != 9 {
		t.Fatal("Interface's factory should construct the proxy")
	}
}
