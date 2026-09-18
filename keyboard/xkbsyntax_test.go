package keyboard

import (
	"fmt"
	"strings"
	"testing"
)

// Shared with the oracle suite so these hand-checked expectations are also
// checked against libxkbcommon on exactly the same source text.
var syntaxCases = []struct {
	name     string
	key      string
	compat   string
	modmap   string
	types    string
	noRepeat bool
	wantSyms []Keysym // base and shifted symbol for each group
}{
	{name: "explicit group type", key: `type[Group1]="TWO_LEVEL", [a,A]`},
	{name: "unindexed symbols", key: `symbols=[a,A]`},
	{name: "bare before indexed", key: `[a,A], symbols[Group2]=[b,B]`, wantSyms: []Keysym{'a', 'A', 'b', 'B'}},
	{name: "indexed before bare", key: `symbols[Group2]=[b,B], [a,A]`, wantSyms: []Keysym{'a', 'A', 'b', 'B'}},
	{name: "two unindexed assignments", key: `symbols=[a,A], symbols=[b,B]`, wantSyms: []Keysym{'a', 'A', 'b', 'B'}},
	{name: "level nine", types: `type "NINE" { modifiers=Shift; map[Shift]=9; };`, key: `type="NINE", [a,b,c,d,e,f,g,h,i]`, wantSyms: []Keysym{'a', 'i'}},
	{name: "interpret built-in default", compat: `interpret a { action=NoAction(); };`, noRepeat: true},
	{name: "named predicate does not match", compat: `interpret a+Exactly(Shift) { repeat=False; };`},
	{name: "wildcard matches empty modmap", compat: `interpret Any+AnyOfOrNone(all) { repeat=False; };`, noRepeat: true},
	{name: "wildcard requires modmap", compat: `interpret Any+AnyOf(all) { repeat=False; };`},
	{name: "NoSymbol is wildcard", compat: `interpret NoSymbol+AnyOfOrNone(all) { repeat=False; };`, noRepeat: true},
	{name: "only level two matches", compat: `interpret A { repeat=False; };`},
	{name: "empty base level", key: `[NoSymbol,A]`, noRepeat: true, wantSyms: []Keysym{0, 'A'}},
	{name: "empty base ignores later repeat", key: `[NoSymbol,A]`, compat: `interpret A { repeat=True; };`, noRepeat: true, wantSyms: []Keysym{0, 'A'}},
	{name: "only group two matches", key: `[a,A], [b,B]`, compat: `interpret b { repeat=False; };`, wantSyms: []Keysym{'a', 'A', 'b', 'B'}},
	{name: "default changes forward", compat: `interpret.repeat=False; interpret b {}; interpret.repeat=True; interpret a {};`},
	{name: "later default is not retroactive", compat: `interpret.repeat=False; interpret a {}; interpret.repeat=True; interpret b {};`, noRepeat: true},
	{name: "default can turn off", compat: `interpret.repeat=True; interpret b {}; interpret.repeat=False; interpret a {};`, noRepeat: true},
	{name: "repeat off", key: `repeat=off, [a,A]`, noRepeat: true},
	{name: "repeat on overrides compat", key: `repeat=on, [a,A]`, compat: `interpret a { repeat=False; };`},
	{name: "repeat false overrides compat", key: `repeat = FALSE, [a,A]`, compat: `interpret a { repeat=True; };`, noRepeat: true},
	{name: "repeat no without space", key: `repeat=no, [a,A]`, noRepeat: true},
	{name: "repeat yes overrides compat", key: `repeat=Yes, [a,A]`, compat: `interpret a { repeat=False; };`},
	{name: "spaced predicate", compat: `interpret a + AnyOfOrNone( all ) { repeat=False; };`, noRepeat: true},
	{
		name:   "spaced virtual modifier interpret",
		key:    `type="CUSTOM", [a,A]`,
		types:  `virtual_modifiers Custom; type "CUSTOM" { modifiers=Custom; map[Custom]=2; };`,
		compat: `virtual_modifiers Custom; interpret a + AnyOf( Shift ) { virtualModifier=Custom; repeat=True; };`,
		modmap: `modifier_map Shift { <AC01> };`,
	},
	{name: "specific predicate wins", compat: `interpret a+Exactly(Shift) { repeat=False; }; interpret a+AnyOfOrNone(all) { repeat=True; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
	{name: "specific keysym wins", compat: `interpret Any+Exactly(Shift) { repeat=True; }; interpret a+AnyOfOrNone(all) { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
	{name: "equal specificity uses first", compat: `interpret a+AnyOf(Shift) { repeat=False; }; interpret a+AnyOf(all) { repeat=True; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
	{name: "NoneOf matches", compat: `interpret a+NoneOf(Control) { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
	{name: "NoneOf rejects", compat: `interpret a+NoneOf(Shift) { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`},
	{name: "AllOf matches", compat: `interpret a+AllOf(Shift) { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
	{name: "AllOf rejects subset", compat: `interpret a+AllOf(Shift+Control) { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`},
	{name: "implicit Exactly", compat: `interpret a+Shift { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
	{name: "Any predicate alias", compat: `interpret a+Any { repeat=False; };`, modmap: `modifier_map Shift { <AC01> };`, noRepeat: true},
}

func syntaxKeymap(key, compat, modmap, types string) string {
	if key == "" {
		key = `[a,A]`
	}
	return fmt.Sprintf(`xkb_keymap {
xkb_keycodes "t" {
	minimum=8; maximum=255;
	<AC01> = 38;
};
xkb_types "t" {
	type "ONE_LEVEL" { modifiers=None; };
	type "TWO_LEVEL" { modifiers=Shift; map[Shift]=2; };
	type "ALPHABETIC" { modifiers=Shift; map[Shift]=2; };
	%s
};
xkb_compatibility "t" {
	%s
};
xkb_symbols "t" {
	key <AC01> { %s };
	%s
};
};`, types, compat, key, modmap)
}

func TestKeymapSyntax(t *testing.T) {
	for _, tc := range syntaxCases {
		t.Run(tc.name, func(t *testing.T) {
			km, err := Compile(syntaxKeymap(tc.key, tc.compat, tc.modmap, tc.types))
			if err != nil {
				t.Fatal(err)
			}
			st := km.NewState()
			if got, want := st.Repeats(38), !tc.noRepeat; got != want {
				t.Errorf("Repeats(38) = %t, want %t", got, want)
			}
			wantSyms := tc.wantSyms
			if wantSyms == nil {
				wantSyms = []Keysym{'a', 'A'}
			}
			for i, want := range wantSyms {
				st.UpdateMask(uint32(i%2), 0, 0, uint32(i/2))
				if got := st.Sym(38); got != want {
					t.Errorf("Sym(38) group=%d shift=%d = %#x, want %#x", i/2, i%2, got, want)
				}
			}
		})
	}
}

func TestTypeLevelLimit(t *testing.T) {
	// libxkbcommon accepts levels 1..2048; eight is merely a common layout.
	symbols := strings.Repeat("a,", 2047) + "b"
	src := syntaxKeymap(`type="WIDE", [`+symbols+`]`, "", "", `type "WIDE" { modifiers=Shift; map[Shift]=2048; };`)
	km, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	st := km.NewState()
	st.UpdateMask(ModShift, 0, 0, 0)
	if got := st.Sym(38); got != 'b' {
		t.Errorf("Sym(38) at level 2048 = %#x, want b", got)
	}
}

func TestInvalidKeymapIndices(t *testing.T) {
	for _, index := range []string{"0", "-1", "999999999999999999999999999999"} {
		for _, field := range []string{"type", "symbols", "level"} {
			t.Run(field+index, func(t *testing.T) {
				key, types := `[a,A]`, ""
				switch field {
				case "type":
					key = `type[Group` + index + `]="TWO_LEVEL", [a,A]`
				case "symbols":
					key = `symbols[Group` + index + `]=[a,A]`
				case "level":
					types = `type "BAD" { modifiers=Shift; map[Shift]=Level` + index + `; };`
				}
				if _, err := Compile(syntaxKeymap(key, "", "", types)); err == nil {
					t.Errorf("Compile with %s index %s succeeded, want error", field, index)
				}
			})
		}
	}
	for _, tc := range []struct{ key, types string }{
		{key: `type[Group5]="TWO_LEVEL", [a,A]`},
		{key: `symbols[Group5]=[a,A]`},
		{types: `type "BAD" { modifiers=Shift; map[Shift]=2049; };`},
	} {
		if _, err := Compile(syntaxKeymap(tc.key, "", "", tc.types)); err == nil {
			t.Errorf("Compile(%q, %q) succeeded, want out-of-range error", tc.key, tc.types)
		}
	}
}
