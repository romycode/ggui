## Task 1: thread `preserve[]` through to `keyType.preserve`

**Files:**
- Modify: `keyboard/xkbmini.go`
- Modify: `keyboard/xkbmini_test.go`

**Step 1: Write the failing tests first (TDD).**

- [ ] Add a test proving a real-modifier `preserve[]` is honored. Minimal keymap, real modifiers only so it does not depend on virtual resolution:

```
xkb_keycodes "t" { <AB01> = 52; };
xkb_types "t" {
	type "T" {
		modifiers= Shift+Lock;
		map[Shift]= 2;
		map[Lock]= 2;
		preserve[Lock]= Lock;
	};
};
xkb_symbols "t" { key <AB01> { type= "T", [ 0x61, 0x41 ] }; };
```

With `Lock` effective, `Consumed(52)` must be `ModShift` — Lock is preserved, so it is *not* consumed. Before the fix it returns `ModShift|ModLock`.

- [ ] Add a test proving a **virtual** modifier resolves on both sides, which is what forces resolution to happen after `resolveVirtualMods`. Use `LevelThree` (falls back to `ModMod5`) on the left and `Lock` on the right, mirroring the real `FOUR_LEVEL_SEMIALPHABETIC`:

```
	type "T4" {
		modifiers= Shift+Lock+LevelThree;
		map[Lock+LevelThree]= 3;
		preserve[Lock+LevelThree]= Lock;
	};
```

An `xkb_compatibility "t" { };` section must be present or `resolveVirtualMods` never runs and the `LevelThree`→`Mod5` fallback never fires. With `Lock|Mod5` effective, `Consumed` must be `ModShift|ModMod5` (the full mask minus the preserved `Lock`).

- [ ] Add a test for `preserve[X]= none;` resolving to mask `0` (nothing preserved, so `Consumed` is the full `t.mods`).

- [ ] Run the tests and **confirm they fail for the right reason** — a wrong `Consumed` value, not a compile error or a parse failure. Record the actual failure output.

**Step 2: Implement.**

- [ ] Add a field to `keyType`:

```go
preserveRaw map[string]string // normalized "Lock+LevelThree" -> raw "Lock"
```

Initialize it in **both** `keyType` construction sites in `parseTypes` — the loop body and the synthetic `ONE_LEVEL` fallback — so no path leaves it nil.

- [ ] In `parseTypes`, replace the discarding loop with one that keeps the value:

```go
for _, e := range rePreserve.FindAllStringSubmatch(body, -1) {
    // Both sides may name virtual modifiers, so the masks are resolved
    // in resolveTypeMasks once the compat section has been read.
    t.preserveRaw[normalizeMods(e[1])] = e[2]
}
```

- [ ] In `resolveTypeMasks`, delete the `strings.HasPrefix(raw, "preserve:")` skip (now dead), and resolve the preserve pairs after the level masks:

```go
for raw, val := range t.preserveRaw {
    var lhs, rhs uint32
    for _, n := range splitMods(raw) {
        lhs |= km.modMask(n)
    }
    for _, n := range splitMods(val) {
        rhs |= km.modMask(n)
    }
    t.preserve[lhs] = rhs
}
```

- [ ] Confirm no `"preserve:"` string literal remains anywhere in the package.

**Step 3: Verify.**

- [ ] All three new tests pass.
- [ ] `go build ./... && go vet ./... && go test ./...` green.
- [ ] `go vet -tags oracle ./keyboard/...` green.
- [ ] `gofmt -l keyboard/` prints nothing.

**Success criteria:** `keyType.preserve` is populated for every type that declares `preserve[]`; the three new tests pass; the whole repo is green; no cgo outside the `oracle` tag.

---

