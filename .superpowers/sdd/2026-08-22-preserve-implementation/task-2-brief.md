## Task 2: verify against the oracle and update the measured docs

**Files:**
- Modify: `docs/keyboard.md`

Task 1 is the code change; this task is the measurement that proves it and the documentation that records it. Do not edit `keyboard/xkbmini.go` here — if the oracle shows `Consumed` is not zero, that is a Task 1 defect to report, not something to patch from this task.

**Step 1: Measure.**

- [ ] Run the full oracle sweep and capture per-layout counts:

```sh
go test -tags oracle -v ./keyboard/... 2>&1 | tee /tmp/oracle-after.log
```

- [ ] Confirm `Consumed` mismatches are **0 for `us`, `es`, `es(cat)` and `us(intl)`**.
- [ ] Confirm `Sym` counts are **unchanged** from the baseline table (`us` 0, `es` 448, `es(cat)` 384, `us(intl)` 160) and `Rune` counts are unchanged (29 / 52 / 50 / 32). Report any movement rather than accepting it.

**Step 2: Update `docs/keyboard.md` (Spanish).**

- [ ] Update the measured-state table with the new `Consumed` column (all zeros).
- [ ] Rewrite the causes list: the `preserve[]` bullet moves from "cause of 100% of `Consumed` mismatches" to resolved. The remaining causes — the capitalization transform and the hand-seeded keysym tables — stay, and now account for **all** remaining mismatches.
- [ ] Update the pending-work ordering: `preserve[]` is done, so the next items by measured impact are the capitalization transform, then generating the keysym tables.
- [ ] Leave the `xkb_keymap_get_as_string` hex-serialization warning and the libxkbcommon version note (1.13.2) intact — both are still true and both are easy to lose in an edit.

**Step 3: Verify.**

- [ ] `go build ./... && go vet ./... && go test ./...` green.
- [ ] Doc text is Spanish; no English leaked into `docs/keyboard.md`.

**Success criteria:** oracle reports 0 `Consumed` mismatches on all four layouts with `Sym`/`Rune` unchanged; `docs/keyboard.md` reflects the measured reality rather than the previous state.

---

## Out of scope

Explicitly **not** part of this plan, all recorded in `docs/keyboard.md` as separate pending work:

- The **capitalization transform** in `Sym` (libxkbcommon uppercases the keysym when Lock is effective and not consumed). This is the natural next task and becomes easier once `preserve[]` is correct, because "not consumed" is only meaningful after this plan lands.
- Generating `keysymNames` from `keysymdef.h` and the `Keysym.Rune` table from `keysym-utf.h`.
- `Composer` tests.
- The `Keyboard` lifecycle type, `Event`/`Mods`, the repeat timer, and the `input/keyboard` + `input/xkbmini` split.
