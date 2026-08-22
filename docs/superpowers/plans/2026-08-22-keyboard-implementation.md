# `keyboard` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `keyboard`'s XKB keymap compiler agree with the real `libxkbcommon`, proven by a differential oracle rather than by inspection. Starting point: the compiler resolved keycode+modifiers to keysyms but disagreed with the library on thousands of comparisons per layout. Ending point: `Consumed` and `Rune` agree exactly on every tested layout, with the single remaining `Sym` gap identified, measured and attributed.

**Architecture:** Three phases, executed in this order because each one's measurement exposed the next. No phase was planned before its predecessor's numbers came in — that is the point of the method, not a defect in it.

1. **`preserve[]`** — the XKB directive marking modifiers as *not consumed*, parsed and then discarded. Confined to `keyboard/xkbmini.go`.
2. **Name-serialized virtual modifiers** — unresolved interpret keysyms comparing equal to `NoSymbol` and corrupting `LevelThree`/`NumLock`. Confined to `keyboard/xkbmini.go`.
3. **`keysymgen`** — a standalone offline generator producing complete keysym tables from a vendored libxkbcommon header, replacing the hand-seeded stubs. New `cmd/keysymgen` subsystem, independent of `cmd/waygenerator`.

**Tech Stack:** Go 1.27, stdlib only in shipped code. The `oracle` build tag (cgo + libxkbcommon) is verification-only and never enters the default build.

**Spec:** `docs/keyboard.md` — the package's reference document, authoritative for semantics. Its "Modificadores consumidos" section defines the rule the whole plan serves: modifiers a key's type spent selecting the level are not part of a shortcut match, so the correct comparison is `Effective &^ Consumed`. Phase 3 additionally has a frozen design snapshot at `.superpowers/specs/2026-08-22-keysym-generator-design.md`.

## Global Constraints

- Module `github.com/romycode/ggui`, Go 1.27.0.
- **Shipped code is pure Go.** The only cgo is `keyboard/oracle_cgo.go` behind the `oracle` build tag. No phase may introduce cgo into the default build.
- Code and code comments in **English (EN_US)**; everything under `docs/` in **Spanish (ES)**. This is `CLAUDE.md` and it is binding.
- **`State.Consumed`'s formula is correct and does not change.** `t.mods &^ t.preserve[masked]` was right from the start; only its inputs were wrong.
- **`km.modMask` is the sole name→mask resolution path.** Never add a second.
- Unknown interpret keysyms **fail closed**: they must never compare equal to a parsed `NoSymbol` merely because both are zero.
- Every phase leaves `go build ./...`, `go vet ./...`, `go test ./...` and `go vet -tags oracle ./keyboard/...` green, and `gofmt -l` silent.
- Strict TDD throughout: record RED for the right reason before touching production code, then GREEN.

## The measurement that governs everything

```sh
go test -tags oracle ./keyboard/...
```

Layouts `us`, `es`, `es(cat)`, `us(intl)`, against libxkbcommon **1.13.2** (the version matters — `xkb_state_key_get_one_sym` semantics changed in 1.9.0). Both sides are fed byte-identical keymap text from `xkb_keymap_get_as_string`, and the sweep is driven by the **library's** keycode list, never `km.keys`, so a key the compiler drops surfaces as a failure instead of silently leaving the comparison.

| phase | | `us` | `es` | `es(cat)` | `us(intl)` |
| --- | --- | --- | --- | --- | --- |
| start | Sym / Consumed / Rune | 0 / 3200 / 29 | 448 / 4416 / 52 | 384 / 4416 / 50 | 160 / 3520 / 32 |
| after 1 | Sym / Consumed / Rune | 0 / **0** / 29 | 448 / 320 / 52 | 384 / 256 / 50 | 160 / 64 / 32 |
| after 3 | Sym / Consumed / Rune | 0 / **0** / **0** | 128 / **0** / **0** | 128 / **0** / **0** | 96 / **0** / **0** |

---

## Phase 1: implement `preserve[]`

**Files:** `keyboard/xkbmini.go`, `keyboard/xkbmini_test.go`

`parseTypes` matched `preserve[...]= ...;` and then discarded the value (`_ = e[2]`), storing only the left-hand side behind a `"preserve:"` prefix inside `mapRaw` — a map whose value type is a level `int`, with nowhere to put a mask. `resolveTypeMasks` then skipped every such key. Net effect: `keyType.preserve` was always empty and `Consumed` always returned the full `t.mods`.

Resolution cannot happen at parse time: both sides may name **virtual** modifiers (`preserve[Lock+LevelThree]= Lock`, `preserve[Control+Alt+LevelThree]= Control+Alt`), which are unknown until `resolveVirtualMods` has read the compat section.

- [ ] RED: three tests — real-modifier preserve, virtual-modifier preserve (needs an `xkb_compatibility` block or the `LevelThree`→`Mod5` fallback never fires), and `preserve[X]= none`.
- [ ] Add `preserveRaw map[string]string` to `keyType`, initialized at **both** construction sites in `parseTypes` (the loop body *and* the synthetic `ONE_LEVEL` fallback — a nil map there is a latent panic).
- [ ] `parseTypes` keeps the right-hand side instead of discarding it.
- [ ] `resolveTypeMasks` resolves both sides through `km.modMask` into `t.preserve`; delete the now-dead `"preserve:"` skip.

**Success:** `Consumed` reaches 0 on `us`. `Sym` and `Rune` unchanged — this phase must not touch level selection, so movement in either direction is a red flag, not a bonus.

### Correction to this phase's original acceptance criterion

The criterion first written here demanded `Consumed` reach 0 on **all four** layouts, on the stated grounds that `preserve[]` accounted for 100% of the mismatches. **That was wrong, and the error is instructive.**

It was derived from observing that every `Consumed` mismatch satisfied `want ⊆ got` and `got == type.mods`. That signature is consistent with `preserve[]` being unimplemented — but *equally* consistent with the key's **type being misselected**, since a wrong type carries a wrong `mods` mask. The two causes were conflated, and a necessary condition was mistaken for a sufficient one.

Phases 2 and 3 exist because of what the residual actually was.

---

## Phase 2: fail closed on unresolved named interprets

**Files:** `keyboard/xkbmini.go`, `keyboard/xkbmini_test.go`

`xkb_keymap_get_as_string` serializes keysyms as **hex**; `xkbcli compile-keymap` serializes them as **names**. `ParseKeysym` short-circuits on the `0x` prefix, so the hex path never consults `keysymNames` at all. On the **name** path, a name missing from the hand-seeded table (`ISO_Level3_Latch`, `ISO_Level3_Lock`) made `ParseKeysym` return `0`; `resolveVirtualMods`' `if s == want` then matched every explicit `NoSymbol` (also `0`) on any modifier-mapped key, OR-ing unrelated real modifiers into the virtual one.

Measured on the same `es` layout, same code, two serializations:

| serialization | `LevelThree` | `NumLock` | `FOUR_LEVEL_SEMIALPHABETIC.mods` |
| --- | --- | --- | --- |
| hex (oracle) | `0x80` correct | `0x10` correct | `0x83` correct |
| names (`xkbcli`) | `0xf8` **wrong** | `0x78` **wrong** | `0xfb` **wrong** |

**The oracle was structurally incapable of catching this**, because it only ever feeds hex. That is not merely a coverage gap — it hid a live correctness bug against any compositor sending a name-serialized keymap.

- [ ] RED: a name-serialized regression keymap with working `ISO_Level3_Shift`, unresolved `ISO_Level3_Latch`/`ISO_Level3_Lock`/`Num_Lock`, and unrelated modifier-mapped keys carrying explicit `NoSymbol`. Assert through public behavior (`Compile` → `UpdateMask` → `Sym`), not private fields.
- [ ] GREEN: `resolveVirtualMods` ignores an interpret whose symbolic name could not be resolved, instead of treating zero as a wildcard.
- [ ] Preserve deliberate zero-valued symbol handling — `VoidSymbol` and a numeric `0x0` are *not* the same as an unresolvable name, and each needs its own regression.
- [ ] Hex oracle counts must not move.

**Success:** named keymaps resolve `LevelThree=Mod5` and `NumLock=Mod2` exactly; hex measurements unchanged.

---

## Phase 3: `keysymgen` — generate the complete tables

**Files:** new `cmd/keysymgen/`, new `cmd/keysymgen/internal/keysymdata/`, generated `keyboard/keysyms.gen.go`, plus `keyboard/xkbmini.go` to consume it.

The hand-seeded `keysymNames` (~20 entries) and `legacyRunes` (~10 entries) were always placeholders. They are the root of both remaining failure modes: `Rune()` returning `-1` for legacy keysyms feeds `isCasePair`, which feeds `guessType`, which picks the wrong automatic type — so a table gap became a *type-selection* bug, visible in `Consumed` and `Sym`, not just in `Rune`.

Vendor libxkbcommon 1.13.2's public keysym header as the single immutable input. Parse it strictly, render deterministic Go, emit atomically. Independent of `cmd/waygenerator`.

- [ ] Task 1: vendor the header; parse and validate it strictly, including aliases, deprecated aliases, and the several Unicode-annotation comment forms (`/* U+0041 ... */`, `/*<U+000D ...>*/`, `/*(U+250C ...)*/`, and forms preceded by version metadata).
- [ ] Task 2: render deterministic Go and add the standalone CLI.
- [ ] Task 3: replace the hand-written seeds; expose F-key and canonical names.
- [ ] Task 4: measure the oracle and update `docs/keyboard.md`.

Two cases worth calling out because they cost real debugging time:

- `guessType` needs the Unicode **upper→lower** conversion as well as lower→upper: simple inverse conversion does not turn `ß` into `ẞ`, so the `ssharp`/`SSHARP` pair silently selected the wrong type.
- Unicode annotations preceded by version metadata must still parse, or `XF86Numeric0`–`9`, `XF86NumericStar` and `XF86NumericPound` are dropped.

**Success:** `Consumed` and `Rune` reach **0 on all four layouts**; all generated keysyms verified against `xkb_keysym_to_utf32`.

---

---

## Phase 4: bind virtual modifiers from group 1 only, and sweep real keymaps

**Files:** `keyboard/xkbmini.go`, `keyboard/xkbmini_test.go`, `keyboard/oracle_cgo.go`, `keyboard/oracle_test.go`, new `keyboard/testdata/*.xkb`

Found by running `example/keylog` against a live compositor after phases 1–3 reported the synthetic sweep clean. AltGr+n produced `n` instead of `”`.

`resolveVirtualMods` scans **every group and every symbol** of a key when matching a compat interpret. On a multi-layout keymap that is wrong. A two-layout setup serializes `<RALT>` as:

```
key <RALT> {
    type= "ONE_LEVEL",
    symbols[1]= [ 0xffea ],    // group 1: Alt_R
    symbols[2]= [ 0xfe03 ]     // group 2: ISO_Level3_Shift
};
```

with `modifier_map Mod1 { <LALT>, <RALT>, <ALT>, <META> }`. Scanning all groups finds `0xfe03` in group 2 and ORs **Mod1** into `LevelThree`, giving `0x88` where libxkbcommon computes `0x80`. `masked` then never matches the type's `map[LevelThree]` entry, so **level 3 becomes unreachable** — AltGr stops working for every key on the layout.

libxkbcommon binds an interpret's `virtualModifier` only from the match at **group 1, level 1**, where `<RALT>` is `Alt_R` and the LevelThree interpret does not apply.

This also exposes the second oracle blind spot, and the more serious one. Phase 2's gap was *serialization* (hex vs names); this is *scope*: the sweep only ever compiles layouts built by `xkb_keymap_new_from_names`, which produces single-group keymaps. Real compositors send multi-group keymaps whose structure that path never generates. Measured on a captured live keymap, before the fix:

| sweep | Sym | Consumed |
| --- | --- | --- |
| synthetic RMLVO (all the suite tested) | 352 total | **0** |
| captured live keymap | **4672** | **13312** |

A green suite while the client is wrong 13 312 ways on the keymap it actually receives. Multi-layout is not an edge case — it is most non-English users.

### Task 1: bind from group 1, level 1

- [ ] RED: a minimal two-group keymap where a modifier-mapped key carries an unrelated keysym in group 1 and the interpret's keysym in group 2. Assert through public behavior — with `Mod5` effective, the level-3 symbol must be selected — not by reading `km.vmods`.
- [ ] GREEN: match only `k.groups[0][0]` instead of iterating all groups and symbols.
- [ ] Keep the fail-closed handling for unresolved interpret names from Phase 2, and the `VoidSymbol` / numeric-zero distinction, intact. Their regressions must still pass.

**Verified expectation** (prototyped and measured before this plan was written, not predicted): `LevelThree` becomes `0x80`; the live keymap goes to **Sym 128 / Consumed 0**, with all 128 capitalization-shaped; the synthetic sweep is **unchanged** at Sym 0/128/128/96 and Consumed 0 everywhere. Any movement in the synthetic numbers is a regression.

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
