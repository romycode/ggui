# Phase 5, Task 1 report — `Keysym.ToUpper`

## What was built

`keyboard/xkbmini.go`:

- `func (k Keysym) ToUpper() Keysym` — mirrors `xkb_keysym_to_upper`. Returns
  `k` unchanged if `k.Rune()` is `-1` or if `unicode.ToUpper` is a no-op on
  that rune. Otherwise resolves the uppercase code point to a keysym using
  the same three-tier rule `ParseKeysym`/`xkb_utf32_to_keysym` use going
  forward: (1) Latin-1 direct (`0x20-0x7e`, `0xa0-0xff`), (2) legacy keysym
  from the reverse table, (3) Unicode-flag form `0x01000000 | codepoint`.
- `func buildLegacyByRune() map[rune]Keysym` — inverts the generated
  `legacyRunes` table (802 entries) with a deterministic tie-break (below).
  Exposed as a standalone function specifically so its determinism can be
  tested by calling it repeatedly, independent of the memoization.
- `legacyByRuneOnce sync.Once` / `legacyByRune map[rune]Keysym` — memoizes
  the reverse table at package level, built once on first `ToUpper` call
  rather than per call (`Sym` runs on every key event).

`keyboard/xkbmini_test.go`:

- `TestKeysymToUpper` — table-driven, 7 cases (the 4 documented pairs plus
  the 3 required edge cases). Raw hex keysym literals throughout, per the
  brief's warning about `ParseKeysym` returning 0 for unknown names.
- `TestLegacyReverseTieBreakIsDeterministic` — calls `buildLegacyByRune()`
  50 times and checks two colliding pairs (`Tab`/`KP_Tab`, `Return`/
  `KP_Enter`) resolve the same way every time. Go randomizes map iteration
  order per run, so this is a real check against the trap the brief warned
  about, not a tautology.

No other files were touched. `git diff --stat` for `keyboard/xkbmini.go`
shows a pure addition (one new import, `sync`) — nothing existing was
edited.

## Tie-break rule chosen, and why

`legacyRunes` has 24 code points reachable from more than one keysym (found
by inverting the table and listing runes with >1 source keysym — see
"Collision census" below). The rule: **when two keysyms map to the same
rune, the reverse table keeps the numerically smaller keysym.**

Reasoning: inspecting all 24 collisions, the smaller value consistently
picks the "plain"/older-vintage keysym over a keypad or vendor-specific
variant — e.g. `Tab` (`0xff09`) over `KP_Tab` (`0xff89`); `KP_Multiply`
(`0xffaa`) over `XF86NumericStar` (`0x1008120a`); `KP_0`..`KP_9`
(`0xffb0`-`0xffb9`) over `XF86Numeric0`..`XF86Numeric9`
(`0x10081200`+). XKB assigns the `0x1008xxxx` block to XFree86 vendor
keysyms introduced later than the core X11 set, and the keypad range
(`0xff80`-`0xffbd`) sits below it too, so "smaller wins" tracks "more
canonical / earlier-assigned wins" across every case I inspected. It is
simple, total (keysym values are always distinct, so there are no further
ties), and independent of Go's map iteration order by construction — it
compares values, not insertion or iteration position.

**I did not derive this from the libxkbcommon source** (per the brief's
instruction not to attempt that). It is a defensible, documented,
deterministic default for Task 2 to check against the real
`xkb_keysym_to_upper`.

### Collision census (all 24 pairs, smaller keysym in **bold**)

| rune | keysyms (smaller **bold**) |
| --- | --- |
| `\t` U+0009 | **Tab** (0xff09) vs KP_Tab (0xff89) |
| `\r` U+000D | **Return** (0xff0d) vs KP_Enter (0xff8d) |
| `*` U+002A | **KP_Multiply** (0xffaa) vs XF86NumericStar (0x1008120a) |
| `.` U+002E | **decimalpoint** (0x0abd) vs KP_Decimal (0xffae) |
| `0`-`9` U+0030-0039 | **KP_0..KP_9** (0xffb0-0xffb9) vs XF86Numeric0..9 (0x10081200+) |
| `∧` U+2227 | **logicaland** (0x08de) vs upcaret (0x0ba9) |
| `∨` U+2228 | **logicalor** (0x08df) vs downcaret (0x0ba8) |
| `∩` U+2229 | **intersection** (0x08dc) vs upshoe (0x0bc3) |
| `∪` U+222A | **union** (0x08dd) vs downshoe (0x0bd6) |
| `⊂` U+2282 | **includedin** (0x08da) vs leftshoe (0x0bda) |
| `⊃` U+2283 | **includes** (0x08db) vs rightshoe (0x0bd8) |
| `─` U+2500 | **horizconnector** (0x08a3) vs horizlinescan5 (0x09f1) |
| `│` U+2502 | **vertconnector** (0x08a6) vs vertbar (0x09f8) |
| `┌` U+250C | **topleftradical** (0x08a2) vs upleftcorner (0x09ec) |
| `○` U+25CB | **emopencircle** (0x0ace) vs circle (0x0bcf) |

(Exact hex values were read from `keysyms.gen.go` during analysis via a
temporary, non-committed test file; that scratch file was deleted before
writing the real tests. It is not part of the diff.)

### Important observation for Task 2

**None of the 24 colliding runes is a cased letter** — they are Tab/Return,
ASCII digits, ASCII punctuation (`*`, `.`), and math/box-drawing symbols.
`unicode.ToUpper` is a no-op on every one of them, which means `ToUpper`'s
"already uppercase/caseless" guard returns the input keysym unchanged
*before* the reverse table is ever consulted for any of these 24 cases —
whether called directly on one of the colliding keysyms (e.g.
`Tab.ToUpper()`) or reached indirectly. I could not construct a scenario
where the tie-break actually changes `ToUpper`'s output for a real key
event. I flag this rather than treating it as settled: Task 2's brief says
its full-sweep test against real `xkb_keysym_to_upper` "settles all 24
cases empirically," so if it exercises `ToUpper` directly on every generated
keysym (not only as a target of some other symbol's uppercase), it will
still touch these 24 — worth confirming the tie-break truly never
diverges from libxkbcommon, even though my analysis says it's inert. I am
**least confident about the math/box-drawing pairs** (`∧∨∩∪⊂⊃─│┌○`), since
I have no structural argument for why the "smaller wins" rule should be
right there beyond "it was right for Tab/Return/keypad/vendor" — those seven
pairs are two arbitrarily-named legacy symbols with no keypad/vendor
distinction to justify the choice either way.

## Verbatim RED output

Ran before any implementation existed (only the test file had the new
tests):

```
$ go test ./keyboard/... -run 'TestKeysymToUpper|TestLegacyReverseTieBreakIsDeterministic' -v
# github.com/romycode/ggui/keyboard [github.com/romycode/ggui/keyboard.test]
keyboard/xkbmini_test.go:685:20: tt.in.ToUpper undefined (type Keysym has no field or method ToUpper)
keyboard/xkbmini_test.go:709:8: undefined: buildLegacyByRune
FAIL	github.com/romycode/ggui/keyboard [build failed]
FAIL
```

Failing for the right reason: both undefined-symbol errors point at the two
pieces of missing implementation (`ToUpper`, `buildLegacyByRune`), not at a
typo or scaffolding mistake in the test file.

## Verbatim GREEN output

```
$ go test ./keyboard/... -run 'TestKeysymToUpper|TestLegacyReverseTieBreakIsDeterministic' -v
=== RUN   TestKeysymToUpper
=== RUN   TestKeysymToUpper/long_s_uppercases_out_of_the_Unicode-flag_space_into_plain_ASCII
=== RUN   TestKeysymToUpper/dstroke_uppercases_to_its_legacy_Dstroke_target
=== RUN   TestKeysymToUpper/idotless_uppercases_to_plain_ASCII_I
=== RUN   TestKeysymToUpper/mu_uppercases_to_legacy_Greek_MU,_crossing_script
=== RUN   TestKeysymToUpper/already-uppercase_keysym_is_unchanged
=== RUN   TestKeysymToUpper/keysym_with_no_code_point_is_unchanged
=== RUN   TestKeysymToUpper/uppercase_target_with_no_legacy_or_Latin-1_form_falls_back_to_the_Unicode-flag_form
--- PASS: TestKeysymToUpper (0.00s)
    --- PASS: TestKeysymToUpper/long_s_uppercases_out_of_the_Unicode-flag_space_into_plain_ASCII (0.00s)
    --- PASS: TestKeysymToUpper/dstroke_uppercases_to_its_legacy_Dstroke_target (0.00s)
    --- PASS: TestKeysymToUpper/idotless_uppercases_to_plain_ASCII_I (0.00s)
    --- PASS: TestKeysymToUpper/mu_uppercases_to_legacy_Greek_MU,_crossing_script (0.00s)
    --- PASS: TestKeysymToUpper/already-uppercase_keysym_is_unchanged (0.00s)
    --- PASS: TestKeysymToUpper/keysym_with_no_code_point_is_unchanged (0.00s)
    --- PASS: TestKeysymToUpper/uppercase_target_with_no_legacy_or_Latin-1_form_falls_back_to_the_Unicode-flag_form (0.00s)
=== RUN   TestLegacyReverseTieBreakIsDeterministic
--- PASS: TestLegacyReverseTieBreakIsDeterministic (0.01s)
PASS
ok  	github.com/romycode/ggui/keyboard	0.019s
```

Note on the "long s" test case: the brief's table lists `U017F` as
`(0x017f)`, which is the **code point** (U+017F), not the raw keysym value.
XKB has no legacy keysym for LATIN SMALL LETTER LONG S, so the keysym that
actually carries that rune is the Unicode-flag form `0x0100017f` (what
`ParseKeysym("U017F")` produces). The test uses `0x0100017f` as input,
matching the brief's own description ("uppercases OUT of the Unicode-flag
space into plain ASCII") — `0x017f` alone would have `Rune() == -1` and the
test would vacuously pass by hitting the "no code point" guard instead of
exercising the real code path.

## Every command run, with results

```
$ go build ./... && go vet ./... && go test ./...
ok  	github.com/romycode/ggui/canvas
ok  	github.com/romycode/ggui/cmd/keysymgen
ok  	github.com/romycode/ggui/cmd/keysymgen/internal/keysymdata
ok  	github.com/romycode/ggui/cmd/waygenerator
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/codegen
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/goname
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/resolve
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/symbols
ok  	github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel
?   	github.com/romycode/ggui/example/cursorshape	[no test files]
?   	github.com/romycode/ggui/example/hidpi	[no test files]
ok  	github.com/romycode/ggui/example/keylog
?   	github.com/romycode/ggui/example/scaling	[no test files]
?   	github.com/romycode/ggui/example/wayland	[no test files]
ok  	github.com/romycode/ggui/keyboard
?   	github.com/romycode/ggui/wayland/cursorshape	[no test files]
?   	github.com/romycode/ggui/wayland/fractionalscale	[no test files]
?   	github.com/romycode/ggui/wayland/tablet	[no test files]
?   	github.com/romycode/ggui/wayland/viewporter	[no test files]
ok  	github.com/romycode/ggui/wayland/wlcore
?   	github.com/romycode/ggui/wayland/xdgshell	[no test files]

$ go vet -tags oracle ./keyboard/...
(no output — clean)

$ gofmt -l keyboard/
(no output — clean)
```

## Oracle sweep — confirmed unchanged

```
$ go test -tags oracle ./keyboard/... -v
    oracle_test.go:205: mismatches: Sym=0 Consumed=0 Rune=0      # us
    oracle_test.go:205: mismatches: Sym=128 Consumed=0 Rune=0    # es
    oracle_test.go:205: mismatches: Sym=128 Consumed=0 Rune=0    # es(cat)
    oracle_test.go:205: mismatches: Sym=96 Consumed=0 Rune=0     # us(intl)
--- FAIL: TestOracleAgainstLibxkbcommon (1.16s)
    --- PASS: TestOracleAgainstLibxkbcommon/us (0.29s)
    --- FAIL: TestOracleAgainstLibxkbcommon/es (0.29s)
    --- FAIL: TestOracleAgainstLibxkbcommon/es(cat) (0.29s)
    --- FAIL: TestOracleAgainstLibxkbcommon/us(intl) (0.29s)
    oracle_test.go:205: mismatches: Sym=128 Consumed=0 Rune=0    # multi-group fixture
--- FAIL: TestOracleFixtureAgainstLibxkbcommon (0.34s)
    --- FAIL: TestOracleFixtureAgainstLibxkbcommon/testdata/live-multigroup.xkb (0.34s)
--- PASS: TestGeneratedRunesAgainstLibxkbcommon (0.00s)
... (all other pre-existing keyboard package tests PASS, including the new
     TestKeysymToUpper and TestLegacyReverseTieBreakIsDeterministic)
```

Matches the brief's required baseline exactly: Sym 0/128/128/96 for
us/es/es(cat)/us(intl), 128 for the multi-group fixture, Consumed 0 and Rune
0 everywhere. `TestOracleAgainstLibxkbcommon` and
`TestOracleFixtureAgainstLibxkbcommon` still report `FAIL` overall (expected
— those Sym mismatches are exactly what Task 2 will fix by calling
`ToUpper` from `State.Sym`; nothing in this task changes `State.Sym`).

## Scope check

`git diff -- keyboard/xkbmini.go` is a pure addition: one new import
(`sync`) plus `buildLegacyByRune`, the package-level memoization vars, and
`ToUpper`, appended after the existing `Name()` method. Nothing existing
was edited — `State.Sym`, `Keysym.Rune`, `Consumed`, type selection,
`preserve[]`, and `resolveVirtualMods` are all byte-for-byte unchanged.

A temporary analysis test file (`keyboard/zzdup_analysis_test.go`) was
created and deleted during investigation, used only to enumerate the 24
collisions and to find a rule-3 example rune with no legacy/Latin-1 form
(`U+0115` -> `U+0114`); it never touched `git diff` and is not part of the
final change.

The pre-existing uncommitted changes in `CLAUDE.md`,
`.superpowers/sdd/2026-08-22-keyboard-implementation/progress.md`, and
`docs/superpowers/plans/2026-08-22-keyboard-implementation.md` were left
untouched and unstaged, per instructions.

## Concerns

- The math/box-drawing collision pairs (7 of the 24: `∧∨∩∪⊂⊃─│┌○`) are the
  ones I'm least sure about for the tie-break — see "Important observation
  for Task 2" above. My analysis says the tie-break is inert for all 24
  cases in practice (none is a cased letter, so `ToUpper`'s early-return
  guard fires first), but Task 2 should still verify this claim as part of
  its full sweep rather than take it on faith.
- No other concerns. Build, vet, gofmt, full test suite, and the oracle
  sweep (both build-tag vet and the actual run) all pass or match the
  required unchanged baseline.
