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

func TestRunGeneratesAFile(t *testing.T) {
	protocolsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(protocolsDir, "wayland.xml"), []byte(tinyProtocol), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()

	if err := run(protocolsDir, outDir); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "tiny.gen.go"))
	if err != nil {
		t.Fatalf("no se generó tiny.gen.go: %v", err)
	}
	if len(data) == 0 {
		t.Error("tiny.gen.go está vacío")
	}
}

func TestRunPropagatesParseErrors(t *testing.T) {
	protocolsDir := t.TempDir() // sin wayland.xml
	outDir := t.TempDir()
	if err := run(protocolsDir, outDir); err == nil {
		t.Fatal("run debería fallar si protocols/wayland.xml no existe")
	}
}
