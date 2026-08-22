# `keyboard` — handoff, 2026-08-22 (pick up Monday)

## TL;DR

The keyboard plan (all 5 phases) is **complete and committed**. The oracle
suite is **green for the first time**: `Sym`, `Consumed` and `Rune` all at
**0 mismatches on all five keymaps**.

Nothing is half-finished. There is no in-flight task, no uncommitted work of
mine, no agent still running. You can start Monday on anything below.

```sh
go build ./... && go vet ./... && go test ./...   # green, pure Go
go test -tags oracle ./keyboard/...               # green, needs libxkbcommon dev headers
```

The one uncommitted file is `CLAUDE.md`, which is **your** edit, not mine — I
left it alone deliberately.

## Where the code is

| commit | what |
| --- | --- |
| `342cbed` | baseline: XKB compiler, Composer, oracle harness |
| `5010a7f` | Phase 1 — `preserve[]` |
| `fb85c3e`, `646dccd` | Phase 2 — fail closed on unresolved named interprets |
| `db87418`..`f1fb1b4` | Phase 3 — `cmd/keysymgen` + generated tables |
| `c5750f4` | Phase 4a — bind virtual modifiers from group 1, level 1 |
| `a635863` | Phase 4b — fixture sweep of captured real keymaps |
| `12c2a67` | `IsModifierKey`, `KEYLOG_DUMP_KEYMAP`, fixture header, docs |
| `55b7d2a` | Phase 5a — `Keysym.ToUpper` |
| `5320220` | Phase 5b — apply in `State.Sym`, settle tie-break vs library |

Plan: `docs/superpowers/plans/2026-08-22-keyboard-implementation.md`
Spec/reference: `docs/keyboard.md` (Spanish, per `CLAUDE.md`)
Ledger with every ruling: `.superpowers/sdd/2026-08-22-keyboard-implementation/progress.md`

## What to do first, in order

### 1. CI — highest value, nothing exists today

There is **no CI configuration in this repo at all**: no `.github/`, no
makefile test target. Every guard built this week only fires if a human types
the command. The suite is green *now*, so this is the moment — wiring CI to a
red suite is how people learn to ignore it.

Two jobs, because they need different environments:

- default: `go build ./... && go vet ./... && go test ./...` — pure Go, no
  system deps.
- oracle: needs `libxkbcommon` dev headers + `pkg-config` + `CGO_ENABLED=1`;
  runs `go test -tags oracle ./keyboard/...`.

### 2. `Composer` tests — the biggest untested surface

`compose.go` has **zero automated tests**. I watched it work by eye twice
(`´`+`a`→`á`, `¨`+`a`→`ä`, `´`+`A`→`Á`) and that is all the verification it
has ever had.

An xkbcommon oracle is meaningless here — `Composer` implements canonical
Unicode NFC on purpose, not X11's Compose file — so it needs its own
table-driven suite. The state machine's documented rules are in
`docs/keyboard.md` ("Composición"): dead+base composing, dead+space, dead+same
dead, dead+non-composing base, dead+non-printable.

### 3. The `Keyboard` lifecycle layer — the remaining feature work

`docs/keyboard.md` describes it and it does not exist: the `Keyboard` type,
`Event`/`Mods`, focus handling, the repeat timer, and the
`input/keyboard` + `input/xkbmini` split.

`example/keylog/window.go` is a working prototype of exactly this — seat
capability tracking (including hot-unplug), keymap fd handling with
`MAP_PRIVATE` and close-on-every-path, `enter` key seeding, `leave` composer
reset. Lift from it.

**Design question to settle first:** `Event.Text` currently carries control
characters — Escape yields `"\x1b"`, and Tab/Return/Backspace likewise. That is
consistent with `Keysym.Rune`'s contract but is a trap for anything appending
it to a buffer. Decide whether `Text` means *insertable text* (control keys
yield `""`, callers switch on `Sym`) or stays raw. I lean to the former.

### 4. Deferred minors, recorded so they are not rediscovered

- **`reSymList` orders groups by bracket position**, not by the `symbols[N]`
  index that precedes each bracket. A key writing `symbols[2]` before
  `symbols[1]` would silently mislabel groups. Unobservable today because real
  `xkbcomp` always emits ascending order — but it is the same shape of
  assumption that produced three of this week's bugs. Fix when the parser is
  next touched.
- **The fixture sweep bounds groups per-key** (`oracle.NumLayouts(kc)`), so
  out-of-range group *wrapping* is never exercised. A reviewer measured that
  widening it to the keymap-wide max yields identical results — so this is
  unguarded correct behaviour, not a bug. ~24k extra comparisons, ~0.1s.
- **`task-brief` cannot address Phase 4/5 tasks.** The consolidated plan has
  two "Task 1" headings; the script matches the first and silently overwrites.
  Phase 5 briefs are hand-written as `phase5-task-N-brief.md`. Remember this if
  a Phase 6 lands in the same file.

## How this package is tested — read before changing anything

Two sweeps, sharing **one** comparison body (`compareOracle` in
`oracle_test.go`). Keeping them shared is deliberate: if duplicated they drift,
and the fixture stops testing what the RMLVO path tests.

1. **Synthetic RMLVO** — `us`, `es`, `es(cat)`, `us(intl)` via
   `xkb_keymap_new_from_names`.
2. **Captured real keymaps** — `keyboard/testdata/*.xkb` via
   `xkb_keymap_new_from_string`.

The second is not an extra. `new_from_names` only produces **single-group**
keymaps; a real compositor sends multi-group ones as soon as the user has two
layouts. That gap hid a bug that killed AltGr entirely while the synthetic
sweep reported `Consumed` perfect.

Capture another fixture with:

```sh
KEYLOG_DUMP_KEYMAP=keyboard/testdata/whatever.xkb go run ./example/keylog
```

Plus two whole-table guarantees, both across all 2505 generated keysyms:
`TestGeneratedRunesAgainstLibxkbcommon` (vs `xkb_keysym_to_utf32`) and
`TestGeneratedKeysymToUpperAgainstLibxkbcommon` (vs `xkb_keysym_to_upper`).

**The sweep is driven by the library's keycode list, never `km.keys`.** If the
compiler drops a key, that must surface as a failure rather than quietly
leaving the comparison. Do not "optimise" that.

## The lesson this plan is a record of

Every bug that mattered was found by **widening what gets tested**, never by
testing the same thing harder:

| widening | what it exposed |
| --- | --- |
| library's keycode list, not `km.keys` | F1-F12, keypad ops, PrintScreen, Pause silently dropped |
| name-serialized keymaps, not just hex | unresolved interprets matching `NoSymbol`, corrupting AltGr/NumLock |
| captured real keymaps, not just RMLVO | virtual modifiers bound from every group: AltGr dead on multi-layout |
| `ToUpper` vs the library, not just the 4 observed pairs | `ssharp` has no simple Unicode uppercase; libxkbcommon pairs it anyway |

Twice a phase shipped an acceptance criterion derived from an unverified
inference, and both times it was wrong (see Phase 1's "Correction" section and
the ledger's rulings). What fixed it: **measure before writing the criterion,
classify rather than sample, and prefer a comparison against the reference over
an argument about what the reference probably does.**

Two live bugs were found by running `example/keylog` against a real compositor
after the suite looked clean. Keep doing that.
