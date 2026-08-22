### Task 2: sweep captured real keymaps

- [ ] Add `xkb_keymap_new_from_string` to `oracle_cgo.go` so a keymap can come from a file rather than an RMLVO triple.
- [ ] Check in the captured multi-group keymap under `keyboard/testdata/` and sweep it exactly as the RMLVO layouts are swept — every keycode × group × 256 modifier combinations, `Sym` / `Consumed` / `Rune`.
- [ ] Drive the fixture sweep from the same helper as the RMLVO sweep so the two cannot drift.

`example/keylog` gained `KEYLOG_DUMP_KEYMAP=<path>` to capture these fixtures; that is how the first one was obtained.

**Success:** the fixture sweep runs in CI alongside the synthetic one, and a regression in multi-group virtual-modifier resolution fails the suite instead of reaching a user.

---

## Outcome

All three phases landed. Verified state:

- `Consumed` and `Rune`: **0 mismatches on all four layouts**.
- `TestGeneratedRunesAgainstLibxkbcommon` compares **2,505** generated keysyms against `xkb_keysym_to_utf32` — clean.
- The name path resolves correctly (`LevelThree=0x80`, `NumLock=0x10`, `ParseKeysym("ISO_Level3_Latch")=0xfe04`), guarded by dedicated regressions.
- `example/keylog` logs resolved keys against a live compositor and doubles as a prototype of the pending `Keyboard` lifecycle layer.

**The one remaining oracle gap** is `Sym`: 128 / 128 / 96 on `es` / `es(cat)` / `us(intl)`, `us` clean. Attributed by measurement, not assumption — classifying every residual mismatch on `es` gives **128 capitalization-shaped, 0 other**. It is entirely the transformation `xkb_state_key_get_one_sym` applies when Lock is effective and *not* consumed (`mu`→`Greek_MU`, `ccedilla`→`Ccedilla`, `ssharp`→`SSHARP`).

## Out of scope

Recorded in `docs/keyboard.md` as separate pending work:

- The **capitalization transform** in `State.Sym` — the last oracle gap, and the natural next plan.
- `Composer` tests. An xkbcommon oracle is meaningless here: it implements canonical NFC deliberately, not X11's Compose file, so it needs its own suite of known ca/es/en sequences.
- The `Keyboard` lifecycle type, `Event`/`Mods`, the repeat timer, and the `input/keyboard` + `input/xkbmini` split.

## SDD records

This plan was executed as three separate runs before being consolidated. Their ledgers, briefs, reports and review diffs are under `.superpowers/sdd/`:

| run | ledger |
| --- | --- |
| `preserve[]` | `.superpowers/sdd/2026-08-22-preserve-implementation/` |
| named virtual modifiers | `.superpowers/sdd/2026-08-22-named-keymap-vmods/` |
| keysym generator | `.superpowers/sdd/2026-08-22-keysym-generator/` |
