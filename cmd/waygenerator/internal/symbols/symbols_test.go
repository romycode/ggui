package symbols

import (
	"testing"

	"github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel"
)

func fakeProtocol() []xmlmodel.Protocol {
	return []xmlmodel.Protocol{
		{
			Name: "wayland",
			File: "wayland.xml",
			Interfaces: []xmlmodel.Interface{
				{
					Name:    "wl_compositor",
					Version: 6,
					Requests: []xmlmodel.Request{
						{Name: "create_surface", Since: 1},
						{Name: "create_region", Since: 1},
					},
				},
				{
					Name:    "wl_fake",
					Version: 2,
					// mismo nombre "sync" en request y en event: deben
					// numerarse en tablas separadas, sin pisarse.
					Requests: []xmlmodel.Request{
						{Name: "sync", Since: 1},
						{Name: "other", Since: 1},
					},
					Events: []xmlmodel.Event{
						{Name: "sync", Since: 1},
					},
					Enums: []xmlmodel.Enum{
						{Name: "mode", Bitfield: true, Entries: []xmlmodel.Entry{
							{Name: "fast", Value: "1"},
						}},
					},
				},
			},
		},
	}
}

func TestBuildOpcodesByIndex(t *testing.T) {
	table := Build(fakeProtocol())
	e, ok := table["wl_compositor"]
	if !ok {
		t.Fatal("wl_compositor no está en la tabla")
	}
	if e.GoPackage != "wlcore" || e.GoType != "Compositor" || e.MaxVersion != 6 {
		t.Errorf("entry = %+v", e)
	}
	if e.ReqOpcodes["create_surface"] != 0 || e.ReqOpcodes["create_region"] != 1 {
		t.Errorf("ReqOpcodes = %v", e.ReqOpcodes)
	}
}

func TestBuildRequestAndEventOpcodesDontCollide(t *testing.T) {
	table := Build(fakeProtocol())
	e := table["wl_fake"]
	if e.ReqOpcodes["sync"] != 0 {
		t.Errorf("ReqOpcodes[sync] = %d, want 0", e.ReqOpcodes["sync"])
	}
	if e.EvtOpcodes["sync"] != 0 {
		t.Errorf("EvtOpcodes[sync] = %d, want 0 (numerado aparte, no debe pisar ReqOpcodes)", e.EvtOpcodes["sync"])
	}
	if e.ReqOpcodes["other"] != 1 {
		t.Errorf("ReqOpcodes[other] = %d, want 1", e.ReqOpcodes["other"])
	}
}

func TestBuildEnumNaming(t *testing.T) {
	table := Build(fakeProtocol())
	e := table["wl_fake"]
	info, ok := e.Enums["mode"]
	if !ok {
		t.Fatal("enum mode no está en la tabla")
	}
	if info.GoName != "FakeMode" || !info.Bitfield {
		t.Errorf("EnumInfo = %+v, want {FakeMode true}", info)
	}
}
