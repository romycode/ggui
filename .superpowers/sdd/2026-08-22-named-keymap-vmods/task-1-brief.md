## Task 1: fail closed for unresolved named interpret keysyms

**Files:**

- Modify: `keyboard/xkbmini.go`
- Modify: `keyboard/xkbmini_test.go`

### Diagnose and RED

- Reconfirm the recorded data flow: a compatibility interpret name absent from `keysymNames` makes `ParseKeysym` return zero; explicit `NoSymbol` in a modifier-mapped key also parses to zero; `resolveVirtualMods` treats the equality as a real match and ORs the unrelated real modifier into the virtual modifier.
- Add the smallest self-contained named-keymap regression containing working `ISO_Level3_Shift`, unresolved `ISO_Level3_Latch`/`ISO_Level3_Lock` and `Num_Lock`, plus unrelated modifier-mapped keys with explicit `NoSymbol`.
- Through public state behavior, prove that effective `ModMod5` selects a level mapped by `LevelThree` and effective `ModMod2` selects a level mapped by `NumLock`.
- Run the focused test before production edits. Record the wrong symbols and confirm the failure is caused by inflated virtual-modifier masks, not parsing or compilation errors.

### GREEN

- Make `resolveVirtualMods` ignore an interpret whose symbolic keysym name could not be resolved, rather than treating its zero result as a wildcard match for `NoSymbol`.
- Keep the fix local and minimal. Preserve deliberate zero-valued symbol handling and existing fallbacks.
- Run the focused regression and all keyboard unit tests.

### Verify

- `gofmt -l keyboard/` prints nothing.
- `go build ./...` exits 0.
- `go vet ./...` exits 0.
- `go test -count=1 ./...` exits 0.
- `go vet -tags oracle ./keyboard/...` exits 0.
- The uncached oracle sweep reproduces the existing expected failures with unchanged per-layout Sym/Consumed/Rune counts; no category may move.

**Success:** Named keymaps no longer inflate LevelThree or NumLock when an unresolved interpret name coexists with `NoSymbol`, the regression proves the public behavior, and the hexadecimal oracle measurements remain unchanged.
