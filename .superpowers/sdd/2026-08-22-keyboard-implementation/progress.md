# SDD ledger — plan: docs/superpowers/plans/2026-08-22-keyboard-implementation.md

Phases 1-3 were executed as three earlier runs (see their ledgers under
.superpowers/sdd/) and are complete. This run executes PHASE 4 only.

Baseline commit: 67d6908
Spec: docs/keyboard.md

## Pre-flight conflict scan

| # | Rows checked | Finding |
| --- | --- | --- |
| T1 x T2 | T1 modifies keyboard/xkbmini.go + xkbmini_test.go; T2 modifies oracle_cgo.go + oracle_test.go + adds testdata/ | No file overlap. T2 consumes T1's fix (its fixture sweep only reaches the expected numbers once T1 lands), so T1 must run first. |
| T1 self | RED test (2-group keymap, public-behavior assertion) vs GREEN change (groups[0][0]) | Consistent. The test exercises exactly the scanning behavior the change alters. |
| T2 self | new_from_string helper vs fixture sweep | Consistent; sweep depends on the helper, same task. |
| Global x T1 | "keep Phase 2 fail-closed + VoidSymbol/numeric-zero regressions passing" | Consistent - T1 changes which symbol is compared, not whether unresolved names are skipped. Those regressions must still pass and are named in the task. |
| Global x T2 | "no cgo outside the oracle tag" | Consistent - new_from_string goes in oracle_cgo.go, already behind the tag. |

Scan clean. No rulings required before execution.

## Rulings

Ruling: Phase 4's acceptance numbers were PROTOTYPED AND MEASURED before this
plan was written, not predicted. Applied the candidate fix to a scratch copy,
measured, then reverted. Live keymap 4672/13312 -> 128/0 (all 128
capitalization-shaped); synthetic RMLVO unchanged at Sym 0/128/128/96,
Consumed 0. This is deliberate: an earlier phase in this same plan shipped an
acceptance criterion derived from an unverified inference and it was wrong.
Cost if wrong: none - the numbers are reproducible by re-running the sweep.

Ruling: run on `main` in-place rather than a worktree, consistent with the
earlier phases of this plan.
Cost if wrong: no branch isolation; recoverable via git revert of the commits
the ledger names.

## Progress

Phase 4 Task 1: dispatched (implementer, sonnet) — BASE 67d6908 — brief task-1-brief.md, report task-1-report.md
Phase 4 Task 1: implemented (commit c5750f4). Controller-verified independently:
synthetic sweep bit-identical (Sym 0/128/128/96, Consumed 0), all three Phase-2
fail-closed regressions pass, default suite green, diff matches the measured
prototype. Task review + Task 2 dispatched concurrently (disjoint files:
review reads xkbmini.go, T2 writes oracle_*.go + testdata/).
Phase 4 Task 2: complete (commit a635863). Controller-verified: fixture sweep
Sym=128 Consumed=0 Rune=0 (matches the pre-plan measurement exactly); RMLVO
sweep unchanged 0/128/128/96 Consumed 0 Rune 0; default build still pure Go.
Sweep body correctly factored into a shared compareOracle() driven by both
runOracle() and runOracleFixture(), so the two entry points cannot drift.
Phase 4 Task 1: review clean (spec OK, quality Approved). Reviewer independently
probed real libxkbcommon 1.13.2 with four minimal keymaps and confirmed
groups[0][0] is the ACTUAL binding rule, not merely sufficient: target at
group 1 level 2 does not bind (rules out "group 1, any level"); group 1 absent
does not bind; group 1 level 1 == NoSymbol does NOT fall back to group 2; and
the real <RALT> scenario does not fold Mod1 into LevelThree. This settles the
controller's stated doubt about deriving the rule from a single keymap.

Task 1: minor (deferred): reSymList (xkbmini.go) appends bracket matches to
k.groups in TEXTUAL order, ignoring the symbols[N] index that precedes each
bracket. A key writing symbols[2] before symbols[1] would mislabel groups.
Pre-existing, not introduced by this diff, and real xkbcomp always emits groups
in ascending order - so unobservable today. Worth fixing when the parser is next
touched; recorded here so it is not rediscovered from scratch.

Phase 4 Task 1: complete (commits 67d6908..c5750f4, review clean)
Phase 4 Task 2: review clean (spec OK, quality Approved). Reviewer reverted
c5750f4 in a scratch tree and confirmed the fixture FAILS loudly (Sym 4672,
Consumed ~13k) while RMLVO stays bit-identical -- the blind spot demonstrated
open, then closed. Classified all 128 residual Sym mismatches (not sampled):
every one satisfies xkb_keysym_to_upper(got)==want; 128 capitalization, 0 other
(U017F->S x32, dstroke->Dstroke x32, idotless->I x32, mu->Greek_MU x32). cgo
verified leak-free: 0 KiB RSS growth over 500 create/query/close cycles.

Task 2: IMPORTANT finding, being fixed now: oracle_test.go documents the
fixture's regeneration path as "KEYLOG_DUMP_KEYMAP, see example/keylog", but
that env var exists only in the UNCOMMITTED working tree. As committed the
pointer dangles. Fixing by committing the capture hook.
Task 2: minor (deferred): fixture has no in-file provenance header - fixing now.
Task 2: minor (deferred): docs/keyboard.md still describes the sweep as RMLVO
only and omits the fixture row - fixing now.
Task 2: minor (deferred): the group loop bounds per-key (oracle.NumLayouts(kc))
rather than keymap-wide, so out-of-range group WRAPPING is never swept.
Reviewer measured that widening it yields identical results (Sym 128,
Consumed 0, 0 out-of-range mismatches) -- xkbmini already matches libxkbcommon
here, so this is unguarded behaviour, not a bug. Worth widening later.
Note: repo has NO CI configuration at all (no .github/, no makefile test
target), so "runs in CI" currently means "runs in the tagged suite".

Phase 4 Task 2: complete (commits c5750f4..a635863, review clean)
