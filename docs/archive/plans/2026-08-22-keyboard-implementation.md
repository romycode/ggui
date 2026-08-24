# `keyboard` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `keyboard`'s XKB keymap compiler agree with the real `libxkbcommon`, proven by a differential oracle rather than by inspection. Starting point: the compiler resolved keycode+modifiers to keysyms but disagreed with the library on thousands of comparisons per layout. Ending point: `Sym`, `Consumed` and `Rune` all agree **exactly** — zero mismatches on every tested keymap, synthetic and real.

**Architecture:** Five phases, executed in this order because each one's measurement exposed the next. No phase was planned before its predecessor's numbers came in — that is the point of the method, not a defect in it.

1. **`preserve[]`** — the XKB directive marking modifiers as *not consumed*, parsed and then discarded. Confined to `keyboard/xkbmini.go`.
2. **Name-serialized virtual modifiers** — unresolved interpret keysyms comparing equal to `NoSymbol` and corrupting `LevelThree`/`NumLock`. Confined to `keyboard/xkbmini.go`.
3. **`keysymgen`** — a standalone offline generator producing complete keysym tables from a vendored libxkbcommon header, replacing the hand-seeded stubs. New `cmd/keysymgen` subsystem, independent of `cmd/waygenerator`.
4. **Multi-group virtual modifiers** — interprets bound from every group instead of group 1, breaking AltGr on real multi-layout keymaps; plus a fixture-based sweep of captured real keymaps. `keyboard/xkbmini.go`, `keyboard/oracle_*.go`, `keyboard/testdata/`.
5. **The capitalization transform** — the last `Sym` gap: libxkbcommon uppercases when Lock is effective and not consumed. `keyboard/xkbmini.go`.

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
| after 5 | Sym / Consumed / Rune | **0 / 0 / 0** | **0 / 0 / 0** | **0 / 0 / 0** | **0 / 0 / 0** |

Plus `testdata/live-multigroup.xkb` (a captured real 3-group keymap), added in Phase 4: **0 / 0 / 0**.

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

---

## Phase 5: the capitalization transform — the last oracle gap

**Files:** `keyboard/xkbmini.go`, `keyboard/xkbmini_test.go`, `keyboard/oracle_test.go`

The only remaining `Sym` disagreement. `xkb_state_key_get_one_sym` — the function the oracle compares against — applies a **capitalization transformation**: when Lock is effective and *not consumed*, the resulting keysym is uppercased. `State.Sym` does not, so it returns the lowercase form.

This is deliberately last. "Lock effective and not consumed" is only a meaningful condition once `preserve[]` (Phase 1) and the group-1 virtual-modifier binding (Phase 4) are both correct — implementing it earlier would have been building on two wrong inputs.

The residual was **classified, not sampled**: every one of the 128 mismatches on `es` satisfies `xkb_keysym_to_upper(got) == want`, and they reduce to four distinct pairs, 32 occurrences each:

| from | to | note |
| --- | --- | --- |
| `U017F` ſ (0x017f) | `S` (0x53) | uppercases **out** of the Unicode-flag space into plain ASCII |
| `dstroke` đ (0x0111) | `Dstroke` (0x0110) | legacy keysym target |
| `idotless` ı (0x0131) | `I` (0x49) | ASCII target |
| `mu` µ (0x00b5) | `Greek_MU` (0x039c) | legacy keysym target, cross-script |

Go's `unicode.ToUpper` produces the correct code point for all four — verified before this phase was written.

### Task 1: `Keysym.ToUpper`

The forward direction (`Keysym.Rune`) exists. The reverse — code point back to keysym — does not, and it is the whole difficulty. `xkb_utf32_to_keysym`'s rule, which must be mirrored:

1. Latin-1 direct: code point in `0x20`–`0x7e` or `0xa0`–`0xff` → the keysym **is** the code point. (`ſ`→`S` depends on this: `S` has no legacy keysym.)
2. Otherwise, a legacy keysym if one exists (`Đ`→`0x1d0`, `Μ`→`0x7cc`).
3. Otherwise, the Unicode-flag form `0x01000000 | codepoint`.

**The trap:** inverting `legacyRunes` is not a function. It has 802 entries and **24 code points reachable from more than one keysym** (`\t` from both `Tab` and `KP_Tab`; `*` from `KP_Multiply` and `XF86NumericStar`; `┌` from two legacy names). The inverse needs a deterministic tie-break, and picking the wrong side silently returns a keypad or vendor keysym where a plain one belongs. Do not guess the rule — verify it (see below).

- [ ] Build the reverse lookup once (package-level, not per call — `Sym` is on the hot path of every key event).
- [ ] Implement `Keysym.ToUpper()` following the three rules above.

### Task 2: apply it in `State.Sym`, and guard it with the library

- [ ] In `State.Sym`, after resolving the level: if `ModLock` is in `Effective()` and **not** in `Consumed(keycode)`, return `sym.ToUpper()`.
- [ ] Add an oracle test comparing `Keysym.ToUpper()` against **`xkb_keysym_to_upper`** across every generated keysym, mirroring the existing `TestGeneratedRunesAgainstLibxkbcommon`. This is what settles the 24 collisions definitively instead of by reasoning, and it is the acceptance criterion that matters most — a `ToUpper` that happens to fix the four observed pairs while being wrong elsewhere would pass the sweep and still be broken.

**Success:** `Sym` reaches **0 on all five keymaps** (`us`, `es`, `es(cat)`, `us(intl)`, and the multi-group fixture); `Consumed` and `Rune` stay at 0; `ToUpper` agrees with `xkb_keysym_to_upper` on all ~2505 generated keysyms.

**Consequence worth stating:** with `Sym` at 0 the whole oracle suite goes green *on its own merits*. An earlier draft of this plan proposed baselining the known residual so the suite could pass while the gap remained. That was the wrong instinct — a tolerance knob to live with a gap that could simply be closed is the same "green but unexamined" failure this plan keeps finding. Closing it removes the need for the knob entirely, and makes CI worth adding.

---

## Outcome

All five phases landed. **The oracle suite is green**: `Sym`, `Consumed` and
`Rune` all report **0 mismatches on all five keymaps** — `us`, `es`, `es(cat)`,
`us(intl)`, and a captured multi-group keymap from a real compositor. The
compiler agrees with libxkbcommon 1.13.2 on every keycode x group x all 256
modifier combinations, on both synthetic and real keymaps.

Supporting guarantees:

- `TestGeneratedRunesAgainstLibxkbcommon` — all 2505 generated keysyms vs
  `xkb_keysym_to_utf32`, clean.
- `TestGeneratedKeysymToUpperAgainstLibxkbcommon` — all 2505 vs
  `xkb_keysym_to_upper`, clean. This is what settled Phase 5's reverse-map
  tie-break empirically instead of by argument, and it is what caught the
  `ssharp` divergence that no amount of reasoning about Unicode would have
  surfaced.
- The name-serialization path resolves correctly, guarded by three dedicated
  regressions.
- `example/keylog` runs against a live compositor and doubles as a prototype
  of the pending `Keyboard` lifecycle layer; `KEYLOG_DUMP_KEYMAP` captures new
  fixtures.

### What this plan is actually a record of

Every bug that mattered was found by **widening what gets tested**, never by
testing the same thing harder. In order:

| # | Widening | What it exposed |
| --- | --- | --- |
| 1 | Sweep the library's keycode list, not `km.keys` | multi-line key blocks dropped: F1-F12, keypad operators, PrintScreen, Pause |
| 2 | Feed name-serialized keymaps, not just hex | unresolved interpret names matching `NoSymbol`, corrupting AltGr and NumLock |
| 3 | Sweep captured real keymaps, not just RMLVO | virtual modifiers bound from every group: AltGr entirely dead on multi-layout setups |
| 4 | Compare `ToUpper` against the library, not just fix the observed pairs | `ssharp` has no simple Unicode uppercase; libxkbcommon pairs it anyway |

Each widening made the previous "green" look premature. Twice a phase shipped
an acceptance criterion derived from an unverified inference and it was wrong
(recorded in the ledger as rulings, and in Phase 1's own correction section).
The habit that fixed it: **measure before writing the criterion, classify
rather than sample, and prefer a comparison against the reference over an
argument about what the reference probably does.**

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
