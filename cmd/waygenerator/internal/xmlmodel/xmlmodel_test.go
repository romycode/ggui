package xmlmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureXML = `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="wayland">
  <interface name="wl_fake_thing" version="3">
    <description summary="a fake thing">
      Body text for the fake interface.
    </description>
    <request name="do_stuff" since="2">
      <description summary="does stuff">
        Body text for do_stuff.
      </description>
      <arg name="target" type="object" interface="wl_fake_thing" allow-null="true" summary="the target"/>
      <arg name="label" type="string" allow-null="true" summary="a label"/>
      <arg name="flags" type="uint" enum="mode" summary="mode flags"/>
    </request>
    <request name="destroy" type="destructor">
    </request>
    <event name="happened">
      <description summary="something happened">
        Body text for happened.
      </description>
      <arg name="data" type="uint" summary="the data"/>
    </event>
    <enum name="mode" bitfield="true">
      <description summary="the mode"/>
      <entry name="fast" value="1" summary="go fast"/>
      <entry name="slow" value="2" summary="go slow"/>
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
	// The rest of the manifest also has to exist -- ParseAll reads every
	// listed file, not just wayland.xml -- but these tests only exercise
	// wayland.xml's content, so an empty protocol (zero interfaces) for
	// each of the rest is enough.
	for _, name := range manifest {
		if name == "wayland.xml" {
			continue
		}
		empty := `<?xml version="1.0" encoding="UTF-8"?><protocol name="stub"></protocol>`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(empty), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParseAllReadsWaylandXML(t *testing.T) {
	dir := writeFixture(t)
	protos, err := ParseAll(dir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(protos) != len(manifest) {
		t.Fatalf("len(protos) = %d, want %d (one per manifest file)", len(protos), len(manifest))
	}
	p := protos[0] // wayland.xml is first in the manifest
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
		t.Errorf("Line = %d, want 3 (where <interface name= starts)", iface.Line)
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
		t.Errorf("do_stuff.Since = %d, want 2 (explicit in the XML)", iface.Requests[0].Since)
	}
	if iface.Requests[1].Since != 1 {
		t.Errorf("destroy.Since = %d, want 1 (default, not in the XML)", iface.Requests[1].Since)
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

func TestParseAllParsesDescriptions(t *testing.T) {
	dir := writeFixture(t)
	protos, _ := ParseAll(dir)
	iface := protos[0].Interfaces[0]

	if iface.Description.Summary != "a fake thing" {
		t.Errorf("iface.Description.Summary = %q, want %q", iface.Description.Summary, "a fake thing")
	}
	if got := strings.TrimSpace(iface.Description.Body); got != "Body text for the fake interface." {
		t.Errorf("iface.Description.Body = %q, want %q", got, "Body text for the fake interface.")
	}

	req := iface.Requests[0]
	if req.Description.Summary != "does stuff" {
		t.Errorf("req.Description.Summary = %q, want %q", req.Description.Summary, "does stuff")
	}
	if got := strings.TrimSpace(req.Description.Body); got != "Body text for do_stuff." {
		t.Errorf("req.Description.Body = %q, want %q", got, "Body text for do_stuff.")
	}
	if req.Args[0].Summary != "the target" {
		t.Errorf("req.Args[0].Summary = %q, want %q", req.Args[0].Summary, "the target")
	}

	// destroy doesn't carry <description>: zero values, not a parse failure.
	destroy := iface.Requests[1]
	if destroy.Description.Summary != "" || destroy.Description.Body != "" {
		t.Errorf("destroy.Description = %+v, want zero value (no <description> in the XML)", destroy.Description)
	}

	ev := iface.Events[0]
	if ev.Description.Summary != "something happened" {
		t.Errorf("ev.Description.Summary = %q, want %q", ev.Description.Summary, "something happened")
	}
	if ev.Args[0].Summary != "the data" {
		t.Errorf("ev.Args[0].Summary = %q, want %q", ev.Args[0].Summary, "the data")
	}

	en := iface.Enums[0]
	if en.Description.Summary != "the mode" {
		t.Errorf("en.Description.Summary = %q, want %q", en.Description.Summary, "the mode")
	}
	if en.Entries[0].Summary != "go fast" {
		t.Errorf("en.Entries[0].Summary = %q, want %q", en.Entries[0].Summary, "go fast")
	}
	if en.Entries[1].Summary != "go slow" {
		t.Errorf("en.Entries[1].Summary = %q, want %q", en.Entries[1].Summary, "go slow")
	}
}

func TestParseAllNormalizesMultilineSummary(t *testing.T) {
	// Regression: real xdg-shell.xml has a summary= wrapped across two
	// lines for readability. encoding/xml doesn't normalize attribute
	// whitespace (it doesn't implement that part of the XML spec), so
	// without normalizeSummaries the "\n" and the following indentation
	// stay literal inside the string -- harmless until codegen dumps it
	// into a single-line Go comment and splits it in two.
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="wayland">
  <interface name="wl_fake" version="1">
    <enum name="mode">
      <entry name="bad" value="0" summary="provided value is
        not a valid variant of the mode enum"/>
    </enum>
  </interface>
</protocol>
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wayland.xml"), []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest {
		if name == "wayland.xml" {
			continue
		}
		empty := `<?xml version="1.0" encoding="UTF-8"?><protocol name="stub"></protocol>`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(empty), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	protos, err := ParseAll(dir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	got := protos[0].Interfaces[0].Enums[0].Entries[0].Summary
	want := "provided value is not a valid variant of the mode enum"
	if got != want {
		t.Errorf("Summary = %q, want %q (whitespace collapsed to single spaces)", got, want)
	}
}

func TestParseAllMissingFileErrors(t *testing.T) {
	dir := t.TempDir() // empty, no wayland.xml
	if _, err := ParseAll(dir); err == nil {
		t.Fatal("ParseAll on a directory without wayland.xml should fail")
	}
}
