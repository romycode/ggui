package xmlmodel

import (
	"os"
	"path/filepath"
	"testing"
)

const fixtureXML = `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="wayland">
  <interface name="wl_fake_thing" version="3">
    <request name="do_stuff" since="2">
      <arg name="target" type="object" interface="wl_fake_thing" allow-null="true" summary="x"/>
      <arg name="label" type="string" allow-null="true" summary="x"/>
      <arg name="flags" type="uint" enum="mode" summary="x"/>
    </request>
    <request name="destroy" type="destructor">
    </request>
    <event name="happened">
      <arg name="data" type="uint" summary="x"/>
    </event>
    <enum name="mode" bitfield="true">
      <entry name="fast" value="1" summary="x"/>
      <entry name="slow" value="2" summary="x"/>
    </enum>
  </interface>
</protocol>
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wayland.xml"), []byte(fixtureXML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseAllReadsWaylandXML(t *testing.T) {
	dir := writeFixture(t)
	protos, err := ParseAll(dir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(protos) != 1 {
		t.Fatalf("len(protos) = %d, want 1", len(protos))
	}
	p := protos[0]
	if p.File != "wayland.xml" {
		t.Errorf("File = %q, want %q", p.File, "wayland.xml")
	}
	if len(p.Interfaces) != 1 {
		t.Fatalf("len(Interfaces) = %d, want 1", len(p.Interfaces))
	}
	iface := p.Interfaces[0]
	if iface.Name != "wl_fake_thing" || iface.Version != 3 {
		t.Errorf("iface = %+v", iface)
	}
	if iface.Line != 3 {
		t.Errorf("Line = %d, want 3 (donde empieza <interface name=)", iface.Line)
	}
}

func TestParseAllSinceDefaultsToOne(t *testing.T) {
	dir := writeFixture(t)
	protos, _ := ParseAll(dir)
	iface := protos[0].Interfaces[0]
	if len(iface.Requests) != 2 {
		t.Fatalf("len(Requests) = %d, want 2", len(iface.Requests))
	}
	if iface.Requests[0].Since != 2 {
		t.Errorf("do_stuff.Since = %d, want 2 (explícito en el XML)", iface.Requests[0].Since)
	}
	if iface.Requests[1].Since != 1 {
		t.Errorf("destroy.Since = %d, want 1 (default, no estaba en el XML)", iface.Requests[1].Since)
	}
	if iface.Requests[1].Type != "destructor" {
		t.Errorf("destroy.Type = %q, want %q", iface.Requests[1].Type, "destructor")
	}
	if len(iface.Events) != 1 || iface.Events[0].Since != 1 {
		t.Errorf("Events = %+v, want one event with Since=1", iface.Events)
	}
}

func TestParseAllParsesArgsAndEnums(t *testing.T) {
	dir := writeFixture(t)
	protos, _ := ParseAll(dir)
	req := protos[0].Interfaces[0].Requests[0]
	if len(req.Args) != 3 {
		t.Fatalf("len(Args) = %d, want 3", len(req.Args))
	}
	target, label, flags := req.Args[0], req.Args[1], req.Args[2]
	if target.Name != "target" || target.Type != "object" || target.Interface != "wl_fake_thing" || !target.AllowNull {
		t.Errorf("target arg = %+v", target)
	}
	if label.Name != "label" || label.Type != "string" || !label.AllowNull {
		t.Errorf("label arg = %+v", label)
	}
	if flags.Name != "flags" || flags.Type != "uint" || flags.Enum != "mode" {
		t.Errorf("flags arg = %+v", flags)
	}

	enums := protos[0].Interfaces[0].Enums
	if len(enums) != 1 || enums[0].Name != "mode" || !enums[0].Bitfield {
		t.Fatalf("Enums = %+v", enums)
	}
	if len(enums[0].Entries) != 2 || enums[0].Entries[0].Name != "fast" || enums[0].Entries[0].Value != "1" {
		t.Errorf("Entries = %+v", enums[0].Entries)
	}
}

func TestParseAllMissingFileErrors(t *testing.T) {
	dir := t.TempDir() // vacío, sin wayland.xml
	if _, err := ParseAll(dir); err == nil {
		t.Fatal("ParseAll en un directorio sin wayland.xml debería fallar")
	}
}
