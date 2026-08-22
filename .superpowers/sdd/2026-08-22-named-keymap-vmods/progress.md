# SDD ledger — plan: /home/romycode/garage/ggui/.superpowers/plans/2026-08-22-named-keymap-vmods.md

Baseline commit: 5010a7f (keyboard: thread preserve[] through to keyType.preserve)
Spec: /home/romycode/garage/ggui/docs/keyboard.md (measured-state oracle limitation and name-serialization warning)
Branch: fix/named-keymap-vmods
Worktree: /home/romycode/garage/ggui/.worktrees/named-keymap-vmods

## Pre-flight conflict scan

| Rows checked | Finding |
| --- | --- |
| Task 1 self: named-keymap public-behavior regression vs local fail-closed resolver change | Consistent. The test exercises `Compile` → virtual-modifier resolution → type masks → `State.Sym`, while the production edit stays in `resolveVirtualMods`. |
| Task 1 files vs Global Constraints | Consistent. Only `keyboard/xkbmini.go` and `keyboard/xkbmini_test.go` may change; keysym tables, `Consumed`, type selection, preserve handling, dependencies, generated files, and production cgo are excluded. |
| Task 1 RED/GREEN vs oracle invariance | Consistent. The regression covers the name path that the hex oracle cannot see; the oracle counts are required to remain unchanged. |

Scan clean. No pre-flight rulings required.

## Progress

Task 1: baseline green (`go test -count=1 ./...`).
Task 1: dispatched from BASE 5010a7f using task-1-brief.md.
Task 1: implementer DONE at fb85c3e; task review dispatched with review-5010a7f..fb85c3e.diff.
Task 1: complete (commits 5010a7f..fb85c3e, spec compliant, quality Approved; no findings).
Final review at fb85c3e: fixes required — Critical: valid numeric-zero interpret values are rejected; Important: the named LevelThree fixture succeeds through fallback instead of a matching ISO_Level3_Shift key. One final fix wave dispatched to the original implementer; FIX_BASE fb85c3e.
Final fix wave: commit 646dccd; scoped re-review verdicts both findings ADDRESSED, no new breakage, no out-of-scope observations.
Final controller verification at 646dccd: formatting, build, vet, oracle-tag vet, focused regressions, uncached full suite, race-short suite, and diff check all exit 0. The oracle exits 1 only for known expected gaps and reproduces exact counts: us 0/0/29, es 448/320/52, es(cat) 384/256/50, us(intl) 160/64/32.
Final review: clean after the single fix wave. No deferred minors, parked findings, or rulings.

Artifact preservation: at the user's request, this completed SDD workspace was copied from the isolated worktree into the repository's main `.superpowers/sdd/2026-08-22-named-keymap-vmods/` directory. Keep these artifacts; do not apply the skill's default post-review deletion.
