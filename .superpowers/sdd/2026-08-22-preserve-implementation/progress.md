# SDD ledger — plan: docs/superpowers/plans/2026-08-22-keyboard-implementation.md

Baseline commit: 342cbed (keyboard: add XKB keymap compiler, composer and libxkbcommon oracle)
Spec: docs/keyboard.md (section "Modificadores consumidos")

## Pre-flight conflict scan

Two tasks. Shared-file and self-consistency rows:

| # | Rows checked | Finding |
| --- | --- | --- |
| T1 × T2 | T1 modifies `keyboard/xkbmini.go` + `keyboard/xkbmini_test.go`; T2 modifies `docs/keyboard.md` only | No file overlap. T2 consumes T1's oracle numbers; T2 explicitly forbidden from editing xkbmini.go, so no write conflict. |
| T1 self | Tests it specifies (3 preserve cases) vs code it specifies (`preserveRaw` field, parseTypes, resolveTypeMasks) | Consistent. Tests exercise exactly the paths the code section changes. |
| T2 self | Measurement step vs doc-edit step | Consistent. Doc edit is downstream of the measurement in the same task. |
| Global constraints × T1 | "Consumed's formula does not change" vs T1's code | Consistent — T1 touches only parseTypes/resolveTypeMasks, never the Consumed expression. |
| Global constraints × T2 | "docs are Spanish" vs T2 step 2 | Consistent, T2 restates it. |

Scan clean. No rulings required before execution.

## Rulings

Ruling: run on `main` in-place rather than a git worktree — the task depends on
the keyboard package, which was uncommitted until 342cbed; a worktree from the
previous HEAD would have contained no keyboard package at all. Baseline was
committed first (with the user's approval) so review packages diff cleanly.
Cost if wrong: work lands on main without branch isolation; recoverable by
`git revert` of the task commits, which the ledger names.

## Progress

(task lines appended below as they complete)
Task 1: dispatched (implementer, sonnet) — BASE 342cbed — brief task-1-brief.md, report task-1-report.md

Ruling: Task 1's acceptance criterion ("Consumed 0 on ALL four layouts") was a
PLAN DEFECT of mine, not a Task 1 failure. I derived it from an earlier
measurement that every Consumed mismatch satisfied `want ⊆ got` and
`got == type.mods` — a signature consistent with preserve[] being unimplemented,
but EQUALLY consistent with the key's type being misselected. I conflated the
two. Verified independently at 5010a7f: all vmods resolve correctly
(LevelThree=0x80, Alt=0x8, NumLock=0x10, Super=0x40), preserve[] is correctly
populated (FOUR_LEVEL_SEMIALPHABETIC.preserve = {0x82:Lock, 0x83:Lock}), and the
sole residual type is FOUR_LEVEL_SEMIALPHABETIC. Root cause of the residual:
es <AD05> = [t, T, tslash(0x3bc), Tslash(0x3ac)]; the AltGr pair are Latin-3
keysyms absent from legacyRunes, so Rune() = -1, isCasePair() = false, and
guessType picks SEMIALPHABETIC where libxkbcommon picks FOUR_LEVEL_ALPHABETIC
(which declares no preserve[]) -> 0x81 vs 0x83. That is the keysym-table gap
already documented as out of scope, reached through guessType.
Amended criterion: Consumed == 0 on `us`; residual 320/256/64 on
es/es(cat)/us(intl) accepted and attributed to the keysym tables.
Cost if wrong: we would be shipping a preserve[] bug disguised as a table gap;
falsifiable by regenerating legacyRunes and re-running the sweep - those 640
mismatches must vanish with no preserve[] change.

Ruling: the implementer's DONE_WITH_CONCERNS mechanism ("ParseKeysym returns 0
for ISO_Level3_Latch/Lock, spuriously matching NoSymbol and inflating
LevelThree beyond Mod5") is REJECTED as the stated cause - measured vmods are
all correct. Its conclusion (pre-existing, separate, out of scope) stands; only
the mechanism was wrong. No code change follows from this.
Cost if wrong: a real resolveVirtualMods inflation bug goes unlogged;
falsifiable by the vmod probe above, which any future session can rerun.

Ruling CORRECTED (supersedes the rejection above): the implementer's mechanism
was RIGHT and my rejection of it was WRONG. We measured different SERIALIZATIONS
of the same layout, and both results are real:

  serialization        LevelThree  NumLock  FOUR_LEVEL_SEMIALPHABETIC.mods
  hex   (oracle path)  0x80  OK    0x10 OK  0x83 OK
  names (xkbcli path)  0xf8  BAD   0x78 BAD 0xfb BAD

`xkb_keymap_get_as_string` (what the oracle feeds) emits HEX keysyms;
`xkbcli compile-keymap` emits NAMES. ParseKeysym short-circuits on "0x", so the
hex path never consults keysymNames. On the NAME path,
ParseKeysym("ISO_Level3_Latch") == 0 (unseeded), and resolveVirtualMods'
`if s == want` then matches every NoSymbol (also 0) on any modifier-mapped key,
OR-ing spurious masks into the virtual modifier. Verified directly.

Consequence, and the important part: THE ORACLE HAS A STRUCTURAL BLIND SPOT.
It only ever feeds hex, so it cannot see this class of bug at all, and my
docs/keyboard.md note that "keysymNames is never consulted during the sweep"
understated it -- that is not merely a coverage gap, it hides a live
correctness bug on name-serialized keymaps.

Both residual mechanisms are therefore real and NOT mutually exclusive:
guessType/legacyRunes (hex path, es <AD05>) AND resolveVirtualMods/ParseKeysym
inflation (name path). Neither is in scope for this plan.
Cost if wrong: none for Task 1, whose verdict is unaffected; the risk is
shipping a client that mis-resolves modifiers against any compositor sending
name-serialized keymaps.

Task 1: complete (commits 342cbed..5010a7f, spec OK, quality Approved)

Task 2: complete in the working tree under the user-approved amended criterion.
`docs/keyboard.md` now records the post-`preserve[]` oracle counts
(`Consumed`: 0/320/256/64), attributes the 640 residual oracle-visible cases
to `legacyRunes`/`guessType`, and documents the separate name-serialization
blind spot in `resolveVirtualMods`/`ParseKeysym` for later repair. Fresh
verification: `go build ./...`, `go vet ./...`, `go test -count=1 ./...`,
`go vet -tags oracle ./keyboard/...`, and `git diff --check` all exit 0. The
uncached oracle sweep exits 1 only with the recorded expected Sym/Consumed/Rune
gaps; its counts remain 0/0/29, 448/320/52, 384/256/50, and 160/64/32 for
us/es/es(cat)/us(intl). No production code changed in Task 2.
