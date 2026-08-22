package keyboard

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Real modifiers. Indices 0-7 are fixed in XKB: this is what travels in
// wl_keyboard.modifiers masks.
const (
	ModShift uint32 = 1 << 0
	ModLock  uint32 = 1 << 1 // Caps Lock
	ModCtrl  uint32 = 1 << 2
	ModMod1  uint32 = 1 << 3 // typically Alt
	ModMod2  uint32 = 1 << 4 // typically NumLock
	ModMod3  uint32 = 1 << 5
	ModMod4  uint32 = 1 << 6 // typically Super/Logo
	ModMod5  uint32 = 1 << 7 // typically AltGr / LevelThree
)

var realMods = map[string]uint32{
	"Shift": ModShift, "Lock": ModLock, "Control": ModCtrl,
	"Mod1": ModMod1, "Mod2": ModMod2, "Mod3": ModMod3,
	"Mod4": ModMod4, "Mod5": ModMod5,
	"none": 0, "None": 0,
}

type Keysym uint32

// ------------------------------------------------------------------ structures

type keyType struct {
	name        string
	modsRaw     []string          // unresolved: may contain virtuals
	mods        uint32            // effective mask after resolving virtuals
	mapRaw      map[string]int    // "Shift+LevelThree" -> level (0-based)
	levels      map[uint32]int    // mask -> level
	preserve    map[uint32]uint32 // mask -> preserved mods (not consumed)
	preserveRaw map[string]string // normalized "Lock+LevelThree" -> raw "Lock"
}

type key struct {
	types  []string   // type name per group
	groups [][]Keysym // groups[group][level]
	repeat bool
}

type Keymap struct {
	keycodes map[string]uint32 // "<AD01>" -> 24 (XKB keycode = evdev+8)
	names    map[uint32]string // inverse
	types    map[string]*keyType
	keys     map[uint32]*key
	modMap   map[uint32]uint32 // keycode -> real modifier mask
	vmods    map[string]uint32 // "LevelThree" -> ModMod5, etc.
}

type State struct {
	km                         *Keymap
	depressed, latched, locked uint32
	group                      uint32
}

// --------------------------------------------------------------------- compile

var (
	reSection  = regexp.MustCompile(`(?s)xkb_(keycodes|types|compat\w*|symbols|geometry)\b[^{]*\{(.*?)\n\};`)
	reKeycode  = regexp.MustCompile(`(?m)^\s*(<[^>]+>)\s*=\s*(\d+)\s*;`)
	reAlias    = regexp.MustCompile(`(?m)^\s*alias\s+(<[^>]+>)\s*=\s*(<[^>]+>)\s*;`)
	reType     = regexp.MustCompile(`(?s)type\s+"([^"]+)"\s*\{(.*?)\}\s*;`)
	reTypeMods = regexp.MustCompile(`modifiers\s*=\s*([^;]+);`)
	reTypeMap  = regexp.MustCompile(`map\[([^\]]+)\]\s*=\s*(?:[Ll]evel)?(\d+)\s*;`)
	rePreserve = regexp.MustCompile(`preserve\[([^\]]+)\]\s*=\s*([^;]+);`)
	// (?s) matters: libxkbcommon writes keys that carry an explicit type as
	// multi-line blocks, and without it F1-F12, the keypad operators,
	// PrintScreen and Pause are silently dropped.
	reKey     = regexp.MustCompile(`(?sm)^\s*key\s+(<[^>]+>)\s*\{(.*?)\}\s*;`)
	reActions = regexp.MustCompile(`actions\s*(\[[^\]]*\])?\s*=\s*\[[^\]]*\]\s*,?`)
	// Drops the group index in `symbols[1]= [...]` so reSymList doesn't read
	// the "[1]" as a one-symbol group of its own.
	reSymIndex  = regexp.MustCompile(`symbols\s*\[[^\]]*\]\s*=`)
	reSymList   = regexp.MustCompile(`\[([^\]]*)\]`)
	reKeyType   = regexp.MustCompile(`type\s*(\[\s*[Gg]roup(\d+)\s*\])?\s*=\s*"([^"]+)"`)
	reModMap    = regexp.MustCompile(`(?m)^\s*modifier_map\s+(\w+)\s*\{([^}]*)\}\s*;`)
	reInterpret = regexp.MustCompile(`(?s)interpret\s+([A-Za-z0-9_]+)[^{]*\{(.*?)\}\s*;`)
	reVMod      = regexp.MustCompile(`virtualModifier\s*=\s*(\w+)\s*;`)
	reUnicodeKS = regexp.MustCompile(`^U([0-9A-Fa-f]{4,6})$`)
)

func Compile(src string) (*Keymap, error) {
	src = stripComments(src)

	km := &Keymap{
		keycodes: map[string]uint32{},
		names:    map[uint32]string{},
		types:    map[string]*keyType{},
		keys:     map[uint32]*key{},
		modMap:   map[uint32]uint32{},
		vmods:    map[string]uint32{},
	}

	sections := map[string]string{}
	for _, m := range reSection.FindAllStringSubmatch(src, -1) {
		sections[m[1]] = m[2]
	}
	if sections["keycodes"] == "" || sections["symbols"] == "" {
		return nil, fmt.Errorf("keyboard: keymap missing xkb_keycodes or xkb_symbols")
	}

	km.parseKeycodes(sections["keycodes"])
	km.parseTypes(sections["types"])
	km.parseSymbols(sections["symbols"])

	// Virtual modifiers depend on compat + modifier_map + symbols, so they
	// are resolved last, and only then are the type masks fixed.
	for name, sec := range sections {
		if strings.HasPrefix(name, "compat") {
			km.resolveVirtualMods(sec)
		}
	}
	km.resolveTypeMasks()
	return km, nil
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func (km *Keymap) parseKeycodes(sec string) {
	for _, m := range reKeycode.FindAllStringSubmatch(sec, -1) {
		n, err := strconv.ParseUint(m[2], 10, 32)
		if err != nil {
			continue
		}
		km.keycodes[m[1]] = uint32(n)
		km.names[uint32(n)] = m[1]
	}
	for _, m := range reAlias.FindAllStringSubmatch(sec, -1) {
		if kc, ok := km.keycodes[m[2]]; ok {
			km.keycodes[m[1]] = kc
		}
	}
}

func (km *Keymap) parseTypes(sec string) {
	for _, m := range reType.FindAllStringSubmatch(sec, -1) {
		t := &keyType{
			name:        m[1],
			mapRaw:      map[string]int{},
			levels:      map[uint32]int{},
			preserve:    map[uint32]uint32{},
			preserveRaw: map[string]string{},
		}
		body := m[2]
		if mm := reTypeMods.FindStringSubmatch(body); mm != nil {
			t.modsRaw = splitMods(mm[1])
		}
		for _, e := range reTypeMap.FindAllStringSubmatch(body, -1) {
			lvl, _ := strconv.Atoi(e[2])
			t.mapRaw[normalizeMods(e[1])] = lvl - 1
		}
		for _, e := range rePreserve.FindAllStringSubmatch(body, -1) {
			// Both sides may name virtual modifiers, so the masks are resolved
			// in resolveTypeMasks once the compat section has been read.
			t.preserveRaw[normalizeMods(e[1])] = e[2]
		}
		km.types[t.name] = t
	}
	// Canonical types in case the keymap doesn't declare them (rare, but
	// it happens).
	if _, ok := km.types["ONE_LEVEL"]; !ok {
		km.types["ONE_LEVEL"] = &keyType{
			name: "ONE_LEVEL", levels: map[uint32]int{0: 0},
			mapRaw: map[string]int{}, preserve: map[uint32]uint32{},
			preserveRaw: map[string]string{},
		}
	}
}

func (km *Keymap) parseSymbols(sec string) {
	for _, m := range reKey.FindAllStringSubmatch(sec, -1) {
		kc, ok := km.keycodes[m[1]]
		if !ok {
			continue
		}
		body := reActions.ReplaceAllString(m[2], "") // actions aren't our concern
		body = reSymIndex.ReplaceAllString(body, "symbols=")
		k := &key{repeat: !strings.Contains(body, "repeat= no")}

		for _, t := range reKeyType.FindAllStringSubmatch(body, -1) {
			g := 1
			if t[2] != "" {
				g, _ = strconv.Atoi(t[2])
			}
			for len(k.types) < g {
				k.types = append(k.types, "")
			}
			k.types[g-1] = t[3]
		}

		for _, l := range reSymList.FindAllStringSubmatch(body, -1) {
			var syms []Keysym
			for _, name := range strings.Split(l[1], ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				syms = append(syms, ParseKeysym(name))
			}
			k.groups = append(k.groups, syms)
		}
		if len(k.groups) == 0 {
			continue
		}
		for gi := range k.groups {
			if gi >= len(k.types) {
				k.types = append(k.types, "")
			}
			if k.types[gi] == "" {
				k.types[gi] = guessType(k.groups[gi])
			}
		}
		km.keys[kc] = k
	}

	for _, m := range reModMap.FindAllStringSubmatch(sec, -1) {
		mask, ok := realMods[m[1]]
		if !ok {
			continue // modifier_map to a virtual: resolved via compat
		}
		for _, name := range strings.Split(m[2], ",") {
			name = strings.TrimSpace(name)
			if kc, ok := km.keycodes[name]; ok {
				km.modMap[kc] |= mask
			}
		}
	}
}

// resolveVirtualMods derives the real encoding of each virtual modifier:
// for every `interpret <sym> { virtualModifier= X; }`, it finds the keys
// whose symbol is <sym> and ORs in their modifier_map mask.
// Simplification: useModMapMods/level and AnyOf(...) conditions are ignored.
func (km *Keymap) resolveVirtualMods(sec string) {
	for _, m := range reInterpret.FindAllStringSubmatch(sec, -1) {
		vm := reVMod.FindStringSubmatch(m[2])
		if vm == nil {
			continue
		}
		want := ParseKeysym(m[1])
		if want == 0 && !isResolvedZeroKeysym(m[1]) {
			continue
		}
		for kc, k := range km.keys {
			mask, ok := km.modMap[kc]
			if !ok {
				continue
			}
			// libxkbcommon binds an interpret's virtualModifier only from the
			// key's group 1, level 1 symbol. A key can carry an unrelated
			// symbol in group 1 and the interpret's symbol only in a later
			// group (e.g. <RALT> = [ Alt_R ] in group 1, [ ISO_Level3_Shift ]
			// in group 2 on a two-layout keymap); matching any group/symbol
			// would then wrongly fold that key's modifier_map mask into the
			// virtual modifier, inflating it beyond what the type expects.
			if len(k.groups) == 0 || len(k.groups[0]) == 0 {
				continue
			}
			if k.groups[0][0] == want {
				km.vmods[vm[1]] |= mask
			}
		}
	}
	// Common fallbacks if compat didn't provide anything.
	for name, def := range map[string]uint32{
		"LevelThree": ModMod5, "Alt": ModMod1, "NumLock": ModMod2, "Super": ModMod4,
	} {
		if km.vmods[name] == 0 {
			km.vmods[name] = def
		}
	}
}

// isResolvedZeroKeysym reports whether name deliberately resolves to zero.
func isResolvedZeroKeysym(name string) bool {
	switch name {
	case "NoSymbol":
		return true
	}
	if strings.HasPrefix(name, "0x") {
		value, err := strconv.ParseUint(name[2:], 16, 32)
		return err == nil && value == 0
	}
	value, ok := keysymNames[name]
	return ok && value == 0
}

func (km *Keymap) resolveTypeMasks() {
	for _, t := range km.types {
		for _, n := range t.modsRaw {
			t.mods |= km.modMask(n)
		}
		for raw, lvl := range t.mapRaw {
			var mask uint32
			for _, n := range splitMods(raw) {
				mask |= km.modMask(n)
			}
			t.levels[mask] = lvl
		}
		for raw, val := range t.preserveRaw {
			var lhs, rhs uint32
			for _, n := range splitMods(raw) {
				lhs |= km.modMask(n)
			}
			for _, n := range splitMods(val) {
				rhs |= km.modMask(n)
			}
			t.preserve[lhs] = rhs
		}
	}
}

func (km *Keymap) modMask(name string) uint32 {
	if m, ok := realMods[name]; ok {
		return m
	}
	return km.vmods[name] // virtual; 0 if unknown
}

func splitMods(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "+") {
		p = strings.TrimSpace(p)
		if p != "" && p != "none" && p != "None" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeMods(s string) string {
	parts := splitMods(s)
	return strings.Join(parts, "+")
}

// guessType mirrors libxkbcommon's FindAutomaticType, used when a key
// doesn't declare a type of its own.
//
// The decisive test is on the base pair (levels 1/2): the AltGr pair is
// consulted only when the base pair is an ordered lower/upper case pair.
// Deciding from the AltGr pair instead makes Caps Lock alter keys it must
// leave alone — on us(intl) it turns "," into "<".
//
// Keys wider than 4 symbols have no automatic type in XKB; the caller
// leaves those alone rather than guessing.
func guessType(syms []Keysym) string {
	switch {
	case len(syms) <= 1:
		return "ONE_LEVEL"
	case len(syms) == 2:
		if isCasePair(syms[0], syms[1]) {
			return "ALPHABETIC"
		}
		if isKeypad(syms[0]) || isKeypad(syms[1]) {
			return "KEYPAD"
		}
		return "TWO_LEVEL"
	case len(syms) <= 4:
		if isCasePair(syms[0], syms[1]) {
			if len(syms) == 4 && isCasePair(syms[2], syms[3]) {
				return "FOUR_LEVEL_ALPHABETIC"
			}
			// Lock still selects level 2, but must be preserved (not
			// consumed) at the AltGr levels so it doesn't flip symbols
			// that have no case.
			return "FOUR_LEVEL_SEMIALPHABETIC"
		}
		if isKeypad(syms[0]) || isKeypad(syms[1]) {
			return "FOUR_LEVEL_KEYPAD"
		}
		return "FOUR_LEVEL"
	default:
		return ""
	}
}

// isCasePair reports whether lo/up are the lower- and upper-case forms of
// one letter, in that order. This is the library's predicate: "both are
// letters" is not equivalent, and wrongly accepts pairs like [mu, masculine]
// or [a, a].
func isCasePair(lo, up Keysym) bool {
	l, u := lo.Rune(), up.Rune()
	if l < 0 || u < 0 {
		return false
	}
	return unicode.IsLower(l) && unicode.IsUpper(u) &&
		(unicode.ToUpper(l) == u || unicode.ToLower(u) == l)
}

// isKeypad mirrors X11's IsKeypadKey range: KP_Space (0xff80) through
// KP_Equal (0xffbd).
func isKeypad(k Keysym) bool {
	return k >= 0xff80 && k <= 0xffbd
}

// ----------------------------------------------------------------------- state

func (km *Keymap) NewState() *State { return &State{km: km} }

// UpdateMask is called as-is from wl_keyboard.modifiers.
func (s *State) UpdateMask(depressed, latched, locked, group uint32) {
	s.depressed, s.latched, s.locked, s.group = depressed, latched, locked, group
}

func (s *State) Effective() uint32 { return s.depressed | s.latched | s.locked }

// Sym translates an XKB keycode (= wl_keyboard.key's keycode + 8) to the
// effective keysym for the current group and level.
func (s *State) Sym(keycode uint32) Keysym {
	k, ok := s.km.keys[keycode]
	if !ok || len(k.groups) == 0 {
		return 0
	}
	g := int(s.group) % len(k.groups) // XKB wraps the group by default
	syms := k.groups[g]
	lvl := s.level(k, g)
	if lvl >= len(syms) {
		lvl = 0
	}
	if len(syms) == 0 {
		return 0
	}
	return syms[lvl]
}

func (s *State) level(k *key, g int) int {
	t := s.km.types[k.types[g]]
	if t == nil {
		return 0
	}
	masked := s.Effective() & t.mods
	if lvl, ok := t.levels[masked]; ok {
		return lvl
	}
	return 0
}

// Consumed reports the modifiers the key's type "spent" choosing the
// level. To match shortcuts, compare against Effective() &^ Consumed():
// otherwise Shift+2 never matches "at", and Ctrl+Shift+X behaves
// differently than expected on non-US layouts.
func (s *State) Consumed(keycode uint32) uint32 {
	k, ok := s.km.keys[keycode]
	if !ok || len(k.groups) == 0 {
		return 0
	}
	g := int(s.group) % len(k.groups)
	t := s.km.types[k.types[g]]
	if t == nil {
		return 0
	}
	masked := s.Effective() & t.mods
	return t.mods &^ t.preserve[masked]
}

func (s *State) Repeats(keycode uint32) bool {
	k, ok := s.km.keys[keycode]
	return ok && k.repeat
}

// -------------------------------------------------------------------- keysyms

// ParseKeysym resolves a keysym name as it appears in xkb_symbols. Symbolic
// names are resolved through the complete generated XKB keysym table.
func ParseKeysym(name string) Keysym {
	switch {
	case name == "" || name == "NoSymbol":
		return 0
	case strings.HasPrefix(name, "0x"):
		v, _ := strconv.ParseUint(name[2:], 16, 32)
		return Keysym(v)
	case len(name) == 1 && name[0] >= 0x20 && name[0] <= 0x7e:
		// In printable ASCII the keysym matches the code point.
		return Keysym(name[0])
	}
	if m := reUnicodeKS.FindStringSubmatch(name); m != nil {
		v, _ := strconv.ParseUint(m[1], 16, 32)
		return Keysym(0x01000000 | v)
	}
	return keysymNames[name] // generated table
}

// Rune returns the keysym's code point, or -1 if it isn't printable. Legacy
// non-Latin-1 keysyms are resolved through the complete generated table.
func (k Keysym) Rune() rune {
	switch {
	case k&0xff000000 == 0x01000000:
		return rune(k & 0x00ffffff)
	case k >= 0x20 && k <= 0x7e, k >= 0xa0 && k <= 0xff:
		return rune(k)
	}
	if r, ok := legacyRunes[k]; ok {
		return r
	}
	return -1
}

// Name returns the canonical XKB name for k. Unnamed Unicode keysyms use
// Uxxxx notation; other unknown values use zero-padded hexadecimal.
func (k Keysym) Name() string {
	if name, ok := keysymCanonicalNames[k]; ok {
		return name
	}
	if k&0xff000000 == 0x01000000 {
		return fmt.Sprintf("U%04X", uint32(k&0x00ffffff))
	}
	return fmt.Sprintf("0x%08x", uint32(k))
}
