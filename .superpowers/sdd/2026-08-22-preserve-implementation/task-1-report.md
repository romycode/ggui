# Task 1 report: thread `preserve[]` through to `keyType.preserve`

## What changed and why

`keyboard/xkbmini.go` parsed `preserve[X]= Y;` directives with `rePreserve`
but threw the result away: the loop in `parseTypes` stuffed a
`"preserve:"+normalizeMods(e[1])` key into `mapRaw` with value `0` and
discarded `e[2]` (the right-hand side) entirely. `resolveTypeMasks` then
explicitly skipped any `mapRaw` entry with that prefix. The net effect: every
`keyType.preserve` map stayed empty for the lifetime of the `Keymap`, so
`State.Consumed` — `t.mods &^ t.preserve[masked]` — always evaluated to the
full `t.mods`, i.e. "nothing is ever preserved." Any type using
`preserve[]` (`FOUR_LEVEL_SEMIALPHABETIC` foremost among them, used for most
letter keys on `es`/`es(cat)`/`us(intl)`) reported Lock as consumed when it
should not have been.

Fix, exactly per the brief:

1. Added `preserveRaw map[string]string` to `keyType` (normalized LHS ->
   raw RHS string), initialized at both `keyType` construction sites in
   `parseTypes` (the loop body and the synthetic `ONE_LEVEL` fallback).
2. `parseTypes`'s preserve loop now records into `t.preserveRaw` instead of
   stuffing a sentinel into `mapRaw`.
3. `resolveTypeMasks` deleted the `strings.HasPrefix(raw, "preserve:")` skip
   (dead code once `preserveRaw` exists) and, after resolving `t.levels` as
   before, resolves each `preserveRaw` pair through `km.modMask` on both
   sides (LHS and RHS can each name a virtual modifier) and stores into
   `t.preserve`.

No `"preserve:"` string literal remains anywhere in the package (verified by
grep; the only textual match left is a comment in the new test explaining
the pre-fix bug, not a literal in production code).

`State.Consumed`'s formula (`t.mods &^ t.preserve[masked]`) was not touched.
`km.modMask` remains the only modifier-name resolver; no second path was
added.

## RED — tests failing for the right reason

First attempt used single-line `xkb_keycodes "t" { <AB01> = 52; };` bodies
per the brief's literal snippets; that hit `reSection`'s requirement of a
newline before the closing `};` and failed as a parse error (wrong reason):

```
=== RUN   TestConsumedHonorsPreserveRealModifier
    xkbmini_test.go:177: Compile: keyboard: keymap missing xkb_keycodes or xkb_symbols
--- FAIL: TestConsumedHonorsPreserveRealModifier (0.00s)
=== RUN   TestConsumedHonorsPreserveVirtualModifier
    xkbmini_test.go:207: Compile: keyboard: keymap missing xkb_keycodes or xkb_symbols
--- FAIL: TestConsumedHonorsPreserveVirtualModifier (0.00s)
=== RUN   TestConsumedHonorsPreserveNone
    xkbmini_test.go:233: Compile: keyboard: keymap missing xkb_keycodes or xkb_symbols
--- FAIL: TestConsumedHonorsPreserveNone (0.00s)
FAIL
```

Reformatted the keycodes/symbols sections to multi-line (matching the style
already used by every other test in the file), and re-ran. This time all
three failed for the right reason — a wrong `Consumed` value:

```
=== RUN   TestConsumedHonorsPreserveRealModifier
    xkbmini_test.go:186: Consumed(Lock) = 0x3, want 0x1 (ModShift; Lock is preserved)
--- FAIL: TestConsumedHonorsPreserveRealModifier (0.00s)
=== RUN   TestConsumedHonorsPreserveVirtualModifier
    xkbmini_test.go:221: Consumed(Lock+Mod5) = 0x83, want 0x81 (ModShift|ModMod5; Lock is preserved)
--- FAIL: TestConsumedHonorsPreserveVirtualModifier (0.00s)
=== RUN   TestConsumedHonorsPreserveNone
--- PASS: TestConsumedHonorsPreserveNone (0.00s)
FAIL
```

`TestConsumedHonorsPreserveNone` passed even pre-fix: with `t.preserve`
always empty (map default `0`), `preserve[X]=none` (which should resolve to
mask `0`) and "no preserve entry at all" are indistinguishable — both leave
`Consumed` at the full `t.mods`. This is expected and not a test defect: the
`none` case is a regression guard for after the fix, not proof of the bug on
its own. The other two tests are the proof: `0x3`/`0x83` are `t.mods`
unreduced, `0x1`/`0x81` are `t.mods` with `Lock` correctly subtracted.

## GREEN

```
=== RUN   TestConsumedHonorsPreserveRealModifier
--- PASS: TestConsumedHonorsPreserveRealModifier (0.00s)
=== RUN   TestConsumedHonorsPreserveVirtualModifier
--- PASS: TestConsumedHonorsPreserveVirtualModifier (0.00s)
=== RUN   TestConsumedHonorsPreserveNone
--- PASS: TestConsumedHonorsPreserveNone (0.00s)
PASS
ok  	github.com/romycode/ggui/keyboard	0.002s
```

## Full verification commands run

| Command | Result |
|---|---|
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `go test ./...` | ok, all packages (keyboard included) |
| `go vet -tags oracle ./keyboard/...` | OK |
| `gofmt -l keyboard/` | prints nothing |
| `go test ./keyboard/... -run TestConsumedHonorsPreserve -v` | 3/3 PASS |
| `go test -tags oracle ./keyboard/...` | still fails overall (expected — Sym/Rune gaps remain out of scope); see below for the counts that matter |

## Oracle sweep: per-layout counts, before vs. after

Baseline ("before") was captured by `git stash`-ing the two changed files,
re-running the oracle suite against the original pre-fix code, and popping
the stash back. The before-fix Sym/Rune numbers reproduce exactly the
baseline given in the task ("Sym: us=0, es=448, es(cat)=384, us(intl)=160;
Rune: 29/52/50/32"), confirming the starting point was correctly
reconstructed.

| Layout | Consumed before | Consumed after | Sym before/after | Rune before/after |
|---|---|---|---|---|
| us | 3200 | **0** | 0 / 0 | 29 / 29 |
| es | 4416 | 320 | 448 / 448 | 52 / 52 |
| es(cat) | 4416 | 256 | 384 / 384 | 50 / 50 |
| us(intl) | 3520 | 64 | 160 / 160 | 32 / 32 |

Sym and Rune are **byte-for-byte unchanged** on every layout — confirms this
change did not touch level selection, as required.

`us` reaches the target of 0. `es`, `es(cat)`, `us(intl)` dropped by
90–100% (4416→320, 4416→256, 3520→64) but did not reach 0. Root cause
identified below; it is not a defect in this change.

## Concern: remaining Consumed mismatches trace to a separate, pre-existing, out-of-scope bug

Traced one remaining `es` mismatch by hand: key `<AC08>` (`k`/`K`/`kra`/
`ampersand`, auto-typed `FOUR_LEVEL_SEMIALPHABETIC`, `modifiers=
Shift+Lock+LevelThree`). Compiling the real `es` keymap text
(`xkbcli compile-keymap --layout es`) with `xkbmini.Compile` and inspecting
the resulting `Keymap` directly:

```
vmods["LevelThree"] = 0xf8   // want: 0x80 (ModMod5) only
type.mods            = 0xfb  // want: 0x83 (Shift|Lock|LevelThree)
```

`km.vmods["LevelThree"]` should resolve to `ModMod5` alone (from
`modifier_map Mod5 { <LVL3> }` plus `interpret ISO_Level3_Shift { ...
virtualModifier= LevelThree; ... }`), but `resolveVirtualMods` also matches
two more interpret blocks in the same compat section —
`ISO_Level3_Latch` and `ISO_Level3_Lock` — which likewise declare
`virtualModifier= LevelThree`. Their keysym names are **not** in the
hand-seeded `keysymNames` table, so `ParseKeysym("ISO_Level3_Latch")` and
`ParseKeysym("ISO_Level3_Lock")` both return `0`. `resolveVirtualMods`'s
inner loop then matches `s == want` against `0` for *any* key carrying an
explicit `NoSymbol` in its symbol list (which also parses to `0`), so it ORs
in the real-modifier masks of several unrelated keys (Mod1/Mod2/Mod3/Mod4
mapped keys), inflating `LevelThree` to `0xf8`. `type.mods` for
`FOUR_LEVEL_SEMIALPHABETIC` then becomes `0x83 | (0xf8 &^ 0x80) = 0xfb`
instead of `0x83`, which corrupts both the `masked` lookup used for level
selection and the one used for `Consumed`.

This is precisely the trap flagged in my task brief ("`ParseKeysym` returns
0 for any keysym name missing from the hand-seeded `keysymNames` table,
which can make an assertion trivially true") — except it is live in
`resolveVirtualMods`, not just a test-writing hazard. It predates this task
(confirmed: the "before" stash run shows the same `Sym` mismatch counts the
task states as baseline, meaning this inflation was already present and
already contributing to those Sym gaps on `es`/`es(cat)`/`us(intl)` — the
three layouts that use virtual modifiers heavily — while `us`, which barely
touches virtual modifiers, hits 0 both for Sym and now for Consumed).

I did **not** touch `resolveVirtualMods` or `ParseKeysym`/`keysymNames`:
doing so would edit virtual-modifier/level-selection code, which my brief
explicitly forbids ("this change must not touch level selection at all"),
and would need either a second modifier-resolution path or a materially
larger `keysymNames` table — both out of this task's stated scope. Since
`preserve[]` is fully wired through and verified correct in isolation (three
passing unit tests covering real modifiers, virtual modifiers, and `none`),
and Sym/Rune are provably unchanged, I'm reporting this rather than chasing
it further. It looks like good raw material for a follow-up task: either
seed `ISO_Level3_Latch`/`ISO_Level3_Lock`/etc. into `keysymNames`, or make
`resolveVirtualMods` skip interpret entries whose keysym failed to resolve
(`want == 0`) instead of treating that as a wildcard match.

## Files touched

- `/home/romycode/garage/ggui/keyboard/xkbmini.go` — production fix
- `/home/romycode/garage/ggui/keyboard/xkbmini_test.go` — three new tests
