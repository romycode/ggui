package text

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

// The Go fonts are the fixtures: they ship in x/image, so the tests never
// depend on what the machine running them has installed. Their family is
// "Go" (or "Go Mono") and their subfamily is the style.

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func familyOf(t *testing.T, f *sfnt.Font) string {
	t.Helper()
	var b sfnt.Buffer
	name, err := f.Name(&b, sfnt.NameIDFamily)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	return name
}

func subfamilyOf(t *testing.T, f *sfnt.Font) string {
	t.Helper()
	var b sfnt.Buffer
	name, err := f.Name(&b, sfnt.NameIDSubfamily)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	return name
}

func TestFindMatchesFamilyAndStyleByTheNamesInsideTheFile(t *testing.T) {
	dir := t.TempDir()
	// File names deliberately lie: matching has to read the name table.
	writeFile(t, filepath.Join(dir, "a.ttf"), gobold.TTF)
	writeFile(t, filepath.Join(dir, "b.ttf"), goregular.TTF)
	writeFile(t, filepath.Join(dir, "c.ttf"), goitalic.TTF)

	for _, tc := range []struct {
		style Style
		want  string
	}{
		{Regular, "Regular"},
		{Bold, "Bold"},
		{Italic, "Italic"},
	} {
		f, err := findIn([]string{dir}, tc.style, []string{"Go"})
		if err != nil {
			t.Fatalf("findIn(%v): %v", tc.style, err)
		}
		if got := familyOf(t, f); got != "Go" {
			t.Errorf("%v: family = %q, want Go", tc.style, got)
		}
		if got := subfamilyOf(t, f); got != tc.want {
			t.Errorf("%v: subfamily = %q, want %q", tc.style, got, tc.want)
		}
	}
}

func TestFindIgnoresCaseAndRepeatedSpaces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "m.ttf"), gomono.TTF)

	if _, err := findIn([]string{dir}, Regular, []string{"  go   MONO "}); err != nil {
		t.Fatalf("findIn: %v", err)
	}
}

func TestFindReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "r.ttf"), goregular.TTF)

	if _, err := findIn([]string{dir}, Regular, []string{"Nonexistent"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown family: err = %v, want ErrNotFound", err)
	}
	// The family exists but not in this style.
	if _, err := findIn([]string{dir}, Bold, []string{"Go"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing style: err = %v, want ErrNotFound", err)
	}
	if _, err := findIn([]string{dir}, Regular, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("no families: err = %v, want ErrNotFound", err)
	}
	if _, err := findIn([]string{filepath.Join(dir, "missing")}, Regular, []string{"Go"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing dir: err = %v, want ErrNotFound", err)
	}
}

// An earlier family in the list wins wherever it sits on disk, because the
// caller ranked them.
func TestFindPrefersEarlierFamiliesOverEarlierFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "1-go.ttf"), goregular.TTF)
	writeFile(t, filepath.Join(dir, "2-mono.ttf"), gomono.TTF)

	c, err := search([]string{dir}, Regular, []string{"Go Mono", "Go"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.path) != "2-mono.ttf" {
		t.Errorf("picked %s, want the Go Mono file though it is scanned second", filepath.Base(c.path))
	}
}

// Equal matches go to the earlier directory, so a user's own fonts
// override the system's.
func TestFindPrefersEarlierDirectoriesForTheSameFamily(t *testing.T) {
	user, system := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(user, "go.ttf"), goregular.TTF)
	writeFile(t, filepath.Join(system, "go.ttf"), goregular.TTF)

	c, err := search([]string{system, user}, Regular, []string{"Go", "Go Mono"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(c.path) != system {
		t.Errorf("picked %s, want the first directory listed", c.path)
	}
}

func TestFindSkipsFilesThatAreNotFonts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "corrupt.ttf"), []byte("this is not a font"))
	writeFile(t, filepath.Join(dir, "empty.otf"), nil)
	writeFile(t, filepath.Join(dir, "notes.txt"), goregular.TTF) // wrong extension
	writeFile(t, filepath.Join(dir, "zz.ttf"), goregular.TTF)

	if _, err := findIn([]string{dir}, Regular, []string{"Go"}); err != nil {
		t.Fatalf("findIn: %v", err)
	}
}

func TestFindFollowsSymlinkedDirectoriesAndSurvivesCycles(t *testing.T) {
	real, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(real, "go.ttf"), goregular.TTF)

	if err := os.Symlink(real, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A cycle, and a dangling link, must neither hang nor fail the search.
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}

	if _, err := findIn([]string{root}, Regular, []string{"Go"}); err != nil {
		t.Fatalf("findIn: %v", err)
	}
}

// makeTTC packs plain TrueType fonts into one collection. The table
// directories inside a collection use offsets from the start of the file,
// so each font's records are shifted by where it lands.
func makeTTC(fonts ...[]byte) []byte {
	header := 12 + 4*len(fonts)

	out := make([]byte, header)
	copy(out, "ttcf")
	binary.BigEndian.PutUint32(out[4:], 0x00010000)
	binary.BigEndian.PutUint32(out[8:], uint32(len(fonts)))

	for i, f := range fonts {
		base := len(out)
		binary.BigEndian.PutUint32(out[12+4*i:], uint32(base))

		shifted := append([]byte(nil), f...)
		numTables := int(binary.BigEndian.Uint16(shifted[4:]))
		for t := range numTables {
			offset := 12 + 16*t + 8
			binary.BigEndian.PutUint32(shifted[offset:], binary.BigEndian.Uint32(shifted[offset:])+uint32(base))
		}
		out = append(out, shifted...)
	}
	return out
}

// System CJK fonts ship as .ttc, one file holding several faces; the match
// has to pick the right face out of it, not just the right file.
func TestFindPicksTheRightFontOutOfACollection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.ttc"), makeTTC(goregular.TTF, gobold.TTF, gomono.TTF))

	for _, tc := range []struct {
		style  Style
		family string
		want   string
	}{
		{Regular, "Go", "Regular"},
		{Bold, "Go", "Bold"},
		{Regular, "Go Mono", "Regular"},
	} {
		f, err := findIn([]string{dir}, tc.style, []string{tc.family})
		if err != nil {
			t.Fatalf("%s %v: %v", tc.family, tc.style, err)
		}
		if got := familyOf(t, f); got != tc.family {
			t.Errorf("%s %v: family = %q", tc.family, tc.style, got)
		}
		if got := subfamilyOf(t, f); got != tc.want {
			t.Errorf("%s %v: subfamily = %q, want %q", tc.family, tc.style, got, tc.want)
		}
	}
}

func TestFontDirsFollowTheXDGEnvironment(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_DATA_HOME", "/data/home")
	t.Setenv("XDG_DATA_DIRS", "/opt/a::/opt/b")

	got := fontDirs()
	want := []string{"/data/home/fonts", "/home/u/.fonts", "/opt/a/fonts", "/opt/b/fonts"}
	if len(got) != len(want) {
		t.Fatalf("fontDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fontDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFontDirsDefaults(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_DATA_DIRS", "")

	got := fontDirs()
	want := []string{
		"/home/u/.local/share/fonts",
		"/home/u/.fonts",
		"/usr/local/share/fonts",
		"/usr/share/fonts",
	}
	if len(got) != len(want) {
		t.Fatalf("fontDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fontDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStyleMatchesAcceptsTheSpellingsFontsUse(t *testing.T) {
	for _, tc := range []struct {
		style Style
		sub   string
		want  bool
	}{
		{Regular, "Regular", true},
		{Regular, "Book", true},
		{Regular, "Bold", false},
		{Bold, "Bold", true},
		{Bold, "Bold Italic", false},
		{Italic, "Oblique", true},
		{Italic, "Bold Italic", false},
		{BoldItalic, "Bold Italic", true},
		{BoldItalic, "BoldOblique", true},
		{BoldItalic, "Italic", false},
	} {
		if got := styleMatches(tc.style, tc.sub); got != tc.want {
			t.Errorf("styleMatches(%v, %q) = %v, want %v", tc.style, tc.sub, got, tc.want)
		}
	}
}
