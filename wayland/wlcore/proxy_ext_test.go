// Package wlcore_test comprueba desde FUERA de wlcore lo que ningún test
// interno puede comprobar: que un paquete de extensión (xdgshell,
// wlrlayershell...) puede satisfacer wlcore.Proxy. clearListener() no está
// exportada, así que lo único que lo hace posible es que la implemente
// *ProxyBase y venga promocionada al embeberlo.
package wlcore_test

import (
	"testing"

	"github.com/romycode/ggui/wayland/wlcore"
)

type extListener struct {
	Ping func(uint32)
}

// extProxy es el esqueleto de lo que emitirá el generador para una interfaz
// de otro paquete: embebe ProxyBase, tiene su listener, y su constructor
// engancha OnClear.
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
		t.Fatal("OnClear debería dejar el listener a su cero")
	}
	if called {
		t.Fatal("nadie debería haber llamado al listener")
	}

	// Y sirve como Interface[T] para Bind, que exige T Proxy.
	iface := wlcore.Interface[*extProxy]{Name: "ext_thing", MaxVersion: 3, New: newExtProxy}
	if iface.New(wlcore.NewProxyBase(9, 1, nil)).ID() != 9 {
		t.Fatal("la factory de Interface debería construir el proxy")
	}
}
