package text

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// sfntNames reads the same four strings through the full parser, as the
// oracle for readNames. A name the font lacks is "".
func sfntNames(t testing.TB, f *sfnt.Font) names {
	t.Helper()

	var b sfnt.Buffer
	get := func(id sfnt.NameID) string {
		s, err := f.Name(&b, id)
		if err != nil {
			return ""
		}
		return s
	}
	return names{
		family:               get(sfnt.NameIDFamily),
		subfamily:            get(sfnt.NameIDSubfamily),
		typographicFamily:    get(sfnt.NameIDTypographicFamily),
		typographicSubfamily: get(sfnt.NameIDTypographicSubfamily),
	}
}

func TestReadNamesAgreesWithTheFullParser(t *testing.T) {
	for name, data := range map[string][]byte{
		"regular": goregular.TTF,
		"bold":    gobold.TTF,
		"mono":    gomono.TTF,
	} {
		font, err := opentype.Parse(data)
		if err != nil {
			t.Fatalf("%s: Parse: %v", name, err)
		}
		got, err := readNames(bytes.NewReader(data), 0)
		if err != nil {
			t.Fatalf("%s: readNames: %v", name, err)
		}
		if want := sfntNames(t, font); got != want {
			t.Errorf("%s: readNames = %+v, want %+v", name, got, want)
		}
	}
}

func TestFontOffsetsAndReadNamesHandleCollections(t *testing.T) {
	data := makeTTC(goregular.TTF, gobold.TTF, gomono.TTF)

	bases, collection, err := fontOffsets(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !collection || len(bases) != 3 {
		t.Fatalf("fontOffsets = %v, collection=%v; want 3 fonts in a collection", bases, collection)
	}

	col, err := opentype.ParseCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	for i, base := range bases {
		got, err := readNames(bytes.NewReader(data), base)
		if err != nil {
			t.Fatalf("font %d: %v", i, err)
		}
		font, err := col.Font(i)
		if err != nil {
			t.Fatal(err)
		}
		if want := sfntNames(t, font); got != want {
			t.Errorf("font %d: readNames = %+v, want %+v", i, got, want)
		}
	}

	if _, collection, err := fontOffsets(bytes.NewReader(goregular.TTF)); err != nil || collection {
		t.Errorf("plain font: collection=%v err=%v, want a single font", collection, err)
	}
}

// Fonts in the wild are truncated, mislabeled and hostile. None of it may
// panic, and none of it may read as a match.
func TestReadNamesRejectsMalformedFonts(t *testing.T) {
	good := goregular.TTF

	// The name table's directory record, to corrupt.
	numTables := int(binary.BigEndian.Uint16(good[4:]))
	nameRec := -1
	for i := range numTables {
		if string(good[12+16*i:12+16*i+4]) == "name" {
			nameRec = 12 + 16*i
		}
	}
	if nameRec < 0 {
		t.Fatal("fixture has no name table")
	}

	corrupt := func(edit func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		edit(b)
		return b
	}

	for name, data := range map[string][]byte{
		"empty":            nil,
		"three bytes":      good[:3],
		"header only":      good[:12],
		"truncated dir":    good[:20],
		"zero tables":      corrupt(func(b []byte) { binary.BigEndian.PutUint16(b[4:], 0) }),
		"huge tables":      corrupt(func(b []byte) { binary.BigEndian.PutUint16(b[4:], 60000) }),
		"name past EOF":    corrupt(func(b []byte) { binary.BigEndian.PutUint32(b[nameRec+8:], uint32(len(good))) }),
		"name too small":   corrupt(func(b []byte) { binary.BigEndian.PutUint32(b[nameRec+12:], 2) }),
		"name too large":   corrupt(func(b []byte) { binary.BigEndian.PutUint32(b[nameRec+12:], 1<<30) }),
		"garbage":          bytes.Repeat([]byte{0xff}, 4096),
		"empty collection": append([]byte("ttcf\x00\x01\x00\x00"), 0, 0, 0, 0),
	} {
		bases, _, err := fontOffsets(bytes.NewReader(data))
		if err != nil {
			continue
		}
		for _, base := range bases {
			if n, err := readNames(bytes.NewReader(data), base); err == nil && n != (names{}) {
				// A font that reads without error must at least not
				// invent names; corrupt-but-parsable input may
				// legitimately yield empty ones.
				t.Errorf("%s: parsed names %+v from malformed input", name, n)
			}
		}
	}
}

// parseNames indexes records by offsets read from the file, so out-of-range
// ones have to be skipped, not trusted.
func TestParseNamesSkipsRecordsThatPointOutsideTheTable(t *testing.T) {
	// One record: Windows, Unicode BMP, en-US, family, length 4, offset 100
	// into a table with no such room.
	table := make([]byte, 6+12)
	binary.BigEndian.PutUint16(table[2:], 1)  // count
	binary.BigEndian.PutUint16(table[4:], 18) // string storage offset
	rec := table[6:]
	binary.BigEndian.PutUint16(rec[0:], 3)
	binary.BigEndian.PutUint16(rec[2:], 1)
	binary.BigEndian.PutUint16(rec[4:], 0x409)
	binary.BigEndian.PutUint16(rec[6:], 1)
	binary.BigEndian.PutUint16(rec[8:], 4)
	binary.BigEndian.PutUint16(rec[10:], 100)

	if n := parseNames(table); n != (names{}) {
		t.Errorf("parseNames = %+v, want no names from an out-of-range record", n)
	}

	// A count that claims more records than the table holds.
	binary.BigEndian.PutUint16(table[2:], 1000)
	_ = parseNames(table)
}

func TestParseNamesPrefersUSEnglishWindowsNames(t *testing.T) {
	utf16be := func(s string) []byte {
		var b []byte
		for _, r := range s {
			b = binary.BigEndian.AppendUint16(b, uint16(r))
		}
		return b
	}

	type rec struct {
		platform, language uint16
		text               []byte
	}
	build := func(recs ...rec) []byte {
		head := 6 + 12*len(recs)
		table := make([]byte, head)
		binary.BigEndian.PutUint16(table[2:], uint16(len(recs)))
		binary.BigEndian.PutUint16(table[4:], uint16(head))
		var storage []byte
		for i, r := range recs {
			o := table[6+12*i:]
			binary.BigEndian.PutUint16(o[0:], r.platform)
			binary.BigEndian.PutUint16(o[4:], r.language)
			binary.BigEndian.PutUint16(o[6:], 1) // family
			binary.BigEndian.PutUint16(o[8:], uint16(len(r.text)))
			binary.BigEndian.PutUint16(o[10:], uint16(len(storage)))
			storage = append(storage, r.text...)
		}
		return append(table, storage...)
	}

	got := parseNames(build(
		rec{3, 0x411, utf16be("日本語")},        // Windows, Japanese
		rec{1, 0, []byte("Mac Name")},        // Mac English
		rec{3, 0x409, utf16be("Windows EN")}, // Windows, US English
		rec{0, 0, utf16be("Unicode")},        // Unicode platform
	))
	if got.family != "Windows EN" {
		t.Errorf("family = %q, want the Windows US English name", got.family)
	}

	// With no Windows name, the Mac English one is better than nothing.
	if got := parseNames(build(rec{1, 0, []byte("Mac Name")})); got.family != "Mac Name" {
		t.Errorf("family = %q, want the Mac name when it is the only one", got.family)
	}
	// A name only in another language never matches what a caller types.
	if got := parseNames(build(rec{1, 12, []byte("Nom")})); got.family != "" {
		t.Errorf("family = %q, want none from a non-English Mac name", got.family)
	}
}

// squash removes spaces and NULs from every name, for comparing against an
// oracle that mangles some of them.
func squash(n names) names {
	strip := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == 0 {
				return -1
			}
			return r
		}, s)
	}
	return names{
		family:               strip(n.family),
		subfamily:            strip(n.subfamily),
		typographicFamily:    strip(n.typographicFamily),
		typographicSubfamily: strip(n.typographicSubfamily),
	}
}

// The strongest check there is: every font actually installed on this
// machine, read both ways. It catches the name-table quirks — many
// languages, odd platforms — that hand-made fixtures do not, and skips on
// a machine with no fonts.
func TestReadNamesAgreesWithTheFullParserOnInstalledFonts(t *testing.T) {
	if testing.Short() {
		t.Skip("scans every installed font")
	}

	checked := 0
	for _, dir := range fontDirs() {
		walkFonts(dir, 0, func(path string) bool {
			data, err := os.ReadFile(path)
			if err != nil {
				return false
			}
			r := bytes.NewReader(data)
			bases, collection, err := fontOffsets(r)
			if err != nil {
				return false
			}

			var fonts []*sfnt.Font
			if collection {
				col, err := opentype.ParseCollection(data)
				if err != nil {
					return false
				}
				for i := range col.NumFonts() {
					f, err := col.Font(i)
					if err != nil {
						return false
					}
					fonts = append(fonts, f)
				}
			} else {
				f, err := opentype.Parse(data)
				if err != nil {
					return false
				}
				fonts = append(fonts, f)
			}

			for i, base := range bases {
				got, err := readNames(r, base)
				if err != nil {
					t.Errorf("%s[%d]: readNames: %v", path, i, err)
					continue
				}
				// Strict equality, modulo NULs: for one font on a full
				// desktop install (an icon font whose names are Symbol
				// encoded) sfnt returns the raw UTF-16 bytes undecoded,
				// "\x00i\x00c\x00o...", where the name is "icomoon".
				// Every other font agrees exactly.
				if want := sfntNames(t, fonts[i]); squash(got) != squash(want) {
					t.Errorf("%s[%d]: readNames = %+v, oracle %+v", path, i, got, want)
				}
				checked++
			}
			return false
		})
	}
	if checked == 0 {
		t.Skip("no installed fonts to check")
	}
	t.Logf("checked %d installed fonts", checked)
}
