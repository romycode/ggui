package main

import (
	"testing"
)

func TestScanCountsWhatGoDocShows(t *testing.T) {
	pkgs, err := Scan("testdata/sample")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("Scan returned %d packages, want 1 (the nested module must be skipped)", len(pkgs))
	}
	got := pkgs[0]

	// Documented: the type, its Field (doc) and Bare (trailing comment),
	// Method, Generic, Exported, and both vars sharing the block comment.
	if got.Documented != 8 {
		t.Errorf("Documented = %d, want 8", got.Documented)
	}

	want := map[string]string{
		"Documented.Naked":        "field",
		"Undocumented":            "type",
		"Documented.Undocumented": "method",
		"Generic.Bare":            "method",
		"Bare":                    "func",
		"Unmentioned":             "value",
	}
	if len(got.Missing) != len(want) {
		t.Errorf("Missing has %d entries, want %d: %+v", len(got.Missing), len(want), got.Missing)
	}
	for _, s := range got.Missing {
		kind, ok := want[s.Name]
		if !ok {
			t.Errorf("Missing lists %s %q, which should not be there", s.Kind, s.Name)
			continue
		}
		if s.Kind != kind {
			t.Errorf("%s reported as %s, want %s", s.Name, s.Kind, kind)
		}
		if s.Line == 0 {
			t.Errorf("%s has no line number", s.Name)
		}
		delete(want, s.Name)
	}
	for name, kind := range want {
		t.Errorf("%s %s was not reported missing", kind, name)
	}
}

// TestScanSkipsUnexportedReceivers is the case worth its own test: a
// documented method on an unexported type is invisible in go doc, so
// counting it as documented would inflate every package that has one.
func TestScanSkipsUnexportedReceivers(t *testing.T) {
	pkgs, err := Scan("testdata/sample")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, s := range pkgs[0].Missing {
		if s.Name == "unexported.Bare" {
			t.Errorf("Missing lists a method on an unexported type: %+v", s)
		}
	}
	// 8 documented + 6 missing = 14; the unexported type contributes two
	// methods and a func, none of which may show up on either side.
	if total := pkgs[0].Documented + len(pkgs[0].Missing); total != 14 {
		t.Errorf("exported surface = %d symbols, want 14", total)
	}
}

func TestPercentOfEmptyPackage(t *testing.T) {
	// Nothing exported is complete, not a hole: reporting 0% would make
	// every internal-only package look like the worst in the table.
	if got := (Package{}).Percent(); got != 100 {
		t.Errorf("Percent of an empty package = %d, want 100", got)
	}
}
