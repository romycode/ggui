package keysymdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleHeader = `
#define XKB_KEY_NoSymbol 0x000000 /* Special KeySym */
#define XKB_KEY_A 0x0041 /* U+0041 LATIN CAPITAL LETTER A */
#define XKB_KEY_KP_Enter 0xff8d /*<U+000D CARRIAGE RETURN>*/
#define XKB_KEY_topleftradical 0x08a2 /*(U+250C BOX DRAWINGS LIGHT DOWN AND RIGHT)*/
#define XKB_KEY_F1 0xffbe
#define XKB_KEY_L1 0xffc8 /* deprecated alias for F11 */
#define XKB_KEY_F11 0xffc8
#define XKB_KEY_dead_tilde 0xfe53
#define XKB_KEY_dead_perispomeni 0xfe53 /* non-deprecated alias for dead_tilde */
#define XKB_KEY_Mode_switch 0xff7e
#define XKB_KEY_script_switch 0xff7e
`

func TestParseBuildsNamesCanonicalNamesAndLegacyRunes(t *testing.T) {
	tables, err := Parse(strings.NewReader(sampleHeader))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for name, want := range map[string]uint32{
		"F1": 0xffbe, "L1": 0xffc8, "F11": 0xffc8,
		"dead_perispomeni": 0xfe53,
	} {
		if got := tables.Names[name]; got != want {
			t.Errorf("Names[%q] = %#x, want %#x", name, got, want)
		}
	}
	if got := tables.CanonicalNames[0xffc8]; got != "F11" {
		t.Errorf("canonical 0xffc8 = %q, want F11", got)
	}
	if got := tables.CanonicalNames[0xfe53]; got != "dead_tilde" {
		t.Errorf("canonical 0xfe53 = %q, want dead_tilde", got)
	}
	if got := tables.CanonicalNames[0xff7e]; got != "Mode_switch" {
		t.Errorf("canonical 0xff7e = %q, want Mode_switch", got)
	}
	if _, ok := tables.Runes[0x0041]; ok {
		t.Error("Latin-1 entry A must stay algorithmic")
	}
	if got := tables.Runes[0xff8d]; got != '\r' {
		t.Errorf("KP_Enter rune = %U, want U+000D", got)
	}
	if got := tables.Runes[0x08a2]; got != '\u250c' {
		t.Errorf("topleftradical rune = %U, want U+250C", got)
	}
}

func TestParseFindsUnicodeAnnotationAfterMetadata(t *testing.T) {
	const src = `#define XKB_KEY_XF86Numeric0 0x10081200 /* v2.6.28 KEY_NUMERIC_0 <U+0030 DIGIT ZERO> */`

	tables, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := tables.Runes[0x10081200], rune('0'); got != want {
		t.Errorf("Runes[XF86Numeric0] = %U, want %U", got, want)
	}
}

func TestParseIgnoresEmbeddedUnicodeText(t *testing.T) {
	for _, comment := range []string{"GNU+0041 metadata", "GNU+Linux"} {
		t.Run(comment, func(t *testing.T) {
			src := "#define XKB_KEY_Example 0x1234 /* " + comment + " */"
			tables, err := Parse(strings.NewReader(src))
			if err != nil {
				t.Fatalf("Parse comment %q: %v", comment, err)
			}
			if got, ok := tables.Runes[0x1234]; ok {
				t.Errorf("Runes[0x1234] for comment %q = %U, want no mapping", comment, got)
			}
		})
	}
}

func TestParseTreatsParenthesizedAnnotationAfterMetadataAsDeprecated(t *testing.T) {
	const src = `
#define XKB_KEY_old_name 0x08a2 /* metadata (U+250C BOX DRAWINGS LIGHT DOWN AND RIGHT) */
#define XKB_KEY_new_name 0x08a2 /* U+250C BOX DRAWINGS LIGHT DOWN AND RIGHT */
`

	tables, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := tables.CanonicalNames[0x08a2], "new_name"; got != want {
		t.Errorf("CanonicalNames[0x08a2] = %q, want %q", got, want)
	}
}

func TestParseRejectsMalformedDefinitions(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"empty", "/* nothing */", "no keysym definitions"},
		{"bad value", "#define XKB_KEY_Bad 0xnope", "invalid keysym definition"},
		{"bad unicode", "#define XKB_KEY_Bad 0x1234 /* U+XYZ */", "invalid Unicode annotation"},
		{"bad unicode after metadata", "#define XKB_KEY_Bad 0x1234 /* metadata <U+XYZ */", "invalid Unicode annotation"},
		{"name conflict", "#define XKB_KEY_X 0x1\n#define XKB_KEY_X 0x2", "name X has conflicting values"},
		{"rune conflict", "#define XKB_KEY_X 0x1234 /* U+1111 A */\n#define XKB_KEY_Y 0x1234 /* U+2222 B */", "keysym 0x1234 has conflicting Unicode mappings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseVendoredLibxkbcommon1132(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "..", "third_party", "libxkbcommon", "xkbcommon-keysyms.h"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tables, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse vendored header: %v", err)
	}
	if got, want := len(tables.Names), 2638; got != want {
		t.Fatalf("name count = %d, want %d", got, want)
	}
	if got := tables.Names["ISO_Level3_Latch"]; got != 0xfe04 {
		t.Errorf("ISO_Level3_Latch = %#x, want 0xfe04", got)
	}
}
