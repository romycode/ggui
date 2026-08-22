package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleHeader = `
#define XKB_KEY_NoSymbol 0x000000 /* Special KeySym */
#define XKB_KEY_KP_Enter 0xff8d /*<U+000D CARRIAGE RETURN>*/
#define XKB_KEY_F1 0xffbe
`

func TestRunGeneratesFile(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "keysyms.h")
	out := filepath.Join(dir, "keysyms.gen.go")
	if err := os.WriteFile(in, []byte(sampleHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(in, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("var keysymNames = map[string]Keysym{")) {
		t.Fatalf("run(%q, %q) output missing keysym table", in, out)
	}
}

func TestRunMissingInputIncludesPath(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.h")
	err := run(missingPath, filepath.Join(t.TempDir(), "keysyms.gen.go"))
	if err == nil {
		t.Fatal("run with missing input succeeded")
	}
	if !strings.Contains(err.Error(), missingPath) {
		t.Errorf("run(%q, _) error = %v, want path included", missingPath, err)
	}
}

func TestCommittedOutputIsCurrent(t *testing.T) {
	repo := filepath.Join("..", "..")
	in := filepath.Join(repo, "third_party", "libxkbcommon", "xkbcommon-keysyms.h")
	wantPath := filepath.Join(repo, "keyboard", "keysyms.gen.go")
	gotPath := filepath.Join(t.TempDir(), "keysyms.gen.go")
	if err := run(in, gotPath); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("keyboard/keysyms.gen.go is stale; run go run ./cmd/keysymgen")
	}
}
