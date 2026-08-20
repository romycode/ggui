package resolve

import (
	"strings"
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
		t.Fatalf("Args = %+v, want empty (the new_id becomes Returns, not an Arg)", req.Args)
	}
	if req.Returns == nil || req.Returns.ObjGoType != "Surface" || req.Returns.TypeString != "*Surface" {
		t.Errorf("Returns = %+v", req.Returns)
	}
	if req.BindLike {
		t.Error("BindLike should not be true for a new_id with interface=")
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
					{Name: "id", Type: "new_id"}, // no interface=
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
		t.Fatal("BindLike should be true for new_id without interface=")
	}
	if req.Returns != nil {
		t.Errorf("Returns = %+v, want nil (BindLike has no static return type)", req.Returns)
	}
	if len(req.Args) != 1 || req.Args[0].GoName != "name" {
		t.Errorf("Args = %+v, want just [name] (a dynamic new_id is not a normal Arg)", req.Args)
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
		t.Error("Destructor should be true")
	}
}

func TestResolveRejectsMissingObjectInterface(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Line:    17,
			Requests: []xmlmodel.Request{{
				Name:  "set_target",
				Since: 1,
				Args: []xmlmodel.Arg{{
					Name:      "target",
					Type:      "object",
					Interface: "wl_missing",
				}},
			}},
		}},
	}}

	_, _, err := build(protos)
	want := `resolve: wayland.xml:17: interface "wl_fake": request "set_target": arg "target": interface "wl_missing" not found`
	if err == nil || err.Error() != want {
		t.Fatalf("Resolve(missing object interface) error = %v, want %q", err, want)
	}
}

func TestResolveRejectsMissingNewIDInterface(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Line:    19,
			Requests: []xmlmodel.Request{{
				Name:  "make_thing",
				Since: 1,
				Args: []xmlmodel.Arg{{
					Name:      "id",
					Type:      "new_id",
					Interface: "wl_missing",
				}},
			}},
		}},
	}}

	_, _, err := build(protos)
	want := `resolve: wayland.xml:19: interface "wl_fake": request "make_thing": arg "id": interface "wl_missing" not found`
	if err == nil || err.Error() != want {
		t.Fatalf("Resolve(missing new_id interface) error = %v, want %q", err, want)
	}
}

func TestResolveRejectsMissingEnum(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Line:    23,
			Requests: []xmlmodel.Request{{
				Name:  "set_mode",
				Since: 1,
				Args:  []xmlmodel.Arg{{Name: "mode", Type: "uint", Enum: "missing"}},
			}},
		}},
	}}

	_, _, err := build(protos)
	want := `resolve: wayland.xml:23: interface "wl_fake": request "set_mode": arg "mode": enum "missing" not found`
	if err == nil || err.Error() != want {
		t.Fatalf("Resolve(missing enum) error = %v, want %q", err, want)
	}
}

func TestResolveRejectsUnknownEventArgType(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Line:    31,
			Events: []xmlmodel.Event{{
				Name:  "surprise",
				Since: 1,
				Args:  []xmlmodel.Arg{{Name: "payload", Type: "mystery"}},
			}},
		}},
	}}

	_, _, err := build(protos)
	want := `resolve: wayland.xml:31: interface "wl_fake": event "surprise": arg "payload": unknown XML type "mystery"`
	if err == nil || err.Error() != want {
		t.Fatalf("Resolve(unknown event arg type) error = %v, want %q", err, want)
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
		t.Error("FDOwning should be true (the event carries an fd arg)")
	}
	if !ev.Args[1].IsFD {
		t.Error("the fd arg should have IsFD=true")
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

func TestResolveIntEnumArg(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name: "wl_output", Version: 1,
			Enums: []xmlmodel.Enum{{
				Name:    "transform",
				Entries: []xmlmodel.Entry{{Name: "normal", Value: "0"}},
			}},
		}, {
			Name: "wl_surface", Version: 1,
			Requests: []xmlmodel.Request{{
				Name: "set_buffer_transform", Since: 1,
				Args: []xmlmodel.Arg{{Name: "transform", Type: "int", Enum: "wl_output.transform"}},
			}},
		}},
	}}

	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, iface := range m.Interfaces {
		if iface.XMLName != "wl_surface" {
			continue
		}
		arg := iface.Requests[0].Args[0]
		if arg.Type.Kind != KindEnum || arg.Type.TypeString != "OutputTransform" {
			t.Fatalf("int enum arg = %+v, want OutputTransform enum", arg)
		}
		return
	}
	t.Fatal("wl_surface missing from resolved model")
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
			t.Fatalf("order = %v, want %v", got, want)
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

func TestResolveThreadsDescriptions(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{{
			Name:    "wl_fake",
			Version: 1,
			Description: xmlmodel.Description{
				Summary: "a fake interface",
				Body:    "\n      Longer body for the interface.\n    ",
			},
			Requests: []xmlmodel.Request{{
				Name:  "do_it",
				Since: 1,
				Description: xmlmodel.Description{
					Summary: "does it",
					Body:    "\n        Longer body for the request.\n      ",
				},
				Args: []xmlmodel.Arg{
					{Name: "count", Type: "uint", Summary: "how many"},
				},
			}},
			Events: []xmlmodel.Event{{
				Name:  "happened",
				Since: 1,
				Description: xmlmodel.Description{
					Summary: "something happened",
					Body:    "\n        Longer body for the event.\n      ",
				},
				Args: []xmlmodel.Arg{
					{Name: "data", Type: "uint", Summary: "the payload"},
				},
			}},
			Enums: []xmlmodel.Enum{{
				Name: "mode",
				Description: xmlmodel.Description{
					Summary: "the mode",
				},
				Entries: []xmlmodel.Entry{
					{Name: "fast", Value: "1", Summary: "go fast"},
				},
			}},
		}},
	}}
	m, _, err := build(protos)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	iface := m.Interfaces[0]
	if iface.Summary != "a fake interface" {
		t.Errorf("iface.Summary = %q, want %q", iface.Summary, "a fake interface")
	}
	if !strings.Contains(iface.Doc, "Longer body for the interface.") {
		t.Errorf("iface.Doc = %q, want it to contain the body text", iface.Doc)
	}

	req := iface.Requests[0]
	if req.Summary != "does it" {
		t.Errorf("req.Summary = %q, want %q", req.Summary, "does it")
	}
	if !strings.Contains(req.Doc, "Longer body for the request.") {
		t.Errorf("req.Doc = %q, want it to contain the body text", req.Doc)
	}
	if req.Args[0].Summary != "how many" {
		t.Errorf("req.Args[0].Summary = %q, want %q", req.Args[0].Summary, "how many")
	}

	ev := iface.Events[0]
	if ev.Summary != "something happened" {
		t.Errorf("ev.Summary = %q, want %q", ev.Summary, "something happened")
	}
	if !strings.Contains(ev.Doc, "Longer body for the event.") {
		t.Errorf("ev.Doc = %q, want it to contain the body text", ev.Doc)
	}
	if ev.Args[0].Summary != "the payload" {
		t.Errorf("ev.Args[0].Summary = %q, want %q", ev.Args[0].Summary, "the payload")
	}

	en := iface.Enums[0]
	if en.Summary != "the mode" {
		t.Errorf("en.Summary = %q, want %q", en.Summary, "the mode")
	}
	if en.Entries[0].Summary != "go fast" {
		t.Errorf("en.Entries[0].Summary = %q, want %q", en.Entries[0].Summary, "go fast")
	}
}

func TestResolveRejectsPackageNameCollision(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_foo_bar", Version: 1, Line: 5},
			{Name: "wl_foo__bar", Version: 1, Line: 9}, // two underscores: PascalCase collapses the same to "FooBar"
		},
	}}
	_, _, err := build(protos)
	if err == nil {
		t.Fatal("Resolve should fail on a Go name collision (FooBar twice)")
	}
}

func TestResolveRejectsFDInNewIDReachableInterface(t *testing.T) {
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{
				Name: "wl_thing", Version: 1, Line: 3,
				Events: []xmlmodel.Event{{
					Name: "arrived", Since: 1,
					Args: []xmlmodel.Arg{{Name: "handle", Type: "fd"}},
				}},
			},
			{
				Name: "wl_owner", Version: 1, Line: 10,
				Events: []xmlmodel.Event{{
					Name: "got_thing", Since: 1,
					Args: []xmlmodel.Arg{{Name: "id", Type: "new_id", Interface: "wl_thing"}},
				}},
			},
		},
	}}
	_, _, err := build(protos)
	if err == nil {
		t.Fatal("Resolve should fail: wl_thing is reachable as new_id in an event and has an event with fd")
	}
}

func TestResolveAllowsCleanGraph(t *testing.T) {
	// Regression: a graph with no violations must not fail because of the
	// new checks.
	protos := []xmlmodel.Protocol{{
		File: "wayland.xml",
		Interfaces: []xmlmodel.Interface{
			{Name: "wl_display", Version: 1},
			{Name: "wl_compositor", Version: 1},
		},
	}}
	if _, _, err := build(protos); err != nil {
		t.Fatalf("Resolve: %v, want nil", err)
	}
}

func TestResolveAllowsSameGoTypeNameAcrossPackages(t *testing.T) {
	// Regression: wl_surface (wlcore) and xdg_surface (xdgshell) produce
	// the same GoType "Surface" once the prefix is stripped. checkRefPackage
	// can't disambiguate by looking up GoType alone across the whole table
	// -- it would confuse wl_compositor's legitimate reference to
	// wl_surface (same package) with a made-up reference to xdg_surface.
	protos := []xmlmodel.Protocol{
		{
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
		},
		{
			File:       "xdg-shell.xml",
			Interfaces: []xmlmodel.Interface{{Name: "xdg_surface", Version: 1}},
		},
	}
	if _, _, err := build(protos); err != nil {
		t.Fatalf("Resolve: %v, want nil (wl_compositor only references wl_surface, from wlcore itself)", err)
	}
}

func TestResolveRejectsRealCrossPackageReference(t *testing.T) {
	// wl_fake (wlcore) referencing xdg_thing (xdgshell) must indeed fail --
	// wlcore cannot depend on an extension. Unlike the previous test, here
	// the reference is real, not a name coincidence.
	protos := []xmlmodel.Protocol{
		{
			File: "wayland.xml",
			Interfaces: []xmlmodel.Interface{{
				Name: "wl_fake", Version: 1, Line: 5,
				Requests: []xmlmodel.Request{{
					Name:  "set_target",
					Since: 1,
					Args:  []xmlmodel.Arg{{Name: "target", Type: "object", Interface: "xdg_thing"}},
				}},
			}},
		},
		{
			File:       "xdg-shell.xml",
			Interfaces: []xmlmodel.Interface{{Name: "xdg_thing", Version: 1}},
		},
	}
	_, _, err := build(protos)
	if err == nil {
		t.Fatal("Resolve should fail: wl_fake (wlcore) references xdg_thing (xdgshell)")
	}
	if !strings.Contains(err.Error(), "xdgshell") {
		t.Errorf("error = %v, want mention of the xdgshell package", err)
	}
}

func TestResolveNoFDCheckDoesNotCrossPackagesOnGoTypeCollision(t *testing.T) {
	// Same bug as TestResolveAllowsSameGoTypeNameAcrossPackages, but in
	// checkNoFDInNewIDReachable: reachable[] was keyed by bare GoType.
	// xdg_surface is new_id-reachable in an xdgshell event; wl_surface
	// shares the GoType "Surface" but is its own wlcore interface, never
	// referenced as new_id -- carrying fd in its events is perfectly legal
	// and must not trigger the invariant.
	protos := []xmlmodel.Protocol{
		{
			File: "wayland.xml",
			Interfaces: []xmlmodel.Interface{{
				Name: "wl_surface", Version: 1, Line: 3,
				Events: []xmlmodel.Event{{
					Name:  "enter",
					Since: 1,
					Args:  []xmlmodel.Arg{{Name: "handle", Type: "fd"}},
				}},
			}},
		},
		{
			File: "xdg-shell.xml",
			Interfaces: []xmlmodel.Interface{
				{Name: "xdg_surface", Version: 1},
				{
					Name: "xdg_owner", Version: 1,
					Events: []xmlmodel.Event{{
						Name:  "got_surface",
						Since: 1,
						Args:  []xmlmodel.Arg{{Name: "id", Type: "new_id", Interface: "xdg_surface"}},
					}},
				},
			},
		},
	}
	if _, _, err := build(protos); err != nil {
		t.Fatalf("Resolve: %v, want nil (wl_surface is not new_id-reachable; it only shares a GoType with xdg_surface)", err)
	}
}

func TestResolveRejectsCrossPackageEventArg(t *testing.T) {
	// checkDAG only walked iface.Requests, never iface.Events -- vacuous in
	// Phase 1 (a wlcore.xml interface could never reference an extension
	// there, because there was no extension to reference), but now that
	// real packages coexist it's a real, unchecked invariant: a wlcore
	// event with an object/new_id arg pointing at an extension would slip
	// through.
	protos := []xmlmodel.Protocol{
		{
			File: "wayland.xml",
			Interfaces: []xmlmodel.Interface{{
				Name: "wl_fake", Version: 1, Line: 7,
				Events: []xmlmodel.Event{{
					Name:  "arrived",
					Since: 1,
					Args:  []xmlmodel.Arg{{Name: "thing", Type: "object", Interface: "xdg_thing"}},
				}},
			}},
		},
		{
			File:       "xdg-shell.xml",
			Interfaces: []xmlmodel.Interface{{Name: "xdg_thing", Version: 1}},
		},
	}
	_, _, err := build(protos)
	if err == nil {
		t.Fatal("Resolve should fail: wl_fake (wlcore) references xdg_thing (xdgshell) in an event")
	}
	if !strings.Contains(err.Error(), "xdgshell") {
		t.Errorf("error = %v, want mention of the xdgshell package", err)
	}
}
