package keyboard

import "testing"

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
