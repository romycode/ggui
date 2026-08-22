package keyboard

import "golang.org/x/text/unicode/norm"

// Composer implements dead keys on top of the keysym stream that State.Sym
// returns. It sits above the keymap, not inside it: Sym resolves
// keycode+mods to a keysym, Composer resolves sequences of keysyms to text.
//
// It doesn't implement the Compose key (Multi_key) or the arbitrary
// sequences from /usr/share/X11/locale/*/Compose: only canonical Unicode
// composition, which is what covers accents and diaereses in ca/es/en.
type Composer struct {
	pending Keysym // dead key in progress, 0 if none
}

// Feed processes a keysym and returns the text to insert.
// It returns "" when the key was swallowed (a dead key is pending) or when
// the keysym isn't printable.
//
// Don't pass modifier keysyms (Shift_L, ISO_Level3_Shift...): filter out
// the 0xFFE1-0xFFEE range first, or a Shift press will cancel the pending
// accent.
func (c *Composer) Feed(k Keysym) string {
	_, isDead := deadMarks[k]

	if isDead {
		prev := c.pending
		c.pending = k
		if prev == 0 {
			return ""
		}
		// Two dead keys in a row: emit the first one as a spacing
		// character. If they're the same, that's the usual gesture for
		// typing the bare diacritic, so it also gets cancelled.
		if prev == k {
			c.pending = 0
		}
		return string(spacing(prev))
	}

	r := k.Rune()

	if c.pending == 0 {
		if r < 0 {
			return ""
		}
		return string(r)
	}

	prev := c.pending
	c.pending = 0
	pm := deadMarks[prev]

	switch {
	case r < 0:
		// Non-printable key (Return, arrows...): the sequence is
		// cancelled and the diacritic is released, same as GTK does.
		return string(spacing(prev))
	case r == ' ':
		return string(spacing(prev))
	}

	if composed, ok := composeRune(r, pm); ok {
		return string(composed)
	}
	// The sequence doesn't compose (e.g. dead_acute + 'k'): bare diacritic
	// followed by the base character.
	return string(spacing(prev)) + string(r)
}

// Reset discards any pending dead key. Call it on wl_keyboard.leave.
func (c *Composer) Reset() { c.pending = 0 }

// Pending reports whether an accent is waiting. Useful for the UI to show
// it underlined, the way toolkits do.
func (c *Composer) Pending() (Keysym, bool) { return c.pending, c.pending != 0 }

// composeRune attempts canonical composition of base + combining mark.
// NFC only composes what Unicode defines as canonical: that covers á é í ó
// ú à è ò ï ü ñ ç, i.e. everything Catalan and Spanish need.
// What it does NOT cover are the non-canonical entries from the Compose
// file (dead_stroke + o = ø, for example, which has no canonical
// decomposition).
func composeRune(base, mark rune) (rune, bool) {
	if mark == 0 {
		return 0, false
	}
	s := norm.NFC.String(string([]rune{base, mark}))
	rs := []rune(s)
	if len(rs) == 1 && rs[0] != base {
		return rs[0], true
	}
	return 0, false
}

func spacing(dead Keysym) rune {
	if r, ok := deadSpacing[dead]; ok {
		return r
	}
	return deadMarks[dead] // fallback: the bare combining mark
}

// The dead_* keysyms occupy 0xFE50-0xFE6E and each one maps to a Unicode
// combining mark. With this table and NFC you avoid needing the full
// (dead, base) -> precomposed pairs table.
var deadMarks = map[Keysym]rune{
	0xfe50: 0x0300, // dead_grave
	0xfe51: 0x0301, // dead_acute
	0xfe52: 0x0302, // dead_circumflex
	0xfe53: 0x0303, // dead_tilde
	0xfe54: 0x0304, // dead_macron
	0xfe55: 0x0306, // dead_breve
	0xfe56: 0x0307, // dead_abovedot
	0xfe57: 0x0308, // dead_diaeresis
	0xfe58: 0x030a, // dead_abovering
	0xfe59: 0x030b, // dead_doubleacute
	0xfe5a: 0x030c, // dead_caron
	0xfe5b: 0x0327, // dead_cedilla
	0xfe5c: 0x0328, // dead_ogonek
	0xfe5d: 0x0345, // dead_iota
	0xfe5e: 0x3099, // dead_voiced_sound
	0xfe5f: 0x309a, // dead_semivoiced_sound
	0xfe60: 0x0323, // dead_belowdot
	0xfe61: 0x0309, // dead_hook
	0xfe62: 0x031b, // dead_horn
	0xfe63: 0x0338, // dead_stroke
	0xfe64: 0x0313, // dead_abovecomma
	0xfe65: 0x0314, // dead_abovereversedcomma
	0xfe66: 0x030f, // dead_doublegrave
	0xfe67: 0x0325, // dead_belowring
	0xfe68: 0x0331, // dead_belowmacron
	0xfe69: 0x032d, // dead_belowcircumflex
	0xfe6a: 0x0330, // dead_belowtilde
	0xfe6b: 0x032e, // dead_belowbreve
	0xfe6c: 0x0324, // dead_belowdiaeresis
	0xfe6d: 0x0311, // dead_invertedbreve
	0xfe6e: 0x0326, // dead_belowcomma
}

// Spacing character emitted when the sequence is cancelled or closed with a
// space. The ones missing fall back to the combining mark, which renders
// oddly but doesn't come up in ca/es/en.
var deadSpacing = map[Keysym]rune{
	0xfe50: '`',
	0xfe51: 0x00b4, // ´
	0xfe52: '^',
	0xfe53: '~',
	0xfe54: 0x00af, // ¯
	0xfe55: 0x02d8, // ˘
	0xfe56: 0x02d9, // ˙
	0xfe57: 0x00a8, // ¨
	0xfe58: 0x02da, // ˚
	0xfe59: 0x02dd, // ˝
	0xfe5a: 0x02c7, // ˇ
	0xfe5b: 0x00b8, // ¸
	0xfe5c: 0x02db, // ˛
}
