package keyboard

import (
	"fmt"
	"testing"
)

// libxkbcommon serializes keys that carry an explicit type across several
// lines:
//
//	key <FK01> {
//		type= "CTRL+ALT",
//		symbols[1]= [ 0xffbe, 0xffbe, 0xffbe, 0xffbe, 0x1008fe01 ]
//	};
//
// reKey was compiled with (?m) but not (?s), so its `(.*?)` body could not
// cross a newline and every such key was dropped from the keymap entirely:
// F1-F12, the keypad operators, PrintScreen and Pause among them.
func TestMultiLineKeyBlockIsParsed(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<FK01> = 67;
};
xkb_types "t" {
	type "CTRL+ALT" {
		modifiers= Shift;
		map[Shift]= 2;
	};
};
xkb_symbols "t" {
	key <FK01> {
		type= "CTRL+ALT",
		symbols[1]= [ 0xffbe, 0xffbf ]
	};
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	k, ok := km.keys[67]
	if !ok {
		t.Fatal("key <FK01> was dropped from the keymap")
	}
	if got, want := k.types[0], "CTRL+ALT"; got != want {
		t.Errorf("type = %q, want %q", got, want)
	}
	// The `symbols[1]=` index must not be mistaken for a symbol list.
	if len(k.groups) != 1 {
		t.Fatalf("groups = %v, want exactly 1 group", k.groups)
	}
	want := []Keysym{0xffbe, 0xffbf}
	if len(k.groups[0]) != len(want) {
		t.Fatalf("group 0 = %v, want %v", k.groups[0], want)
	}
	for i, w := range want {
		if k.groups[0][i] != w {
			t.Errorf("group 0 = %v, want %v", k.groups[0], want)
			break
		}
	}
}

// Real compiled keymaps (as dumped by xkb_keymap_get_as_string, e.g. via
// `xkbcli compile-keymap`) write level maps as bare numbers, not "Level2":
//
//	type "ALPHABETIC" { modifiers= Shift+Lock; map[Shift]= 2; map[Lock]= 2; };
//
// reTypeMap required the word "Level" before the digit, so none of these
// entries ever matched and every type silently fell back to level 0.
func TestLevelMapWithoutLevelKeyword(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<AB01> = 52;
};
xkb_types "t" {
	type "TWO_LEVEL" {
		modifiers= Shift;
		map[Shift]= 2;
	};
};
xkb_symbols "t" {
	key <AB01> { type= "TWO_LEVEL", [ a, A ] };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()

	st.UpdateMask(0, 0, 0, 0)
	if got := st.Sym(52); got != ParseKeysym("a") {
		t.Errorf("Sym(no mods) = %#x, want %#x (a)", got, ParseKeysym("a"))
	}

	st.UpdateMask(ModShift, 0, 0, 0)
	if got := st.Sym(52); got != ParseKeysym("A") {
		t.Errorf("Sym(Shift) = %#x, want %#x (A)", got, ParseKeysym("A"))
	}
}

// XKB's automatic type assignment (used when a key doesn't declare an
// explicit type) assigns "KEYPAD" to a 2-symbol key when either symbol is
// in the KP_* range, not "TWO_LEVEL". Getting this wrong means NumLock
// never changes what a keypad key produces.
func TestGuessTypeKeypad(t *testing.T) {
	kpHome, kp7 := Keysym(0xff95), Keysym(0xffb7) // KP_Home, KP_7
	got := guessType([]Keysym{kpHome, kp7})
	if got != "KEYPAD" {
		t.Errorf("guessType(KP_Home, KP_7) = %q, want KEYPAD", got)
	}
}

// End-to-end: a keypad key with no explicit type should honor NumLock
// (falls back to the default NumLock->Mod2 virtual mapping), not Shift.
//
// The empty xkb_compatibility block below is load-bearing: with no compat
// section at all resolveVirtualMods never runs, the NumLock->Mod2 fallback
// never fires, and this fails for a reason unrelated to what it tests.
func TestKeypadKeyHonorsNumLock(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<KP7> = 79;
};
xkb_types "t" {
	type "KEYPAD" {
		modifiers= NumLock;
		map[NumLock]= 2;
	};
};
xkb_compatibility "t" {
};
xkb_symbols "t" {
	key <KP7> { [ 0xff95, 0xffb7 ] };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()
	kpHome, kp7 := Keysym(0xff95), Keysym(0xffb7)

	st.UpdateMask(0, 0, 0, 0)
	if got := st.Sym(79); got != kpHome {
		t.Errorf("Sym(no mods) = %#x, want KP_Home (%#x)", got, kpHome)
	}

	st.UpdateMask(ModMod2, 0, 0, 0) // NumLock
	if got := st.Sym(79); got != kp7 {
		t.Errorf("Sym(NumLock) = %#x, want KP_7 (%#x)", got, kp7)
	}
}

// All standard named compatibility interprets resolve through the generated
// table. Truly unknown names must still be ignored, or their zero value would
// match explicit NoSymbol entries in unrelated modifier-mapped keys and
// inflate the virtual modifier masks.
func TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<LV3> = 100;
	<NUM> = 101;
	<M5N> = 102;
	<M2N> = 103;
	<L3S> = 104;
	<WRK> = 105;
};
xkb_types "t" {
	type "LEVEL_THREE" {
		modifiers= LevelThree;
		map[LevelThree]= 2;
	};
	type "NUM_LOCK" {
		modifiers= NumLock;
		map[NumLock]= 2;
	};
	type "WORKING_NAMED" {
		modifiers= Shift+WorkingLevelThree;
		map[Shift+WorkingLevelThree]= 2;
	};
};
xkb_compatibility "t" {
	interpret ISO_Level3_Shift { virtualModifier= LevelThree; };
	interpret ISO_Level3_Latch { virtualModifier= LevelThree; };
	interpret ISO_Level3_Lock { virtualModifier= LevelThree; };
	interpret Num_Lock { virtualModifier= NumLock; };
	interpret ISO_Level3_Shift { virtualModifier= WorkingLevelThree; };
	interpret ISO_Level3_Latch { virtualModifier= WorkingLevelThree; };
};
xkb_symbols "t" {
	key <LV3> { type= "LEVEL_THREE", [ 0x61, 0x62 ] };
	key <NUM> { type= "NUM_LOCK", [ 0x63, 0x64 ] };
	key <M5N> { [ NoSymbol ] };
	key <M2N> { [ NoSymbol ] };
	key <L3S> { [ ISO_Level3_Shift ] };
	key <WRK> { type= "WORKING_NAMED", [ 0x65, 0x66 ] };
	modifier_map Mod5 { <L3S>, <M5N> };
	modifier_map Mod2 { <M2N> };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got, want := km.vmods["LevelThree"], uint32(ModMod5); got != want {
		t.Errorf("LevelThree mask = %#x, want %#x", got, want)
	}
	if got, want := km.vmods["NumLock"], uint32(ModMod2); got != want {
		t.Errorf("NumLock mask = %#x, want %#x", got, want)
	}
	st := km.NewState()

	st.UpdateMask(ModMod5, 0, 0, 0)
	if got, want := st.Sym(100), Keysym(0x62); got != want {
		t.Errorf("Sym(LevelThree with ModMod5) = %#x, want %#x", got, want)
	}
	if got, want := st.Sym(101), Keysym(0x63); got != want {
		t.Errorf("Sym(NumLock with ModMod5) = %#x, want %#x", got, want)
	}

	st.UpdateMask(ModShift, 0, 0, 0)
	if got, want := st.Sym(105), Keysym(0x65); got != want {
		t.Errorf("Sym(WorkingLevelThree with Shift) = %#x, want %#x", got, want)
	}

	st.UpdateMask(ModShift|ModMod5, 0, 0, 0)
	if got, want := st.Sym(105), Keysym(0x66); got != want {
		t.Errorf("Sym(WorkingLevelThree with Shift+ModMod5) = %#x, want %#x", got, want)
	}

	st.UpdateMask(ModMod2, 0, 0, 0)
	if got, want := st.Sym(100), Keysym(0x61); got != want {
		t.Errorf("Sym(LevelThree with ModMod2) = %#x, want %#x", got, want)
	}
	if got, want := st.Sym(101), Keysym(0x64); got != want {
		t.Errorf("Sym(NumLock with ModMod2) = %#x, want %#x", got, want)
	}
}

func TestVoidSymbolInterpretDoesNotMatchNoSymbol(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<VOID> = 110;
	<NONE> = 111;
};
xkb_compatibility "t" {
	interpret VoidSymbol { virtualModifier= Void; };
	interpret NoSymbol { virtualModifier= None; };
};
xkb_symbols "t" {
	key <VOID> { [ VoidSymbol ] };
	key <NONE> { [ NoSymbol ] };
	modifier_map Mod4 { <VOID> };
	modifier_map Mod2 { <NONE> };
};
`

	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got, want := km.vmods["Void"], uint32(ModMod4); got != want {
		t.Errorf("Void mask = %#x, want %#x", got, want)
	}
	if got, want := km.vmods["None"], uint32(ModMod2); got != want {
		t.Errorf("None mask = %#x, want %#x", got, want)
	}
}

// A numeric zero keysym is deliberate: its compatibility interpret must
// continue to match a modifier-mapped NoSymbol key.
func TestNumericZeroInterpretMatchesNoSymbol(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<ZERO> = 110;
	<ZERO_MOD> = 111;
};
xkb_types "t" {
	type "SHIFT_ZERO" {
		modifiers= Shift+Zero;
		map[Shift+Zero]= 2;
	};
};
xkb_compatibility "t" {
	interpret 0x0 { virtualModifier= Zero; };
};
xkb_symbols "t" {
	key <ZERO> { type= "SHIFT_ZERO", [ 0x61, 0x62 ] };
	key <ZERO_MOD> { [ NoSymbol ] };
	modifier_map Mod3 { <ZERO_MOD> };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()

	st.UpdateMask(ModShift, 0, 0, 0)
	if got, want := st.Sym(110), Keysym(0x61); got != want {
		t.Errorf("Sym(Zero with Shift) = %#x, want %#x", got, want)
	}

	st.UpdateMask(ModShift|ModMod3, 0, 0, 0)
	if got, want := st.Sym(110), Keysym(0x62); got != want {
		t.Errorf("Sym(Zero with Shift+ModMod3) = %#x, want %#x", got, want)
	}
}

// preserve[] marks a modifier as "not consumed" by the type even though it
// took part in selecting the level. XKB's own FOUR_LEVEL_SEMIALPHABETIC uses
// exactly this: Lock still picks level 2, but must not be reported as
// consumed, or callers that compare Effective()&^Consumed() against a
// shortcut binding would wrongly ignore Lock+key chords.
//
// Before the fix, preserve[] was parsed and discarded (stuffed into mapRaw
// under a "preserve:" prefix, then skipped in resolveTypeMasks), so
// t.preserve was always empty and Consumed() returned the full t.mods.
func TestConsumedHonorsPreserveRealModifier(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<AB01> = 52;
};
xkb_types "t" {
	type "T" {
		modifiers= Shift+Lock;
		map[Shift]= 2;
		map[Lock]= 2;
		preserve[Lock]= Lock;
	};
};
xkb_symbols "t" {
	key <AB01> { type= "T", [ 0x61, 0x41 ] };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()
	st.UpdateMask(0, 0, ModLock, 0)
	if got, want := st.Consumed(52), ModShift; got != want {
		t.Errorf("Consumed(Lock) = %#x, want %#x (ModShift; Lock is preserved)", got, want)
	}
}

// Mirrors the real FOUR_LEVEL_SEMIALPHABETIC type: preserve[] can name a
// virtual modifier on either side, which only resolves to a real mask after
// resolveVirtualMods has run. The empty xkb_compatibility block is
// load-bearing: without it resolveVirtualMods never runs, LevelThree never
// falls back to Mod5, and this fails for a reason unrelated to preserve.
func TestConsumedHonorsPreserveVirtualModifier(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<AB01> = 52;
};
xkb_types "t" {
	type "T4" {
		modifiers= Shift+Lock+LevelThree;
		map[Lock+LevelThree]= 3;
		preserve[Lock+LevelThree]= Lock;
	};
};
xkb_compatibility "t" {
};
xkb_symbols "t" {
	key <AB01> { type= "T4", [ 0x61, 0x41, 0x62, 0x42 ] };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()
	st.UpdateMask(0, 0, ModLock|ModMod5, 0)
	want := ModShift | ModMod5
	if got := st.Consumed(52); got != want {
		t.Errorf("Consumed(Lock+Mod5) = %#x, want %#x (ModShift|ModMod5; Lock is preserved)", got, want)
	}
}

// preserve[X]= none; must resolve to mask 0 (nothing preserved), relying on
// splitMods already dropping "none" rather than any special-casing.
func TestConsumedHonorsPreserveNone(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<AB01> = 52;
};
xkb_types "t" {
	type "T2" {
		modifiers= Shift;
		map[Shift]= 2;
		preserve[Shift]= none;
	};
};
xkb_symbols "t" {
	key <AB01> { type= "T2", [ 0x61, 0x41 ] };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()
	st.UpdateMask(ModShift, 0, 0, 0)
	if got, want := st.Consumed(52), ModShift; got != want {
		t.Errorf("Consumed(Shift) = %#x, want %#x (full t.mods; nothing preserved)", got, want)
	}
}

// guessType mirrors libxkbcommon's FindAutomaticType. The decisive test is
// on the BASE pair (levels 1/2): only when that is an ordered lower/upper
// case pair does the AltGr pair (levels 3/4) get consulted at all. Each
// expectation below was read back off libxkbcommon 1.13.2 by compiling the
// key into a real keymap and probing the consumed-modifier mask, which
// identifies the type unambiguously (FOUR_LEVEL consumes Shift+LevelThree
// = 0x81, the ALPHABETIC variants add Lock = 0x83).
func TestGuessTypeMatchesLibraryAlgorithm(t *testing.T) {
	const (
		a, A                  = Keysym(0x0061), Keysym(0x0041)
		q, Q                  = Keysym(0x0071), Keysym(0x0051)
		m, M                  = Keysym(0x006d), Keysym(0x004d)
		comma, less           = Keysym(0x002c), Keysym(0x003c)
		ccedilla, Ccedilla    = Keysym(0x00e7), Keysym(0x00c7)
		adiaeresis, Adiaresis = Keysym(0x00e4), Keysym(0x00c4)
		braceright, deadBreve = Keysym(0x007d), Keysym(0xfe55)
		mu, masculine         = Keysym(0x00b5), Keysym(0x00ba)
		kpHome, kp7           = Keysym(0xff95), Keysym(0xffb7)
	)
	for _, tc := range []struct {
		name string
		syms []Keysym
		want string
	}{
		{"single symbol", []Keysym{a}, "ONE_LEVEL"},
		{"case pair", []Keysym{a, A}, "ALPHABETIC"},
		// Not an ordered lower/upper pair, so not ALPHABETIC: "both are
		// letters" is the wrong predicate.
		{"same letter twice", []Keysym{a, a}, "TWO_LEVEL"},
		{"upper then lower", []Keysym{A, a}, "TWO_LEVEL"},
		{"keypad pair", []Keysym{kpHome, kp7}, "KEYPAD"},
		// es <BKSL>: alphabetic base pair, non-alphabetic AltGr pair.
		{"alpha base, non-alpha altgr", []Keysym{ccedilla, Ccedilla, braceright, deadBreve}, "FOUR_LEVEL_SEMIALPHABETIC"},
		// Alphabetic on both pairs.
		{"alpha base, alpha altgr", []Keysym{q, Q, adiaeresis, Adiaresis}, "FOUR_LEVEL_ALPHABETIC"},
		// us(intl) <AB08>: the AltGr pair IS a case pair, but the base pair
		// is not, so the library never looks at it and stays on FOUR_LEVEL.
		// Assigning an ALPHABETIC variant here makes Caps Lock turn "," into "<".
		{"non-alpha base, alpha altgr", []Keysym{comma, less, ccedilla, Ccedilla}, "FOUR_LEVEL"},
		// es <AB07>: mu/masculine are letters to unicode.IsLetter but are
		// not a case pair, so this is SEMIALPHABETIC, not ALPHABETIC.
		{"alpha base, letter-but-not-case-pair altgr", []Keysym{m, M, mu, masculine}, "FOUR_LEVEL_SEMIALPHABETIC"},
		{"keypad base, four symbols", []Keysym{kpHome, kp7, comma, less}, "FOUR_LEVEL_KEYPAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := guessType(tc.syms); got != tc.want {
				t.Errorf("guessType(%v) = %q, want %q", tc.syms, got, tc.want)
			}
		})
	}
}

func TestParseGeneratedKeysymNames(t *testing.T) {
	for i := uint32(0); i < 12; i++ {
		name := fmt.Sprintf("F%d", i+1)
		if got, want := ParseKeysym(name), Keysym(0xffbe+i); got != want {
			t.Errorf("ParseKeysym(%q) = %#x, want %#x", name, got, want)
		}
	}
	if got, want := ParseKeysym("ISO_Level3_Latch"), Keysym(0xfe04); got != want {
		t.Errorf("ParseKeysym(ISO_Level3_Latch) = %#x, want %#x", got, want)
	}
}

func TestVoidSymbolRoundTripsGeneratedName(t *testing.T) {
	const want = Keysym(0xffffff)

	got := ParseKeysym("VoidSymbol")
	if got != want {
		t.Fatalf("ParseKeysym(VoidSymbol) = %#x, want %#x", got, want)
	}
	if name := got.Name(); name != "VoidSymbol" {
		t.Errorf("Keysym(%#x).Name() = %q, want VoidSymbol", got, name)
	}
}

func TestKeysymName(t *testing.T) {
	tests := []struct {
		keysym Keysym
		want   string
	}{
		{0, "NoSymbol"},
		{0xffbe, "F1"},
		{0xffc9, "F12"},
		{0x0101f642, "U1F642"},
		{0x00abcdef, "0x00abcdef"},
	}
	for _, tt := range tests {
		if got := tt.keysym.Name(); got != tt.want {
			t.Errorf("Keysym(%#x).Name() = %q, want %q", tt.keysym, got, tt.want)
		}
	}
}

func TestGeneratedLegacyRunes(t *testing.T) {
	tests := []struct {
		keysym Keysym
		want   rune
	}{
		{0x03bc, '\u0167'}, // tslash
		{0x07e1, '\u03b1'}, // Greek_alpha
		{0x06c1, '\u0430'}, // Cyrillic_a
		{0xff8d, '\r'},     // KP_Enter
		{0xffbe, -1},       // F1 is recognized but non-printable
	}
	for _, tt := range tests {
		if got := tt.keysym.Rune(); got != tt.want {
			t.Errorf("Keysym(%#x).Rune() = %U, want %U", tt.keysym, got, tt.want)
		}
	}
}

func TestGuessTypeUsesGeneratedLegacyCasePairs(t *testing.T) {
	if got := guessType([]Keysym{0x03bc, 0x03ac}); got != "ALPHABETIC" {
		t.Errorf("guessType(tslash, Tslash) = %q, want ALPHABETIC", got)
	}
}

func TestGuessTypeUsesAsymmetricUnicodeCasePair(t *testing.T) {
	syms := []Keysym{'s', 'S', 0x00df, 0x01001e9e}
	if got := guessType(syms); got != "FOUR_LEVEL_ALPHABETIC" {
		t.Errorf("guessType(s, S, ssharp, SSHARP) = %q, want FOUR_LEVEL_ALPHABETIC", got)
	}
}

// resolveVirtualMods must bind an interpret's virtualModifier only from the
// key's group 1, level 1 symbol, mirroring libxkbcommon. A real multi-group
// keymap serializes <RALT> as:
//
//	key <RALT> {
//		type= "ONE_LEVEL",
//		symbols[1]= [ 0xffea ],    // group 1: Alt_R
//		symbols[2]= [ 0xfe03 ]     // group 2: ISO_Level3_Shift
//	};
//
// with `modifier_map Mod1 { <RALT> }`. Scanning every group's every symbol
// finds 0xfe03 in group 2 and ORs Mod1 into the LevelThree virtual modifier
// mask (0x88 instead of 0x80), so the type's map[LevelThree] entry (built
// from the correct 0x80 mask) never matches Effective()&t.mods and level 3
// becomes unreachable -- this is exactly how AltGr broke on a real
// multi-layout keymap.
func TestVirtualModifierBindsFromGroup1Level1Only(t *testing.T) {
	const src = `
xkb_keycodes "t" {
	<RALT> = 100;
	<L3S> = 101;
	<AB01> = 102;
};
xkb_types "t" {
	type "LEVEL_THREE" {
		modifiers= LevelThree;
		map[LevelThree]= 2;
	};
};
xkb_compatibility "t" {
	interpret 0xfe03 { virtualModifier= LevelThree; };
};
xkb_symbols "t" {
	key <RALT> {
		type= "ONE_LEVEL",
		symbols[1]= [ 0xffea ],
		symbols[2]= [ 0xfe03 ]
	};
	key <L3S> { [ 0xfe03 ] };
	key <AB01> { type= "LEVEL_THREE", [ 0x61, 0x62 ] };
	modifier_map Mod1 { <RALT> };
	modifier_map Mod5 { <L3S> };
};
`
	km, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	st := km.NewState()

	st.UpdateMask(0, 0, 0, 0)
	if got, want := st.Sym(102), Keysym(0x61); got != want {
		t.Errorf("Sym(AB01, no mods) = %#x, want %#x", got, want)
	}

	st.UpdateMask(ModMod5, 0, 0, 0)
	if got, want := st.Sym(102), Keysym(0x62); got != want {
		t.Errorf("Sym(AB01, Mod5) = %#x, want %#x -- the LevelThree interpret must "+
			"bind only from <RALT>'s group 1, level 1 symbol (Alt_R), not group 2's "+
			"ISO_Level3_Shift, so <RALT>'s Mod1 modifier_map entry must not inflate "+
			"the LevelThree virtual modifier mask", got, want)
	}
}
