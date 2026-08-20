package resolve

import (
	"testing"

	"github.com/romycode/ggui/cmd/waygenerator/internal/symbols"
	"github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel"
)

func build(protos []xmlmodel.Protocol) (Model, symbols.Table, error) {
	table := symbols.Build(protos)
	m, err := Resolve(protos, table)
	return m, table, err
}

func TestResolvePrimitivesAndFixed(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Requests: []xmlmodel.Request{{
				Name:  "do_it",
				Since: 1,
				Args: []xmlmodel.Arg{
					{Name: "count", Type: "uint"},
					{Name: "delta", Type: "int"},
					{Name: "scale", Type: "fixed"},
					{Name: "label", Type: "string"},
					{Name: "note", Type: "string", AllowNull: true},
					{Name: "blob", Type: "array"},
				},
			}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	req := m.Interfaces[0].Requests[0]
	if req.GoName != "DoIt" {
		t.Errorf("GoName = %q, want DoIt", req.GoName)
	}
	want := []struct {
		goName, typeString string
	}{
		{"count", "uint32"},
		{"delta", "int32"},
		{"scale", "Fixed"},
		{"label", "string"},
		{"note", "*string"},
		{"blob", "[]byte"},
	}
	if len(req.Args) != len(want) {
		t.Fatalf("len(Args) = %d, want %d", len(req.Args), len(want))
	}
	for i, w := range want {
		if req.Args[i].GoName != w.goName || req.Args[i].Type.TypeString != w.typeString {
			t.Errorf("Args[%d] = %+v, want {%s %s}", i, req.Args[i], w.goName, w.typeString)
		}
	}
}

func TestResolveFDArgSkipsEncoderButKeepsParam(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Requests: []xmlmodel.Request{{
				Name: "send_fd", Since: 1,
				Args: []xmlmodel.Arg{{Name: "fd", Type: "fd"}},
			}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	arg := m.Interfaces[0].Requests[0].Args[0]
	if !arg.IsFD || arg.Type.TypeString != "int" {
		t.Errorf("fd arg = %+v, want IsFD=true TypeString=int", arg)
	}
}

func TestResolveNewIDStaticBecomesReturn(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_surface", Version: 1},
			{
				Name: "wl_compositor", Version: 1,
				Requests: []xmlmodel.Request{{
					Name: "create_surface", Since: 1,
					Args: []xmlmodel.Arg{{Name: "id", Type: "new_id", Interface: "wl_surface"}},
				}},
			},
		},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var comp ResolvedInterface
	for _, iface := range m.Interfaces {
		if iface.XMLName == "wl_compositor" {
			comp = iface
		}
	}
	req := comp.Requests[0]
	if len(req.Args) != 0 {
		t.Fatalf("Args = %+v, want vacío (el new_id se convierte en Returns, no en Arg)", req.Args)
	}
	if req.Returns == nil || req.Returns.ObjGoType != "Surface" || req.Returns.TypeString != "*Surface" {
		t.Errorf("Returns = %+v", req.Returns)
	}
	if req.BindLike {
		t.Error("BindLike no debería ser true para un new_id con interface=")
	}
}

func TestResolveBindLikeNewIDWithoutInterface(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name: "wl_registry", Version: 1,
			Requests: []xmlmodel.Request{{
				Name: "bind", Since: 1,
				Args: []xmlmodel.Arg{
					{Name: "name", Type: "uint"},
					{Name: "id", Type: "new_id"}, // sin interface=
				},
			}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	req := m.Interfaces[0].Requests[0]
	if !req.BindLike {
		t.Fatal("BindLike debería ser true para new_id sin interface=")
	}
	if req.Returns != nil {
		t.Errorf("Returns = %+v, want nil (BindLike no tiene tipo de retorno estático)", req.Returns)
	}
	if len(req.Args) != 1 || req.Args[0].GoName != "name" {
		t.Errorf("Args = %+v, want solo [name] (el new_id dinámico no es un Arg normal)", req.Args)
	}
}

func TestResolveDestructorRequest(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name: "wl_fake", Version: 1,
			Requests: []xmlmodel.Request{{Name: "destroy", Type: "destructor", Since: 1}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !m.Interfaces[0].Requests[0].Destructor {
		t.Error("Destructor debería ser true")
	}
}

func TestResolveEventFDOwning(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name: "wl_fake", Version: 1,
			Events: []xmlmodel.Event{{
				Name: "keymap", Since: 1,
				Args: []xmlmodel.Arg{
					{Name: "format", Type: "uint"},
					{Name: "fd", Type: "fd"},
					{Name: "size", Type: "uint"},
				},
			}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ev := m.Interfaces[0].Events[0]
	if !ev.FDOwning {
		t.Error("FDOwning debería ser true (el evento lleva un arg fd)")
	}
	if !ev.Args[1].IsFD {
		t.Error("el arg fd debería tener IsFD=true")
	}
}

func TestResolveNewIDInEventIsTypedArg(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_data_offer", Version: 1},
			{
				Name: "wl_data_device", Version: 1,
				Events: []xmlmodel.Event{{
					Name: "data_offer", Since: 1,
					Args: []xmlmodel.Arg{{Name: "id", Type: "new_id", Interface: "wl_data_offer"}},
				}},
			},
		},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var dev ResolvedInterface
	for _, iface := range m.Interfaces {
		if iface.XMLName == "wl_data_device" {
			dev = iface
		}
	}
	arg := dev.Events[0].Args[0]
	if arg.Type.Kind != KindNewIDStatic || arg.Type.ObjGoType != "DataOffer" {
		t.Errorf("arg = %+v", arg)
	}
}

func TestResolveObjectAndEnumArgs(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_output", Version: 1},
			{
				Name: "wl_fake", Version: 1,
				Enums: []xmlmodel.Enum{{Name: "kind", Entries: []xmlmodel.Entry{{Name: "a", Value: "0"}}}},
				Requests: []xmlmodel.Request{{
					Name: "set_stuff", Since: 1,
					Args: []xmlmodel.Arg{
						{Name: "output", Type: "object", Interface: "wl_output", AllowNull: true},
						{Name: "mode", Type: "uint", Enum: "kind"},
					},
				}},
			},
		},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var fake ResolvedInterface
	for _, iface := range m.Interfaces {
		if iface.XMLName == "wl_fake" {
			fake = iface
		}
	}
	args := fake.Requests[0].Args
	if args[0].Type.Kind != KindObject || args[0].Type.ObjGoType != "Output" || !args[0].Type.AllowNull {
		t.Errorf("output arg = %+v", args[0])
	}
	if args[1].Type.Kind != KindEnum || args[1].Type.ObjGoType != "FakeKind" {
		t.Errorf("mode arg = %+v", args[1])
	}
}

func TestResolveHasEventsAndPublicListener(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_display", Version: 1, Events: []xmlmodel.Event{{Name: "delete_id", Since: 1, Args: []xmlmodel.Arg{{Name: "id", Type: "uint"}}}}},
			{Name: "wl_compositor", Version: 1},
		},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var display, comp ResolvedInterface
	for _, iface := range m.Interfaces {
		switch iface.XMLName {
		case "wl_display":
			display = iface
		case "wl_compositor":
			comp = iface
		}
	}
	if !display.HasEvents || display.PublicListener {
		t.Errorf("wl_display: HasEvents=%v PublicListener=%v, want true false", display.HasEvents, display.PublicListener)
	}
	if comp.HasEvents || !comp.PublicListener {
		t.Errorf("wl_compositor: HasEvents=%v PublicListener=%v, want false true", comp.HasEvents, comp.PublicListener)
	}
	if display.Recv != "d" || comp.Recv != "c" {
		t.Errorf("Recv: display=%q compositor=%q, want d c", display.Recv, comp.Recv)
	}
}

func TestResolveModelSortedByName(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_surface", Version: 1},
			{Name: "wl_compositor", Version: 1},
			{Name: "wl_buffer", Version: 1},
		},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := []string{m.Interfaces[0].XMLName, m.Interfaces[1].XMLName, m.Interfaces[2].XMLName}
	want := []string{"wl_buffer", "wl_compositor", "wl_surface"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orden = %v, want %v", got, want)
		}
	}
}

func TestResolveEnumEntries(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name: "wl_seat", Version: 1,
			Enums: []xmlmodel.Enum{{
				Name: "capability", Bitfield: true,
				Entries: []xmlmodel.Entry{
					{Name: "pointer", Value: "1"},
					{Name: "keyboard", Value: "2"},
				},
			}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	en := m.Interfaces[0].Enums[0]
	if en.GoName != "SeatCapability" || !en.Bitfield {
		t.Fatalf("enum = %+v", en)
	}
	if len(en.Entries) != 2 || en.Entries[0].GoName != "SeatCapabilityPointer" || en.Entries[0].Value != "1" {
		t.Errorf("entries = %+v", en.Entries)
	}
}
