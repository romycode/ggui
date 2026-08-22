//go:build oracle

// This file compares xkbmini against the real libxkbcommon, via oracleRef
// (oracle_cgo.go). It requires cgo and libxkbcommon development headers
// (pkg-config xkbcommon), so it is excluded from the default build: run it
// with `go test -tags oracle`.
package keyboard

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// oracleLayouts mirrors the coverage promised in docs/keyboard.md: a plain
// Latin-1 layout, an accented one, one with non-default group/level
// structure, and one that leans on AltGr/dead keys.
var oracleLayouts = []struct{ layout, variant string }{
	{"us", ""},
	{"es", ""},
	{"es", "cat"},
	{"us", "intl"},
}

// The 8 real XKB modifiers have fixed indices; sweeping all 256
// combinations is cheap and removes any guesswork about which to hand-pick.
const maxRealMods = 256

func TestOracleAgainstLibxkbcommon(t *testing.T) {
	for _, l := range oracleLayouts {
		name := l.layout
		if l.variant != "" {
			name += "(" + l.variant + ")"
		}
		t.Run(name, func(t *testing.T) {
			runOracle(t, l.layout, l.variant)
		})
	}
}

func runOracle(t *testing.T, layout, variant string) {
	oracle, keymapText, err := newOracleRef(layout, variant)
	if err != nil {
		t.Fatalf("newOracleRef(%q, %q): %v", layout, variant, err)
	}
	defer oracle.Close()

	km, err := Compile(keymapText)
	if err != nil {
		t.Fatalf("xkbmini.Compile: %v", err)
	}
	st := km.NewState()

	const maxMismatches = 40
	symMismatches, consumedMismatches := 0, 0

	// Driven by the keymap libxkbcommon compiled, never by km.keys: a key
	// xkbmini dropped has to surface as a failure, not vanish from the
	// comparison.
	keycodes := oracle.Keycodes()
	var missing []string
	for _, kc := range keycodes {
		if _, ok := km.keys[kc]; !ok {
			missing = append(missing, fmt.Sprintf("%s(%d)", km.names[kc], kc))
		}
	}
	if len(missing) > 0 {
		t.Errorf("xkbmini.Compile dropped %d of %d keys the keymap defines: %s",
			len(missing), len(keycodes), strings.Join(missing, " "))
	}

	for _, kc := range keycodes {
		for g := 0; g < oracle.NumLayouts(kc); g++ {
			for mods := uint32(0); mods < maxRealMods; mods++ {
				st.UpdateMask(mods, 0, 0, uint32(g))

				gotSym := st.Sym(kc)
				wantSym := oracle.Sym(kc, mods, g)
				if gotSym != wantSym {
					symMismatches++
					if symMismatches <= maxMismatches {
						t.Errorf("Sym(keycode=%d %q) group=%d mods=%#x = %#x, want %#x (libxkbcommon)",
							kc, km.names[kc], g, mods, gotSym, wantSym)
					}
				}

				gotConsumed := st.Consumed(kc)
				wantConsumed := oracle.Consumed(kc, mods, g)
				if gotConsumed != wantConsumed {
					consumedMismatches++
					if consumedMismatches <= maxMismatches {
						t.Errorf("Consumed(keycode=%d %q) group=%d mods=%#x = %#x, want %#x (libxkbcommon)",
							kc, km.names[kc], g, mods, gotConsumed, wantConsumed)
					}
				}
			}
		}
	}
	if symMismatches > maxMismatches {
		t.Errorf("... and %d more Sym mismatches", symMismatches-maxMismatches)
	}
	if consumedMismatches > maxMismatches {
		t.Errorf("... and %d more Consumed mismatches", consumedMismatches-maxMismatches)
	}

	// The keysym universe comes out of the keymap itself, so the Rune
	// comparison isn't scoped by whatever xkbmini happened to resolve.
	var syms []Keysym
	seen := map[Keysym]bool{}
	for _, kc := range keycodes {
		for g := 0; g < oracle.NumLayouts(kc); g++ {
			for lvl := 0; lvl < oracle.NumLevels(kc, g); lvl++ {
				for _, s := range oracle.LevelSyms(kc, g, lvl) {
					if !seen[s] {
						seen[s] = true
						syms = append(syms, s)
					}
				}
			}
		}
	}
	slices.Sort(syms)

	runeMismatches := 0
	for _, sym := range syms {
		got := sym.Rune()
		want := oracleRune(sym)
		if got != want {
			runeMismatches++
			if runeMismatches <= maxMismatches {
				t.Errorf("Keysym(%#x).Rune() = %s, want %s (libxkbcommon)", uint32(sym), runeDesc(got), runeDesc(want))
			}
		}
	}
	if runeMismatches > maxMismatches {
		t.Errorf("... and %d more Rune mismatches", runeMismatches-maxMismatches)
	}
	t.Logf("mismatches: Sym=%d Consumed=%d Rune=%d",
		symMismatches, consumedMismatches, runeMismatches)
}

func runeDesc(r rune) string {
	if r < 0 {
		return "(none)"
	}
	return fmt.Sprintf("%q", r)
}
