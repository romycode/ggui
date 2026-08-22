### Task 1: bind from group 1, level 1

- [ ] RED: a minimal two-group keymap where a modifier-mapped key carries an unrelated keysym in group 1 and the interpret's keysym in group 2. Assert through public behavior — with `Mod5` effective, the level-3 symbol must be selected — not by reading `km.vmods`.
- [ ] GREEN: match only `k.groups[0][0]` instead of iterating all groups and symbols.
- [ ] Keep the fail-closed handling for unresolved interpret names from Phase 2, and the `VoidSymbol` / numeric-zero distinction, intact. Their regressions must still pass.

**Verified expectation** (prototyped and measured before this plan was written, not predicted): `LevelThree` becomes `0x80`; the live keymap goes to **Sym 128 / Consumed 0**, with all 128 capitalization-shaped; the synthetic sweep is **unchanged** at Sym 0/128/128/96 and Consumed 0 everywhere. Any movement in the synthetic numbers is a regression.


---

## Diagnostic context (verified, do not re-derive)

`resolveVirtualMods` currently scans every group and every symbol of each key:

```go
for _, g := range k.groups {
    for _, s := range g {
        if s == want {
            km.vmods[vm[1]] |= mask
        }
    }
}
```

A two-layout keymap serializes `<RALT>` with two groups:

```
key <RALT> {
    type= "ONE_LEVEL",
    symbols[1]= [ 0xffea ],    // group 1: Alt_R
    symbols[2]= [ 0xfe03 ]     // group 2: ISO_Level3_Shift
};
```

and `modifier_map Mod1 { <LALT>, <RALT>, <ALT>, <META> }`. The loop finds
`0xfe03` in group 2 and ORs Mod1 into `LevelThree` -> `0x88` instead of `0x80`.
`masked` then never matches the type's `map[LevelThree]` entry, so level 3 is
unreachable and AltGr silently stops working for the whole layout.

libxkbcommon binds an interpret's `virtualModifier` only from the match at
group 1, level 1 -- where `<RALT>` is `Alt_R`, so the LevelThree interpret does
not apply to it at all.

A captured real keymap exhibiting this is at:
`.superpowers/sdd/2026-08-22-keyboard-implementation/live-multigroup.xkb`

Task 2 will check it in as a test fixture; for Task 1 it is available if you
want to confirm behavior at scale, but your REQUIRED regression test must be a
minimal hand-written keymap in `xkbmini_test.go`, not this file.

## Verification commands

```sh
go build ./... && go vet ./... && go test ./...
go vet -tags oracle ./keyboard/...
go test -tags oracle ./keyboard/...   # expected to FAIL overall: Sym gaps are known
gofmt -l keyboard/
```

The oracle sweep must still report, unchanged: Sym 0/128/128/96 and Consumed 0
for us/es/es(cat)/us(intl). Report any movement rather than accepting it.
