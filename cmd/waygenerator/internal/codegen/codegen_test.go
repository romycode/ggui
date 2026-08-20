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
