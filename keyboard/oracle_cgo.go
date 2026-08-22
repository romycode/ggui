//go:build oracle

// oracleRef wraps libxkbcommon just enough to compare xkbmini's output
// against it. It requires cgo and libxkbcommon development headers
// (pkg-config xkbcommon), so it only builds with `go test -tags oracle`.
package keyboard

/*
#cgo pkg-config: xkbcommon
#include <stdlib.h>
#include <xkbcommon/xkbcommon.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

type oracleRef struct {
	ctx    *C.struct_xkb_context
	keymap *C.struct_xkb_keymap
	state  *C.struct_xkb_state
}

// newOracleRef compiles the given RMLVO with the real libxkbcommon and
// returns both the compiled keymap text (to feed into xkbmini.Compile, so
// both sides parse byte-identical input) and a live oracle to query.
func newOracleRef(layout, variant string) (ref *oracleRef, keymapText string, err error) {
	ctx := C.xkb_context_new(C.XKB_CONTEXT_NO_FLAGS)
	if ctx == nil {
		return nil, "", errors.New("xkb_context_new failed")
	}

	cLayout := C.CString(layout)
	defer C.free(unsafe.Pointer(cLayout))
	var cVariant *C.char
	if variant != "" {
		cVariant = C.CString(variant)
		defer C.free(unsafe.Pointer(cVariant))
	}
	rmlvo := C.struct_xkb_rule_names{layout: cLayout, variant: cVariant}

	keymap := C.xkb_keymap_new_from_names(ctx, &rmlvo, C.XKB_KEYMAP_COMPILE_NO_FLAGS)
	if keymap == nil {
		C.xkb_context_unref(ctx)
		return nil, "", errors.New("xkb_keymap_new_from_names failed")
	}

	cText := C.xkb_keymap_get_as_string(keymap, C.XKB_KEYMAP_FORMAT_TEXT_V1)
	if cText == nil {
		C.xkb_keymap_unref(keymap)
		C.xkb_context_unref(ctx)
		return nil, "", errors.New("xkb_keymap_get_as_string failed")
	}
	defer C.free(unsafe.Pointer(cText))
	keymapText = C.GoString(cText)

	state := C.xkb_state_new(keymap)
	if state == nil {
		C.xkb_keymap_unref(keymap)
		C.xkb_context_unref(ctx)
		return nil, "", errors.New("xkb_state_new failed")
	}

	return &oracleRef{ctx: ctx, keymap: keymap, state: state}, keymapText, nil
}

// newOracleRefFromKeymap compiles already-authored keymap text with the real
// libxkbcommon and returns a live oracle to query. Unlike newOracleRef (which
// builds a keymap from an RMLVO triple via xkb_keymap_new_from_names, and so
// can only ever produce single-group keymaps), this drives
// xkb_keymap_new_from_string, so it can load a captured real keymap with
// whatever group/level structure a live compositor actually produced —
// including multi-group ones RMLVO cannot express. The caller already has
// the keymap text (it's what's being loaded), so there is nothing to hand
// back beyond the oracle itself.
func newOracleRefFromKeymap(keymapText string) (ref *oracleRef, err error) {
	ctx := C.xkb_context_new(C.XKB_CONTEXT_NO_FLAGS)
	if ctx == nil {
		return nil, errors.New("xkb_context_new failed")
	}

	cText := C.CString(keymapText)
	defer C.free(unsafe.Pointer(cText))

	keymap := C.xkb_keymap_new_from_string(ctx, cText, C.XKB_KEYMAP_FORMAT_TEXT_V1, C.XKB_KEYMAP_COMPILE_NO_FLAGS)
	if keymap == nil {
		C.xkb_context_unref(ctx)
		return nil, errors.New("xkb_keymap_new_from_string failed")
	}

	state := C.xkb_state_new(keymap)
	if state == nil {
		C.xkb_keymap_unref(keymap)
		C.xkb_context_unref(ctx)
		return nil, errors.New("xkb_state_new failed")
	}

	return &oracleRef{ctx: ctx, keymap: keymap, state: state}, nil
}

func (o *oracleRef) Close() {
	C.xkb_state_unref(o.state)
	C.xkb_keymap_unref(o.keymap)
	C.xkb_context_unref(o.ctx)
}

// Keycodes returns every keycode the keymap actually defines, in ascending
// order. The sweep must be driven by this rather than by xkbmini's own key
// map: a key xkbmini fails to parse would otherwise be silently excluded
// from the comparison instead of reported as a mismatch.
func (o *oracleRef) Keycodes() []uint32 {
	var out []uint32
	lo := uint32(C.xkb_keymap_min_keycode(o.keymap))
	hi := uint32(C.xkb_keymap_max_keycode(o.keymap))
	for kc := lo; kc <= hi; kc++ {
		if o.NumLayouts(kc) > 0 {
			out = append(out, kc)
		}
	}
	return out
}

// NumLayouts is the keymap's own group count for a key, which is what
// bounds a valid group index.
func (o *oracleRef) NumLayouts(keycode uint32) int {
	return int(C.xkb_keymap_num_layouts_for_key(o.keymap, C.xkb_keycode_t(keycode)))
}

// LevelSyms returns the keysyms the keymap defines for one key/layout/level,
// straight out of the keymap rather than through a state. Used to enumerate
// the keysym universe for the Rune comparison authoritatively.
func (o *oracleRef) LevelSyms(keycode uint32, layout, level int) []Keysym {
	var syms *C.xkb_keysym_t
	n := C.xkb_keymap_key_get_syms_by_level(o.keymap, C.xkb_keycode_t(keycode),
		C.xkb_layout_index_t(layout), C.xkb_level_index_t(level), &syms)
	if n == 0 || syms == nil {
		return nil
	}
	out := make([]Keysym, 0, int(n))
	for _, s := range unsafe.Slice(syms, int(n)) {
		out = append(out, Keysym(s))
	}
	return out
}

// NumLevels is the number of shift levels for a key/layout.
func (o *oracleRef) NumLevels(keycode uint32, layout int) int {
	return int(C.xkb_keymap_num_levels_for_key(o.keymap, C.xkb_keycode_t(keycode),
		C.xkb_layout_index_t(layout)))
}

// set drives the reference state exactly the way State.UpdateMask drives
// xkbmini's, so the two are always queried in the same configuration.
func (o *oracleRef) set(mods uint32, group int) {
	C.xkb_state_update_mask(o.state, C.xkb_mod_mask_t(mods), 0, 0, 0, 0, C.xkb_layout_index_t(group))
}

func (o *oracleRef) Sym(keycode uint32, mods uint32, group int) Keysym {
	o.set(mods, group)
	return Keysym(C.xkb_state_key_get_one_sym(o.state, C.xkb_keycode_t(keycode)))
}

func (o *oracleRef) Consumed(keycode uint32, mods uint32, group int) uint32 {
	o.set(mods, group)
	return uint32(C.xkb_state_key_get_consumed_mods2(o.state, C.xkb_keycode_t(keycode), C.XKB_CONSUMED_MODE_XKB))
}

// Rune returns -1 when libxkbcommon reports no codepoint, matching
// Keysym.Rune's convention.
func oracleRune(k Keysym) rune {
	r := rune(C.xkb_keysym_to_utf32(C.xkb_keysym_t(k)))
	if r == 0 {
		return -1
	}
	return r
}

// oracleToUpper wraps xkb_keysym_to_upper, the function xkb_state_key_get_one_sym
// applies when Lock is effective and not consumed, and which Keysym.ToUpper
// is meant to mirror.
func oracleToUpper(k Keysym) Keysym {
	return Keysym(C.xkb_keysym_to_upper(C.xkb_keysym_t(k)))
}
