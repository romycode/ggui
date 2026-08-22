# Task 1 report: bind from group 1, level 1

## Status

DONE

## What changed and why

`keyboard/xkbmini.go`, function `resolveVirtualMods`. The old loop scanned
every group and every symbol of a modifier-mapped key looking for the
interpret's target keysym:

```go
for _, g := range k.groups {
    for _, s := range g {
        if s == want {
            km.vmods[vm[1]] |= mask
        }
    }
}
```

libxkbcommon only binds an interpret's `virtualModifier` from a key's
**group 1, level 1** symbol. On a real multi-layout keymap, `<RALT>` is
serialized with two groups:

```
key <RALT> {
    type= "ONE_LEVEL",
    symbols[1]= [ 0xffea ],    // group 1: Alt_R
    symbols[2]= [ 0xfe03 ]     // group 2: ISO_Level3_Shift
};
```

with `modifier_map Mod1 { <LALT>, <RALT>, <ALT>, <META> }`. The old loop
found `0xfe03` (ISO_Level3_Shift) in group 2 and OR'd `Mod1` into the
`LevelThree` virtual modifier mask, inflating it to `0x88` instead of the
correct `0x80`. The `FOUR_LEVEL`/`LEVEL_THREE`-family types' `map[LevelThree]`
entries are built from the *correct* mask (`0x80`), so `Effective() &
t.mods` (now `0x88`) never equals that entry's key, level 3 becomes
unreachable, and AltGr silently stops working for the whole layout.

Fix: match only `k.groups[0][0]` (guarding for empty groups) instead of
iterating all groups and symbols:

```go
if len(k.groups) == 0 || len(k.groups[0]) == 0 {
    continue
}
if k.groups[0][0] == want {
    km.vmods[vm[1]] |= mask
}
```

This is a minimal, surgical change confined to `resolveVirtualMods`. It does
not touch `State.Consumed`, `State.Sym`, type selection, or `preserve[]`.

## Method: TDD

### RED

Added `TestVirtualModifierBindsFromGroup1Level1Only` to
`keyboard/xkbmini_test.go`: a minimal hand-written two-group keymap with

- `<RALT>`: group 1 = `[0xffea]` (Alt_R, unrelated to the interpret),
  group 2 = `[0xfe03]` (ISO_Level3_Shift, the interpret's target), mapped
  via `modifier_map Mod1 { <RALT> }` — mirrors the diagnosed real keymap
  exactly.
- `<L3S>`: single group `[0xfe03]` (correctly matches at group 1, level 1),
  mapped via `modifier_map Mod5 { <L3S> }` — proves the fix still performs a
  real, non-fallback match.
- `<AB01>`: type `"LEVEL_THREE"` (`modifiers= LevelThree; map[LevelThree]=
  2;`), symbols `[0x61, 0x62]`.
- `xkb_compatibility` with `interpret 0xfe03 { virtualModifier= LevelThree;
  };` (raw hex keysym literal per the codebase's documented `ParseKeysym`
  gotcha, not a symbolic name).

Assertion is entirely through public behavior: `Compile` -> `NewState` ->
`UpdateMask(ModMod5, 0, 0, 0)` -> `Sym(<AB01>)` must select the level-3
symbol `0x62`. No access to the private `km.vmods` map.

Verbatim RED output (`go test ./keyboard/... -run
TestVirtualModifierBindsFromGroup1Level1Only -v`):

```
=== RUN   TestVirtualModifierBindsFromGroup1Level1Only
    xkbmini_test.go:595: Sym(AB01, Mod5) = 0x61, want 0x62 -- the LevelThree interpret must bind only from <RALT>'s group 1, level 1 symbol (Alt_R), not group 2's ISO_Level3_Shift, so <RALT>'s Mod1 modifier_map entry must not inflate the LevelThree virtual modifier mask
--- FAIL: TestVirtualModifierBindsFromGroup1Level1Only (0.00s)
FAIL
FAIL	github.com/romycode/ggui/keyboard	0.010s
```

Confirmed this failed for the *right* reason: got `0x61` (level 1, the
fallback when the masked lookup misses), not a compile error or a parse
failure. This is exactly the "inflated mask never matches `t.levels`"
symptom described in the diagnostic context.

### GREEN

Implemented the fix above. Re-ran the new test plus the three protected
regressions:

```
=== RUN   TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms
--- PASS: TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms (0.00s)
=== RUN   TestVoidSymbolInterpretDoesNotMatchNoSymbol
--- PASS: TestVoidSymbolInterpretDoesNotMatchNoSymbol (0.00s)
=== RUN   TestNumericZeroInterpretMatchesNoSymbol
--- PASS: TestNumericZeroInterpretMatchesNoSymbol (0.00s)
=== RUN   TestVirtualModifierBindsFromGroup1Level1Only
--- PASS: TestVirtualModifierBindsFromGroup1Level1Only (0.00s)
PASS
ok  	github.com/romycode/ggui/keyboard	0.014s
```

## Full verification suite

All commands run from `/home/romycode/garage/ggui`.

### `go build ./...`

```
(no output, exit 0)
```

### `go vet ./...`

```
(no output, exit 0)
```

### `go test ./...`

```
ok  	github.com/romycode/ggui/canvas	(cached)
ok  	github.com/romycode/ggui/cmd/keysymgen	(cached)
ok  	github.com/romycode/ggui/cmd/keysymgen/internal/keysymdata	(cached)
ok  	github.com/romycode/ggui/cmd/waygenerator	(cached)
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/codegen	(cached)
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/goname	(cached)
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/resolve	(cached)
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/symbols	(cached)
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel	(cached)
?   	github.com/romycode/ggui/example/cursorshape	[no test files]
?   	github.com/romycode/ggui/example/hidpi	[no test files]
ok  	github.com/romycode/ggui/example/keylog	0.011s
?   	github.com/romycode/ggui/example/scaling	[no test files]
?   	github.com/romycode/ggui/example/wayland	[no test files]
ok  	github.com/romycode/ggui/keyboard	0.013s
?   	github.com/romycode/ggui/wayland/cursorshape	[no test files]
?   	github.com/romycode/ggui/wayland/fractionalscale	[no test files]
?   	github.com/romycode/ggui/wayland/tablet	[no test files]
?   	github.com/romycode/ggui/wayland/viewporter	[no test files]
ok  	github.com/romycode/ggui/wayland/wlcore	(cached)
?   	github.com/romycode/ggui/wayland/xdgshell	[no test files]
```

### `go vet -tags oracle ./keyboard/...`

```
(no output, exit 0)
```

### `gofmt -l keyboard/`

```
(no output, exit 0 — no files need formatting)
```

### `go test -tags oracle ./keyboard/...` (expected to FAIL overall; Sym gaps are known)

Overall result: FAIL (as expected — unrelated known Sym gaps documented in
prior phases; `us` subtest passes, `es`/`es(cat)`/`us(intl)` fail on
pre-existing gaps unrelated to this task).

Per-layout mismatch counts, **before** the fix (verified by temporarily
stashing `keyboard/xkbmini.go` and rerunning):

```
us:        Sym=0   Consumed=0 Rune=0   -- PASS
es:        Sym=128 Consumed=0 Rune=0   -- FAIL
es(cat):   Sym=128 Consumed=0 Rune=0   -- FAIL
us(intl):  Sym=96  Consumed=0 Rune=0   -- FAIL
```

Per-layout mismatch counts, **after** the fix:

```
us:        Sym=0   Consumed=0 Rune=0   -- PASS
es:        Sym=128 Consumed=0 Rune=0   -- FAIL
es(cat):   Sym=128 Consumed=0 Rune=0   -- FAIL
us(intl):  Sym=96  Consumed=0 Rune=0   -- FAIL
```

**Identical before and after** — exactly the "synthetic sweep UNCHANGED"
result specified as the verified expectation. This makes sense: the
synthetic sweep's keymaps are single-group and never exercise the
group-1-vs-group-2 distinction this fix addresses.

## At-scale confirmation on the captured live keymap

Per the task instructions, the captured real multi-group keymap
(`.superpowers/sdd/2026-08-22-keyboard-implementation/live-multigroup.xkb`)
is not to be checked in as a fixture (that's Task 2's job) but is available
to confirm behavior at scale. I added a temporary, throwaway test file
(`keyboard/zzscratch_test.go`, never committed, deleted immediately after
running) that compiled this file and logged `km.vmods["LevelThree"]`:

```
=== RUN   TestScratchLiveMultigroupLevelThree
    zzscratch_test.go:17: LevelThree vmod = 0x80
--- PASS: TestScratchLiveMultigroupLevelThree (0.03s)
PASS
ok  	github.com/romycode/ggui/keyboard	0.042s
```

Matches the verified expectation (`0x80`, not `0x88`) exactly. The scratch
file was deleted immediately after this one run; `git status --short`
confirms it left no trace:

```
 M CLAUDE.md
 M docs/superpowers/plans/2026-08-22-keyboard-implementation.md
 M example/keylog/window.go
 M keyboard/xkbmini.go
 M keyboard/xkbmini_test.go
```

## Every command run, with results

1. `go test ./keyboard/... -run TestVirtualModifierBindsFromGroup1Level1Only -v` (before fix) — FAIL, correct RED reason (see above).
2. Implemented the fix in `keyboard/xkbmini.go`.
3. `go test ./keyboard/... -run 'TestVirtualModifierBindsFromGroup1Level1Only|TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms|TestVoidSymbolInterpretDoesNotMatchNoSymbol|TestNumericZeroInterpretMatchesNoSymbol' -v` — all PASS.
4. `go build ./...` — clean.
5. `go vet ./...` — clean.
6. `go test ./...` — all packages ok.
7. `go vet -tags oracle ./keyboard/...` — clean.
8. `gofmt -l keyboard/` — silent.
9. `go test -tags oracle ./keyboard/... -run TestOracleAgainstLibxkbcommon -v` (after fix) — per-layout Sym/Consumed counts as above.
10. `git stash push --keep-index -- keyboard/xkbmini.go` then reran step 9 (before-fix numbers), then `git stash pop` to restore the fix.
11. Full verification suite rerun after `git stash pop` to confirm the fix is intact and all green — confirmed identical to earlier runs.
12. Scratch test against `live-multigroup.xkb`, then deleted the scratch file; confirmed via `git status --short` that no stray files remain.

## Concerns

None. The fix is a two-line behavioral change (guard + single-index
compare) confined exactly to the loop body the brief pointed at. All
protected regressions pass, the synthetic oracle numbers are bit-for-bit
unchanged before/after, and the real captured keymap now resolves
`LevelThree` to the expected `0x80`.

Two files show as modified in `git status` that I did not touch and left
alone, per the task's explicit instruction: `CLAUDE.md` and
`example/keylog/window.go`. A third, `docs/superpowers/plans/2026-08-22-keyboard-implementation.md`,
was also already modified in the working tree before I started (pre-existing,
unrelated to this task) — I did not stage or touch it either.
