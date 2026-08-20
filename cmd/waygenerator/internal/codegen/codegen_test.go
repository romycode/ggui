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
