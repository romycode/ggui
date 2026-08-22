# SDD ledger — plan: .superpowers/plans/2026-08-22-keysym-generator.md

Workspace: `/home/romycode/garage/ggui/.worktrees/keysym-generator`
Branch: `feature/keysym-generator`
Merge base: `db8741870d9dbe890f30512031a5438a0033676f`
Baseline: `GOCACHE=/tmp/ggui-keysym-go-cache go test -count=1 ./...` passes with escalated Unix-socket access. The sandboxed run fails only because existing `wayland/wlcore` tests cannot call `getsockopt`/`setsockopt`.

## Pre-flight consistency scan

| Scope | Produces / consumes | Finding |
| --- | --- | --- |
| Task 1 internal | Vendored 1.13.2 header, `Tables`, and `Parse(io.Reader)` match its parser tests | Consistent; installed header version and pinned SHA-256 were verified before planning. |
| Task 2 internal | `Render`, `Generate`, `Emit`, and `main.run` match its renderer, atomic-output, and CLI tests | Consistent. |
| Task 3 internal | Generated map names match the runtime consumers; `Name`, `Rune`, keylog label, and freshness tests use the same formats | Consistent except the first pre-commit `git diff --exit-code` idempotence command cannot be clean for a newly generated uncommitted file. |
| Task 4 internal | Oracle summary feeds the exact documentation update and final gates | Consistent, with the existing main-checkout `docs/keyboard.md` edits needing preservation when Task 4 starts. |
| Tasks 1 → 2 | Task 1 produces `Tables` and `Parse`; Task 2 consumes those exact names and types | Consistent. |
| Tasks 2 → 3 | Task 2 produces the standalone command and three generated maps; Task 3 consumes all four | Consistent. |
| Tasks 3 → 4 | Task 3 produces complete runtime tables; Task 4 measures them and updates docs | Consistent. |
| Plan → SDD finish | Global constraints require the audit trail to remain under `.superpowers/sdd`; generic SDD cleanup says to delete it | Conflict resolved below. |

Ruling: For Task 3 idempotence, compare the generated file's SHA-256 immediately before and after the second `go run ./cmd/keysymgen`; the initial `git diff --exit-code` is not used until the file is committed — this proves the same behavior without asking an uncommitted new file to equal the base — cost if wrong: a non-deterministic generator could evade detection only if two consecutive runs coincidentally hash identically.

Ruling: Before Task 4 edits `docs/keyboard.md`, copy the current main-checkout file into the feature worktree, then edit the specified keysym/oracle sections. Those dirty main-checkout hunks are the accepted preserve/named-keymap findings this task is designed to supersede, not unrelated changes — cost if wrong: any unrecognized user prose in that file would become part of the feature commit, so the Task 4 reviewer must inspect the complete documentation diff carefully.

Ruling: Preserve this plan's `.superpowers/sdd/2026-08-22-keysym-generator/` audit trail instead of deleting it at SDD finish, because the user's explicit instruction and the plan's Global Constraints override generic scratch cleanup — cost if wrong: the repository retains review/process artifacts that would otherwise be disposable.

Task 1: dispatched from base `db8741870d9dbe890f30512031a5438a0033676f`.

Task 1: implementation reported DONE at `ff66dcc90bae857cfffaba7f1077f8ae2821718c`; focused parser tests and full suite pass, review dispatched.

Task 1: complete (commits `db87418..ff66dcc`, review clean).

Task 2: dispatched from base `ff66dcc90bae857cfffaba7f1077f8ae2821718c`.

Task 2: implementation reported DONE at `d8cb6eff2885be855fb492d41a9e864b28acbe54`; focused generator tests, vet, and full suite pass, review dispatched.

Task 2: complete (commits `ff66dcc..d8cb6ef`, review clean).

Task 3: dispatched from base `d8cb6eff2885be855fb492d41a9e864b28acbe54`.

Task 3: Ruling: use `%#06x` in `symLabel`, because Go's formatter renders the plan's `%#08x` sample as `0x0000ffbe`, while the same task's explicit behavior test requires `F1(0x00ffbe)`; the observable test contract wins over the contradictory implementation sample — cost if wrong: the example's numeric field has six hex digits rather than the formatter width implied by the sample, with no effect on keysym identity.

Task 3: implementation reported DONE at `a7531cd1840a77a4408109ff05d5b3cb3a88096d`; focused/full/race tests and generated-file idempotence pass, review pending.

Task 3: complete (commits `d8cb6ef..a7531cd`, review clean).

Main-checkout `docs/keyboard.md` preservation snapshot before Task 4: SHA-256 `99342f8a6b9dc80e70b3b4815d9e278d68d6ea23d804f94ba5685c5cedfdd98e`.

Task 4: dispatched from base `a7531cd1840a77a4408109ff05d5b3cb3a88096d`.

Task 4 ruling: the first measured oracle run violated the planned acceptance (`us: Sym=0 Consumed=0 Rune=12`; `es: Sym=192 Consumed=64 Rune=12`; `es(cat): Sym=192 Consumed=64 Rune=12`; `us(intl): Sym=96 Consumed=0 Rune=12`). Systematic debugging proved two in-scope completeness defects: twelve vendored XF86 numeric comments place `<U+...>` after kernel metadata that the anchored parser skipped, and the asymmetric Unicode uppercase mapping for `ß`/`ẞ` caused AC02 to be misclassified as `FOUR_LEVEL_SEMIALPHABETIC`. Task 4 therefore expands to add focused RED tests and fix those source-level causes before documentation; recording nonzero `Consumed`/`Rune` values would contradict Final Acceptance — cost if wrong: Task 4 changes generator/runtime code beyond its original file list, but only to satisfy the already-approved complete-table and zero-mismatch contracts.

Task 4: implementation reported DONE at `27470e4cd9617430bae1595505b0417324438d7e`; two identical oracle captures report `us=0/0/0`, `es=128/0/0`, `es(cat)=128/0/0`, and `us(intl)=96/0/0` for `Sym/Consumed/Rune`; build, vet, full default/race tests, freshness, idempotence, documentation scan, and `waygenerator` isolation pass; review dispatched.

Task 4 review round 1: changes requested. The broadened Unicode-annotation search is not token-aware (`GNU+0041` can be accepted as an annotation), and metadata-prefixed deprecated `(U+...)` annotations are parsed without the deprecated flag. Fix round 1 dispatched to the original implementer with focused boundary and non-prefix-parenthesized RED tests required.

Task 4 fix round 1: implementation reported DONE at `385f9aed58cf6cd4a3ea4537508e9e8affcd6539`; focused boundary/deprecation tests, full build/vet/default tests, focused race tests, freshness, idempotence, unchanged oracle summaries, and `waygenerator` isolation pass; scoped re-review dispatched.

Task 4: complete (commits `a7531cd..385f9ae`, review round 1 findings resolved, scoped re-review clean).
