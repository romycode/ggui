package main

import (
	"os"
	"path/filepath"
	"testing"
)

const tinyProtocol = `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="wayland">
  <interface name="wl_tiny" version="1">
    <request name="destroy" type="destructor">
    </request>
  </interface>
</protocol>
`

// otherManifestFiles are the xmlmodel manifest files besides wayland.xml --
// ParseAll requires all of them to exist, so a run() test focused on
// wayland.xml needs empty stubs for the rest.
var otherManifestFiles = []string{"xdg-shell.xml", "viewporter.xml", "fractional-scale-v1.xml", "tablet-v2.xml", "cursor-shape-v1.xml"}

func writeStubProtocols(t *testing.T, dir string) {
	t.Helper()
	const empty = `<?xml version="1.0" encoding="UTF-8"?><protocol name="stub"></protocol>`
	for _, name := range otherManifestFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(empty), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunGeneratesAFile(t *testing.T) {
	protocolsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(protocolsDir, "wayland.xml"), []byte(tinyProtocol), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStubProtocols(t, protocolsDir)
	outDir := t.TempDir()

	if err := run(protocolsDir, outDir); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "tiny.gen.go"))
	if err != nil {
		t.Fatalf("tiny.gen.go was not generated: %v", err)
	}
	if len(data) == 0 {
		t.Error("tiny.gen.go is empty")
	}
}

func TestRunPropagatesParseErrors(t *testing.T) {
	protocolsDir := t.TempDir() // no wayland.xml
	outDir := t.TempDir()
	if err := run(protocolsDir, outDir); err == nil {
		t.Fatal("run should fail if protocols/wayland.xml does not exist")
	}
}
