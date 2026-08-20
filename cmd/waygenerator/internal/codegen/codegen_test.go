package codegen

import (
	"os"
	"testing"

	"github.com/romycode/ggui/cmd/waygenerator/internal/resolve"
)

func skeletonEnumFixture() resolve.ResolvedInterface {
	return resolve.ResolvedInterface{
		XMLName:        "wl_fake_thing",
		GoPackage:      "wlcore",
		GoType:         "FakeThing",
		Recv:           "f",
		MaxVersion:     1,
		HasEvents:      false,
		PublicListener: true,
		Enums: []resolve.ResolvedEnum{
			{
				GoName:   "FakeThingMode",
				Bitfield: true,
				Entries: []resolve.ResolvedEnumEntry{
					{GoName: "FakeThingModeFast", Value: "1"},
					{GoName: "FakeThingModeSlow", Value: "2"},
				},
			},
			{
				GoName:   "FakeThingKind",
				Bitfield: false,
				Entries: []resolve.ResolvedEnumEntry{
					{GoName: "FakeThingKindA", Value: "0"},
				},
			},
		},
	}
}

func TestRenderInterfaceSkeletonAndEnums(t *testing.T) {
	got, err := RenderInterface(skeletonEnumFixture())
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/skeleton_enum.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderInterfaceFactoryParameterDoesNotCollideWithReceiver(t *testing.T) {
	iface := skeletonEnumFixture()
	iface.XMLName = "wl_buffer"
	iface.GoType = "Buffer"
	iface.Recv = "b"
	iface.Enums = nil

	got, err := RenderInterface(iface)
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/factory_receiver.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func requestsFixture() resolve.ResolvedInterface {
	newID := func(objGoType string) *resolve.GoType {
		t := resolve.GoType{Kind: resolve.KindNewIDStatic, ObjGoType: objGoType, TypeString: "*" + objGoType}
		return &t
	}
	return resolve.ResolvedInterface{
		XMLName:        "wl_widget",
		GoPackage:      "wlcore",
		GoType:         "Widget",
		Recv:           "w",
		MaxVersion:     1,
		PublicListener: true,
		Requests: []resolve.ResolvedRequest{
			{
				XMLName: "set_size", GoName: "SetSize", Since: 1,
				Args: []resolve.ResolvedArg{
					{XMLName: "width", GoName: "width", Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "int32"}},
					{XMLName: "height", GoName: "height", Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "int32"}},
				},
			},
			{
				XMLName: "create_pool", GoName: "CreatePool", Since: 1,
				Args: []resolve.ResolvedArg{
					{XMLName: "fd", GoName: "fd", IsFD: true, Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "int"}},
					{XMLName: "size", GoName: "size", Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "int32"}},
				},
				Returns: newID("WidgetPool"),
			},
			{
				XMLName: "create_child", GoName: "CreateChild", Since: 1,
				Returns: newID("WidgetChild"),
			},
			{XMLName: "destroy", GoName: "Destroy", Since: 1, Destructor: true},
			{XMLName: "bind", GoName: "Bind", Since: 1, BindLike: true,
				Args: []resolve.ResolvedArg{}},
		},
	}
}

func TestRenderInterfaceRequests(t *testing.T) {
	got, err := RenderInterface(requestsFixture())
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/requests.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func eventsFixture() resolve.ResolvedInterface {
	return resolve.ResolvedInterface{
		XMLName:        "wl_gizmo",
		GoPackage:      "wlcore",
		GoType:         "Gizmo",
		Recv:           "g",
		MaxVersion:     1,
		HasEvents:      true,
		PublicListener: true,
		Events: []resolve.ResolvedEvent{
			{
				XMLName: "ping", GoName: "Ping", Since: 1,
				Args: []resolve.ResolvedArg{
					{XMLName: "value", GoName: "value", Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "uint32"}},
				},
			},
			{
				XMLName: "keymap", GoName: "Keymap", Since: 1, FDOwning: true,
				Args: []resolve.ResolvedArg{
					{XMLName: "format", GoName: "format", Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "uint32"}},
					{XMLName: "fd", GoName: "fd", IsFD: true, Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "int"}},
					{XMLName: "size", GoName: "size", Type: resolve.GoType{Kind: resolve.KindPrimitive, TypeString: "uint32"}},
				},
			},
			{
				XMLName: "widget", GoName: "Widget", Since: 1,
				Args: []resolve.ResolvedArg{
					{XMLName: "child", GoName: "child", Type: resolve.GoType{Kind: resolve.KindNewIDStatic, ObjGoType: "GizmoChild", TypeString: "*GizmoChild"}},
				},
			},
		},
	}
}

func TestRenderInterfaceEvents(t *testing.T) {
	got, err := RenderInterface(eventsFixture())
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/events.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func versioningFixture() resolve.ResolvedInterface {
	return resolve.ResolvedInterface{
		XMLName:        "wl_gadget",
		GoPackage:      "wlcore",
		GoType:         "Gadget",
		Recv:           "g",
		MaxVersion:     2,
		PublicListener: true,
		Requests: []resolve.ResolvedRequest{
			{
				XMLName: "set_parent", GoName: "SetParent", Since: 2,
				Args: []resolve.ResolvedArg{
					{XMLName: "parent", GoName: "parent", Type: resolve.GoType{Kind: resolve.KindObject, ObjGoType: "Gadget", TypeString: "*Gadget"}},
				},
			},
		},
	}
}

func TestRenderInterfaceVersionGuard(t *testing.T) {
	got, err := RenderInterface(versioningFixture())
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/versioning.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderInterfaceNullableObjectRequest(t *testing.T) {
	iface := resolve.ResolvedInterface{
		XMLName:        "wl_widget",
		GoPackage:      "wlcore",
		GoType:         "Widget",
		Recv:           "w",
		MaxVersion:     1,
		PublicListener: true,
		Requests: []resolve.ResolvedRequest{{
			XMLName: "set_parent",
			GoName:  "SetParent",
			Since:   1,
			Args: []resolve.ResolvedArg{{
				XMLName: "parent",
				GoName:  "parent",
				Type: resolve.GoType{
					Kind:       resolve.KindObject,
					TypeString: "*Widget",
					ObjGoType:  "Widget",
					AllowNull:  true,
				},
			}},
		}},
	}

	got, err := RenderInterface(iface)
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/nullable_object.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderInterfaceVersionedDestructorGuard(t *testing.T) {
	iface := resolve.ResolvedInterface{
		XMLName:        "wl_gadget",
		GoPackage:      "wlcore",
		GoType:         "Gadget",
		Recv:           "g",
		MaxVersion:     3,
		PublicListener: true,
		Requests: []resolve.ResolvedRequest{{
			XMLName:    "release",
			GoName:     "Release",
			Since:      3,
			Destructor: true,
		}},
	}

	got, err := RenderInterface(iface)
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	want, err := os.ReadFile("testdata/destructor_versioning.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderInterface output difiere del golden file.\ngot:\n%s\nwant:\n%s", got, want)
	}
}
