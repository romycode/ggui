### Task 4: Measure the oracle and update keyboard documentation

**Files:**
- Modify: `keyboard/oracle_test.go`
- Modify: `docs/keyboard.md`

**Interfaces:**
- Consumes: complete generated tables and runtime behavior from Task 3.
- Produces: one stable per-layout oracle summary and Spanish documentation of
  the exact measured residual mismatches.

- [ ] **Step 1: Add an explicit oracle summary line**

At the end of `runOracle`, after `runeMismatches` is known, add:

```go
t.Logf("mismatches: Sym=%d Consumed=%d Rune=%d",
		symMismatches, consumedMismatches, runeMismatches)
```

This is observability for the differential test, not a relaxation: existing
`t.Errorf` calls continue to fail on every mismatch.

- [ ] **Step 2: Run the oracle and capture exact results**

```sh
go test -tags oracle -count=1 -v ./keyboard/... 2>&1 | tee /tmp/keysym-oracle-after.log
rg 'mismatches: Sym=' /tmp/keysym-oracle-after.log
```

Expected invariants for all four layouts (`us`, `es`, `es(cat)`, `us(intl)`):

- `Consumed=0`;
- `Rune=0`;
- `Sym` may remain nonzero only for the separately scoped capitalization
  transform.

Because `oracle_test.go` still treats residual `Sym` mismatches as errors, a
nonzero command exit is expected until capitalization lands. Reject the task
if any `Consumed` or `Rune` count is nonzero, if a key was dropped, or if the
failure has any other mechanism.

- [ ] **Step 3: Update the measured-state section in `docs/keyboard.md`**

Edit only the current oracle/keysym sections, preserving all other user
changes. In Spanish:

1. Replace the four table rows with the exact `Sym` values from Step 2 and
   `Consumed=0`, `Rune=0`.
2. Mark the hand-seeded `keysymNames`/`legacyRunes` cause as resolved and
   explain that generated data also removed the residual automatic-type
   errors (`tslash`/`Tslash`) that affected `Consumed` and `Sym`.
3. Keep capitalization as the remaining measured `Sym` cause.
4. Preserve the structural warning that the hexadecimal oracle does not
   exercise name parsing, but add the dedicated name-path regression as the
   coverage that now closes that blind spot.
5. Replace every suggestion that `waygenerator` should gain a second mode
   with the standalone command:

```sh
go run ./cmd/keysymgen
```

6. Document the vendored libxkbcommon 1.13.2 header and the generated
   `keyboard/keysyms.gen.go` file.
7. Explain that F-keys have names but no rune/text.

- [ ] **Step 4: Verify docs and repository state**

```sh
rg -n 'waygenerator.*keysym|segundo modo de `waygenerator`|hand-seeded|siembras manuales' docs/keyboard.md keyboard
```

Expected: no stale suggestion that `waygenerator` owns keysym data and no
claim that runtime maps remain hand-seeded. Historical references in frozen
plans/specs are not rewritten.

Run the complete default quality gate:

```sh
gofmt -w keyboard/oracle_test.go
gofmt -l cmd/keysymgen keyboard example/keylog
go build ./...
go vet ./...
go vet -tags oracle ./keyboard/...
go test -count=1 ./...
go test ./cmd/keysymgen/... -run TestCommittedOutputIsCurrent -count=1 -v
```

Expected: all commands PASS and `gofmt -l` prints nothing.

Re-run the oracle capture once more and confirm its summary is identical to
Step 2, with zero `Consumed` and `Rune` counts.

- [ ] **Step 5: Prove `waygenerator` isolation**

Compare from the design commit:

```sh
git diff --exit-code bb42bfa..HEAD -- cmd/waygenerator
```

Expected: no output and exit 0.

- [ ] **Step 6: Commit Task 4**

Stage only the oracle instrumentation and the intended hunks of the existing
dirty documentation file:

```sh
git add keyboard/oracle_test.go
git add -p docs/keyboard.md
git diff --cached --check
git commit -m "docs: record generated keysym oracle results"
```

Before committing, inspect `git diff --cached -- docs/keyboard.md` and ensure
no unrelated user edit was included.

**Task 4 success:** the default repository is green, the oracle reports zero
`Consumed` and `Rune` differences, residual `Sym` differences are solely the
documented capitalization task, docs point to `keysymgen`, and
`cmd/waygenerator` is byte-for-byte untouched.

---

## Final Acceptance

- `go run ./cmd/keysymgen` is idempotent and offline.
- `ParseKeysym("F1") == 0xffbe` and `ParseKeysym("F12") == 0xffc9`.
- `Keysym(0xffbe).Name() == "F1"` and `Keysym(0xffbe).Rune() == -1`.
- `ParseKeysym("ISO_Level3_Latch") == 0xfe04` and named keymaps resolve exact
  `LevelThree=Mod5` and `NumLock=Mod2` masks.
- Legacy Unicode conversion matches libxkbcommon for every keysym exercised by
  all four oracle layouts.
- Oracle `Consumed` and `Rune` mismatch counts are zero for all layouts.
- Full build, vet, and default tests pass from a clean cache.
- The committed output freshness test passes.
- `docs/keyboard.md` contains the exact post-change measurements in Spanish.
- `.superpowers/sdd/2026-08-22-keysym-generator/` contains the SDD audit trail.
- No file under `cmd/waygenerator` changed.
