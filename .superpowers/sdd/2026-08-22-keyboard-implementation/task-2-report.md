# Task 2 report: sweep captured real keymaps

## What was built

1. **`keyboard/oracle_cgo.go`** — added `newOracleRefFromKeymap(keymapText string) (*oracleRef, error)`, an
   `xkb_keymap_new_from_string`-based constructor alongside the existing `newOracleRef` (which uses
   `xkb_keymap_new_from_names`). It returns the same `*oracleRef` type, so `Keycodes`, `NumLayouts`,
   `NumLevels`, `LevelSyms`, `Sym`, `Consumed`, `Close` all work unchanged against it. Memory discipline
   mirrors the existing constructor exactly: the `C.CString` for the keymap text is freed via `defer
   C.free(...)`, and every error path unrefs whatever was already allocated (`ctx` on `xkb_keymap_new_from_string`
   failure; `keymap` and `ctx` on `xkb_state_new` failure) before returning.

   Unlike `newOracleRef`, this constructor does not need to return `keymapText` — the caller already has it
   (it's what's being loaded), so there's nothing new to hand back.

2. **`keyboard/testdata/live-multigroup.xkb`** — copied verbatim from
   `.superpowers/sdd/2026-08-22-keyboard-implementation/live-multigroup.xkb`. A two-layout (multi-group)
   keymap captured from a live compositor via `KEYLOG_DUMP_KEYMAP`, checked in as a fixture. 40605 bytes,
   1744 lines.

3. **`keyboard/oracle_test.go`** — factored the sweep so both entry points drive one implementation:
   - `compareOracle(t *testing.T, oracle *oracleRef, keymapText string)` is the sweep body (unchanged
     logic, just extracted): compiles `keymapText` with xkbmini, drives every keycode the oracle
     defines × every group × all 256 real-modifier combinations, comparing `Sym`, `Consumed`, and
     (over the keysym universe the oracle keymap defines) `Rune`, with the same `maxMismatches`-capped
     `t.Errorf` reporting convention as before.
   - `runOracle(t, layout, variant)` — unchanged behavior, now just builds the oracle via `newOracleRef`
     and calls `compareOracle`.
   - `runOracleFixture(t, path)` — new. Reads the fixture file with `os.ReadFile`, builds the oracle via
     `newOracleRefFromKeymap`, and calls `compareOracle` with the same keymap text.
   - `TestOracleFixtureAgainstLibxkbcommon` — new top-level test, parallel in structure to
     `TestOracleAgainstLibxkbcommon`, iterating a `fixtureKeymaps []string` slice (currently just
     `testdata/live-multigroup.xkb`) and subtesting each via `t.Run(path, ...)`.

   Because both entry points now call the same `compareOracle`, the fixture sweep and the RMLVO sweep
   cannot drift apart on what counts as a match.

## Commands run and output

### `go build ./...`
```
$ go build ./...
BUILD_OK
```

### `go vet ./...`
```
$ go vet ./...
VET_OK
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
ok  	github.com/romycode/ggui/example/keylog	(cached)
?   	github.com/romycode/ggui/example/scaling	[no test files]
?   	github.com/romycode/ggui/example/wayland	[no test files]
ok  	github.com/romycode/ggui/keyboard	(cached)
?   	github.com/romycode/ggui/wayland/cursorshape	[no test files]
?   	github.com/romycode/ggui/wayland/fractionalscale	[no test files]
?   	github.com/romycode/ggui/wayland/tablet	[no test files]
?   	github.com/romycode/ggui/wayland/viewporter	[no test files]
ok  	github.com/romycode/ggui/wayland/wlcore	(cached)
?   	github.com/romycode/ggui/wayland/xdgshell	[no test files]
```
No `oracle`-tagged files were compiled here (as expected — `go:build oracle` on both `oracle_cgo.go`
and `oracle_test.go` keeps them out of the default build/test).

### `go vet -tags oracle ./keyboard/...`
```
$ go vet -tags oracle ./keyboard/...
VET_ORACLE_OK
```

### `gofmt -l keyboard/`
```
$ gofmt -l /home/romycode/garage/ggui/keyboard/
(no output — silent, as required)
```

### `go test -tags oracle -v ./keyboard/...` (full suite)

Every non-oracle-comparison test passes. The two oracle-comparison tests fail exactly on the known,
documented capitalization residual — same as before this task, for the RMLVO sweep, and now also for
the new fixture sweep:

```
=== RUN   TestOracleAgainstLibxkbcommon
=== RUN   TestOracleAgainstLibxkbcommon/us
=== RUN   TestOracleAgainstLibxkbcommon/es
=== RUN   TestOracleAgainstLibxkbcommon/es(cat)
=== RUN   TestOracleAgainstLibxkbcommon/us(intl)
--- FAIL: TestOracleAgainstLibxkbcommon (1.14s)
    --- PASS: TestOracleAgainstLibxkbcommon/us (0.28s)
    --- FAIL: TestOracleAgainstLibxkbcommon/es (0.28s)
    --- FAIL: TestOracleAgainstLibxkbcommon/es(cat) (0.30s)
    --- FAIL: TestOracleAgainstLibxkbcommon/us(intl) (0.29s)
=== RUN   TestOracleFixtureAgainstLibxkbcommon
=== RUN   TestOracleFixtureAgainstLibxkbcommon/testdata/live-multigroup.xkb
--- FAIL: TestOracleFixtureAgainstLibxkbcommon (0.32s)
    --- FAIL: TestOracleFixtureAgainstLibxkbcommon/testdata/live-multigroup.xkb (0.32s)
=== RUN   TestGeneratedRunesAgainstLibxkbcommon
--- PASS: TestGeneratedRunesAgainstLibxkbcommon (0.00s)
... (all remaining xkbmini tests PASS) ...
FAIL
FAIL	github.com/romycode/ggui/keyboard	1.480s
```

Per-subtest mismatch summary lines (`t.Logf("mismatches: Sym=%d Consumed=%d Rune=%d", ...)`), captured
with `-run TestOracleAgainstLibxkbcommon -v` and `-run TestOracleFixtureAgainstLibxkbcommon -v`:

| target | Sym | Consumed | Rune |
| --- | --- | --- | --- |
| us | 0 | 0 | 0 |
| es | 128 | 0 | 0 |
| es(cat) | 128 | 0 | 0 |
| us(intl) | 96 | 0 | 0 |
| **testdata/live-multigroup.xkb** | **128** | **0** | **0** |

**RMLVO numbers confirmed unchanged**: Sym 0/128/128/96, Consumed 0, Rune 0 on us/es/es(cat)/us(intl) —
identical to the pre-task baseline recorded in the plan's Outcome section and in the task brief.

**Fixture sweep result**: Sym=128, Consumed=0, Rune=0 — matches the brief's measured prototype result
("After the fix (current main): Sym 128, Consumed 0") exactly.

### Spot-checking the fixture's 128 Sym mismatches are capitalization-shaped

Sampled the printed mismatches (capped at 40 lines by `maxMismatches`, plus the "... and N more" tally):
representative pairs are `got=0x100017f want=0x53` (`'S'`→uppercased-Unicode form) and `got=0x2b9
want=0x49` (`'I'`, modifier `ı`/`İ` dotless-i class) on keycodes `<AD02>` and `<AD08>` in group 1 — the
same shape as the `es` RMLVO residual (`ccedilla`→`Ccedilla`, `ssharp`→`SSHARP` etc.), i.e. the documented
`xkb_state_key_get_one_sym` Lock-uppercasing transform, out of scope per the brief. Did not attempt to fix
it, per instructions, and did not modify `keyboard/xkbmini.go`.

## Files touched (committed)

- `keyboard/oracle_cgo.go` — added `newOracleRefFromKeymap`.
- `keyboard/oracle_test.go` — extracted `compareOracle`; added `runOracleFixture`, `fixtureKeymaps`,
  `TestOracleFixtureAgainstLibxkbcommon`.
- `keyboard/testdata/live-multigroup.xkb` — new fixture, copied verbatim from the SDD run directory.

Commit: `a635863` — "keyboard: sweep captured multi-group keymaps against the oracle" (on `main`, no
branch/push/rebase/amend performed, as instructed).

Left untouched, as instructed: `CLAUDE.md`, `example/keylog/window.go`, and (though not explicitly named
in the task, also pre-existing and not written by this task)
`docs/superpowers/plans/2026-08-22-keyboard-implementation.md` — all three remain unstaged/uncommitted in
the working tree exactly as found.

## Concerns

None. All required checks are green (`go build ./...`, `go vet ./...`, `go test ./...`,
`go vet -tags oracle ./keyboard/...`, `gofmt -l keyboard/`), the fixture sweep's numbers match the
brief's measured expectation exactly (Sym 128 / Consumed 0 / Rune 0), the RMLVO sweep is byte-for-byte
unchanged (Sym 0/128/128/96, Consumed 0, Rune 0), and `keyboard/xkbmini.go` was not modified.
