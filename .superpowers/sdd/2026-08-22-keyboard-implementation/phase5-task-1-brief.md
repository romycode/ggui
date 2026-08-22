# Phase 5, Task 1: `Keysym.ToUpper`

Add a `ToUpper` method on `Keysym` that mirrors libxkbcommon's
`xkb_keysym_to_upper`. Task 2 will apply it inside `State.Sym`; this task only
builds and tests the function.

**Files:**
- Modify: `keyboard/xkbmini.go`
- Modify: `keyboard/xkbmini_test.go`

## Why it is needed

`xkb_state_key_get_one_sym` — the function the oracle sweep compares against —
applies a capitalization transformation: when Lock is effective and *not
consumed*, the resulting keysym is uppercased. `State.Sym` does not, which is
the only remaining `Sym` disagreement in the whole suite (128 / 128 / 96 / 128
on `es` / `es(cat)` / `us(intl)` / the multi-group fixture; `us` is already 0).

The residual was classified rather than sampled: every mismatch satisfies
`xkb_keysym_to_upper(got) == want`, and they reduce to four distinct pairs:

| from | to | why it is interesting |
| --- | --- | --- |
| `U017F` ſ (0x017f) | `S` (0x53) | uppercases OUT of the Unicode-flag space into plain ASCII |
| `dstroke` đ (0x0111) | `Dstroke` (0x0110) | legacy keysym target |
| `idotless` ı (0x0131) | `I` (0x49) | ASCII target |
| `mu` µ (0x00b5) | `Greek_MU` (0x039c) | legacy target, and cross-script |

Go's `unicode.ToUpper` yields the correct **code point** for all four; this was
verified before the task was written. The difficulty is not the uppercasing, it
is getting from a code point back to the right keysym.

## What to build

`Keysym.ToUpper() Keysym`. The forward direction (`Keysym.Rune`) already
exists. The reverse — code point to keysym — does not, and is the whole of the
work. Mirror `xkb_utf32_to_keysym`'s rule:

1. **Latin-1 direct.** Code point in `0x20`–`0x7e` or `0xa0`–`0xff` → the
   keysym *is* the code point. (`ſ`→`S` depends on this: `S` has no legacy
   keysym entry.)
2. **Legacy keysym** if one exists for that code point (`Đ`→`0x1d0`,
   `Μ`→`0x7cc`).
3. **Unicode-flag form** otherwise: `0x01000000 | codepoint`.

A keysym whose `Rune()` is `-1`, or whose uppercase equals its input, must come
back unchanged.

## The trap — read this before writing the reverse map

Inverting `legacyRunes` **is not a function**. It has 802 entries and **24 code
points reachable from more than one keysym**. Real examples:

- `\t` from both `Tab` (0xff09) and `KP_Tab` (0xff89)
- `*` from `KP_Multiply` (0xffaa) and `XF86NumericStar` (0x1008120a)
- `┌` from two distinct legacy names (0x08a2, 0x09ec)

So the inverse needs a **deterministic** tie-break, and picking the wrong side
silently returns a keypad or vendor keysym where a plain one belongs. Iterating
a Go map to build the inverse without a tie-break is non-deterministic between
runs and is a bug even if tests happen to pass.

Do not try to derive the correct tie-break from first principles. Task 2 adds a
test comparing `ToUpper` against the real `xkb_keysym_to_upper` across every
generated keysym, which settles all 24 cases empirically. For this task, pick a
rule, make it deterministic, document why, and note in your report that Task 2
will verify it.

Build the reverse lookup **once** at package level (a `sync.Once` or a package
`var` initialised in `init`), not per call — `Sym` runs on every key event.

## Tests required (TDD — write them first, watch them fail)

Cover at minimum the four pairs in the table above, plus:
- a keysym that is already uppercase (unchanged)
- a keysym with no code point, e.g. `F1` (0xffbe) (unchanged)
- a keysym whose uppercase has no legacy or Latin-1 form, so rule 3 applies

Use raw hex keysym literals. `ParseKeysym` returning 0 for an unknown name has
produced vacuously-passing assertions in this package before.

## Constraints

- Module `github.com/romycode/ggui`, Go 1.27.0. Stdlib only; `unicode` is
  already imported by `xkbmini.go`.
- Code and comments in English (EN_US).
- **Do not modify `State.Sym` in this task** — that is Task 2. Do not modify
  `Keysym.Rune`, `Consumed`, type selection, `preserve[]`, or
  `resolveVirtualMods`.
- No cgo outside the `oracle` build tag.
- Adding a package-level table is fine; do not regenerate `keysyms.gen.go`.

## Verification

```sh
go build ./... && go vet ./... && go test ./...
go vet -tags oracle ./keyboard/...
go test -tags oracle ./keyboard/...
gofmt -l keyboard/
```

The oracle sweep must be **unchanged** by this task, since nothing calls
`ToUpper` yet: Sym 0/128/128/96 for us/es/es(cat)/us(intl), 128 for the
fixture, Consumed 0 and Rune 0 everywhere. Movement means you touched
something you should not have.
