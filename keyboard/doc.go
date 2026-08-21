// Package keyboard compiles the subset of the XKB v1 keymap format that a
// Wayland client needs: xkb_keycodes, xkb_types, xkb_symbols and just
// enough of xkb_compatibility to resolve virtual modifiers.
//
// # Scope
//
// Out of scope, on purpose:
//   - Actions (SetMods, LockGroup, RedirectKey...): that's the compositor's
//     job, the client only needs to translate keycode+mods -> keysym.
//   - Compose / dead keys / input method — see [Composer] for that.
//   - Includes: the keymap delivered through wl_keyboard.keymap already
//     arrives resolved.
//   - Legacy non-Latin-1 keysyms (Greek, Cyrillic, Latin-2/3/4). See the
//     note on [Keysym.Rune].
//
// # Usage
//
//	km, err := keyboard.Compile(keymapString)
//	st := km.NewState()
//	st.UpdateMask(depressed, latched, locked, group) // from wl_keyboard.modifiers
//	sym := st.Sym(evdevKeycode + 8)                  // from wl_keyboard.key
package keyboard
