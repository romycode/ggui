# Phase 5, Task 2: apply the transform in `State.Sym`, and settle `ToUpper` against the library

**Files:**
- Modify: `keyboard/xkbmini.go`
- Modify: `keyboard/xkbmini_test.go`
- Modify: `keyboard/oracle_test.go`

Task 1 added `Keysym.ToUpper` (commit `55b7d2a`). Nothing calls it yet. This
task applies it and — more importantly — verifies it against real
libxkbcommon.

## Part A: apply it in `State.Sym`

`xkb_state_key_get_one_sym`, which the oracle sweep compares against, applies a
capitalization transformation: when Lock is **effective** and **not consumed**,
the resulting keysym is uppercased.

- [ ] In `State.Sym`, after resolving the level and before returning: if
      `ModLock` is set in `Effective()` and **not** set in `Consumed(keycode)`,
      return `sym.ToUpper()`.

The "not consumed" half is what makes this correct rather than a blunt
uppercase-when-Caps-is-on. A key whose type spends Lock selecting the level
(`ALPHABETIC`, where `map[Lock]= 2` already produced the capital) must NOT be
uppercased again. Getting this backwards would double-apply on ordinary letters
and would show up immediately as a large `Sym` regression on `us`, which is
currently at 0.

Write the test first and watch it fail. Cover both halves: a key where Lock IS
consumed (must be unchanged) and one where it is NOT (must be uppercased).

## Part B: settle the tie-break — this is the part that matters

Task 1's reverse map (code point back to keysym) needed a tie-break, because
inverting `legacyRunes` is not a function: 802 entries, **24 code points
reachable from more than one keysym**. Task 1 chose "smaller keysym value wins"
and explicitly flagged it as unverified, listing 7 pairs it was least
confident about (math and box-drawing symbols: ∧ ∨ ∩ ∪ ⊂ ⊃ ─ │ ┌ ○).

- [ ] Add an oracle test comparing `Keysym.ToUpper()` against real
      **`xkb_keysym_to_upper`** across **every** keysym in the generated
      tables, mirroring the existing `TestGeneratedRunesAgainstLibxkbcommon`
      in `oracle_test.go` (same shape: iterate the generated keysyms, sort for
      determinism, compare, report mismatches).

This is the acceptance criterion that matters most. A `ToUpper` that happens to
fix the four observed residual pairs while being wrong elsewhere would make the
sweep go green and still be broken. The library comparison is what turns the
tie-break from a guess into a verified fact.

**If the comparison shows the tie-break is wrong for some code points**, fix the
rule so it matches libxkbcommon, and say so plainly in your report — that is a
success of the method, not a failure. Do not adjust the test to accommodate a
wrong rule.

Task 1 also observed that none of the 24 collisions is a cased letter, and
therefore believed the tie-break may be practically inert for `ToUpper` because
of its early-return guard. **Confirm or refute that with the comparison rather
than assuming it.** If it is genuinely inert, say so — that is useful, because
it means the reverse map's risk lives entirely in some future caller, not here.

## Expected results

- `Sym` reaches **0 on all five keymaps**: `us`, `es`, `es(cat)`, `us(intl)`,
  and `testdata/live-multigroup.xkb`.
- `Consumed` and `Rune` stay at **0** everywhere. If either moves, stop and
  report — this change must not touch them.
- `ToUpper` agrees with `xkb_keysym_to_upper` across all generated keysyms
  (~2505).

With `Sym` at 0 the entire oracle suite should go **green** for the first time.
Report explicitly whether it does.

## Constraints

- Module `github.com/romycode/ggui`, Go 1.27.0. Stdlib only in shipped code.
- Code and comments in English (EN_US). This task touches no docs.
- No cgo outside the `oracle` build tag — the new comparison test belongs in
  `oracle_test.go`, which is already tagged.
- Do not modify `Keysym.Rune`, `Consumed`, type selection, `preserve[]`, or
  `resolveVirtualMods`. You may modify `ToUpper` and its reverse map **only**
  if the library comparison proves the tie-break wrong.
- Do not regenerate `keysyms.gen.go`.

## Verification

```sh
go build ./... && go vet ./... && go test ./...
go vet -tags oracle ./keyboard/...
go test -tags oracle ./keyboard/...
gofmt -l keyboard/
```

Use raw hex keysym literals in any hand-written test keymap: `ParseKeysym`
returning 0 for an unknown name has produced vacuously-passing assertions in
this package before.
