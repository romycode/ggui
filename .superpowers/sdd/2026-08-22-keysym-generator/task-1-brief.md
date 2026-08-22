### Task 1: Vendor and strictly parse the libxkbcommon keysym dataset

**Files:**
- Create: `third_party/libxkbcommon/xkbcommon-keysyms.h`
- Create: `third_party/libxkbcommon/README.md`
- Create: `cmd/keysymgen/internal/keysymdata/parse.go`
- Test: `cmd/keysymgen/internal/keysymdata/parse_test.go`

**Interfaces:**
- Consumes: libxkbcommon 1.13.2's installed or downloaded `include/xkbcommon/xkbcommon-keysyms.h` snapshot.
- Produces:

```go
package keysymdata

type Tables struct {
	Names          map[string]uint32
	CanonicalNames map[uint32]string
	Runes          map[uint32]rune
}

func Parse(r io.Reader) (Tables, error)
```

- `Names` contains every explicit `XKB_KEY_` spelling, including aliases.
- `CanonicalNames` contains one stable display name per keysym value.
- `Runes` contains only mappings not already covered by `Keysym.Rune`'s direct-Unicode and Latin-1 rules.

- [ ] **Step 1: Copy and verify the pinned upstream input**

Confirm the installed development package matches the oracle version:

```sh
test "$(pkg-config --modversion xkbcommon)" = "1.13.2"
mkdir -p third_party/libxkbcommon
cp /usr/include/xkbcommon/xkbcommon-keysyms.h third_party/libxkbcommon/xkbcommon-keysyms.h
test "$(sha256sum third_party/libxkbcommon/xkbcommon-keysyms.h | cut -d' ' -f1)" = "13023369f65a17411606084e3e09557b4886aeb15f89affba4aaa86490a463f3"
```

If the local package is not exactly 1.13.2, obtain the same file from the
`xkbcommon-1.13.2` upstream tag, verify the checksum above, and do not use a
newer header.

- [ ] **Step 2: Add provenance documentation**

Create `third_party/libxkbcommon/README.md` in Spanish with this content:

```markdown
# Datos de keysyms de libxkbcommon

`xkbcommon-keysyms.h` procede de libxkbcommon 1.13.2:

`include/xkbcommon/xkbcommon-keysyms.h`

Origen: <https://github.com/xkbcommon/libxkbcommon/tree/xkbcommon-1.13.2>

SHA-256:
`13023369f65a17411606084e3e09557b4886aeb15f89affba4aaa86490a463f3`

La cabecera conserva íntegros sus avisos de copyright y licencia. Se
vendoriza para que `go run ./cmd/keysymgen` sea reproducible y no dependa de
las cabeceras instaladas ni de la red.

Para actualizarla, se elige primero una versión concreta de libxkbcommon, se
sustituye la cabecera por la de esa etiqueta, se actualizan versión y checksum
en este fichero, y se ejecutan el generador y el oráculo XKB. La actualización
de datos y cualquier cambio de comportamiento se revisan en el mismo commit.
```

- [ ] **Step 3: Write failing parser tests**

Create `parse_test.go` with a compact fixture that exercises all three Unicode
annotation forms, aliases, explicit deprecation, implicit alias deprecation,
and a keysym without text:

```go
package keysymdata

import (
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
```

Add table-driven error cases with exact substrings:

```go
func TestParseRejectsMalformedDefinitions(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"empty", "/* nothing */", "no keysym definitions"},
		{"bad value", "#define XKB_KEY_Bad 0xnope", "invalid keysym definition"},
		{"bad unicode", "#define XKB_KEY_Bad 0x1234 /* U+XYZ */", "invalid Unicode annotation"},
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
```

Add a pinned-snapshot test:

```go
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
```

- [ ] **Step 4: Run the parser tests and confirm the RED state**

Run:

```sh
go test ./cmd/keysymgen/internal/keysymdata -run TestParse -count=1 -v
```

Expected: FAIL because `Tables` and `Parse` do not exist. A fixture path or
checksum failure is not the intended RED state; fix setup first.

- [ ] **Step 5: Implement the strict parser**

Create `parse.go` with the public types above and these private parsing rules:

```go
var (
	defineRE  = regexp.MustCompile(`^#define\s+XKB_KEY_([A-Za-z0-9_]+)\s+0x([0-9A-Fa-f]+)(?:\s+/\*(.*?)\*/)?\s*$`)
	unicodeRE = regexp.MustCompile(`^[<(]?U\+([0-9A-F]{4,6})(?:\s|[>)])`)
)

type definition struct {
	name                string
	value               uint32
	r                    rune
	hasRune             bool
	deprecated          bool
	explicitNonDeprecated bool
}
```

`Parse` must:

1. scan line by line with `bufio.Scanner` and a 1 MiB maximum token;
2. ignore comments, blank lines, preprocessor structure, and non-keysym
   definitions;
3. treat a line beginning with `#define XKB_KEY_` but not matching
   `defineRE` as `invalid keysym definition on line N`;
4. parse values with `strconv.ParseUint(..., 16, 32)`;
5. trim the comment and recognize `U+`, `<U+`, and `(U+` prefixes;
6. reject a recognized Unicode prefix that does not match `unicodeRE`;
7. mark comments beginning with `deprecated` and parenthesized Unicode
   annotations as deprecated; mark comments beginning with
   `non-deprecated alias` as explicitly non-deprecated;
8. insert every spelling into `Names`, rejecting only same-name/different-value
   conflicts;
9. choose the first non-deprecated name as canonical, with the first spelling
   retained as fallback if every spelling is deprecated; an ordinary later
   alias is implicit-deprecated after a canonical name already exists;
10. reject different Unicode mappings for one keysym value;
11. omit from `Runes` values satisfying either direct rule:

```go
func algorithmicRune(value uint32) bool {
	return value&0xff000000 == 0x01000000 ||
		(value >= 0x20 && value <= 0x7e) ||
		(value >= 0xa0 && value <= 0xff)
}
```

Wrap scanner and validation errors with the prefix `keysymgen:` so the CLI
can print them without adding context a second time.

- [ ] **Step 6: Run tests and verify GREEN**

Run:

```sh
gofmt -w cmd/keysymgen/internal/keysymdata
go test ./cmd/keysymgen/internal/keysymdata -count=1 -v
```

Expected: PASS, including exact `2638` name count and `ISO_Level3_Latch`.

- [ ] **Step 7: Commit Task 1**

```sh
git add third_party/libxkbcommon cmd/keysymgen/internal/keysymdata
git commit -m "keysymgen: parse vendored libxkbcommon keysyms"
```

**Task 1 success:** the pinned source is licensed and reproducible, every
explicit name parses, canonical aliases are deterministic, Unicode conflicts
are rejected, and no production keyboard code has changed.

---

