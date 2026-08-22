# Task 2 report: deterministic Go rendering and standalone CLI

## Implementation summary

Added the standalone `cmd/keysymgen` command and the renderer/emitter behind
its internal `keysymdata` package.

- `Render` sorts names lexically and keysym-indexed maps numerically, emits the
  exact `keyboard` declarations, formats with `go/format`, quotes strings, and
  uses lower-case zero-padded hexadecimal values.
- `Generate` is the required parse-then-render composition.
- `Emit` reads the input, produces source before touching the destination, then
  writes a `0o644` temporary file in the destination directory and atomically
  renames it into place. Temporary files are cleaned up on all failure paths.
- The command's `run` seam delegates to `keysymdata.Emit`; its default paths
  are the specified vendored header and future generated keyboard file.
- `cmd/waygenerator` remains unmodified and is not imported.

## RED evidence

After adding only `render_test.go`, `emit_test.go`, and `main_test.go`, ran:

```sh
GOCACHE=/tmp/ggui-keysym-go-cache go test ./cmd/keysymgen/... -count=1 -v
```

Result: failed as intended because `Render`, `Generate`, `Emit`, and `run`
were undefined. The compiler reported every expected missing seam; no
production implementation existed at that point.

## GREEN and verification evidence

Ran after implementing and formatting:

```sh
gofmt -w cmd/keysymgen
GOCACHE=/tmp/ggui-keysym-go-cache go test ./cmd/keysymgen/... -count=1 -v
GOCACHE=/tmp/ggui-keysym-go-cache go vet ./cmd/keysymgen/...
GOCACHE=/tmp/ggui-keysym-go-cache go test ./... -count=1
```

Results:

- Focused keysymgen tests passed, including deterministic rendering, parse
  propagation, generated output, preservation of an existing output on parse
  failure, CLI generation, and missing-input path errors.
- `go vet ./cmd/keysymgen/...` completed successfully with no findings.
- The first sandboxed full-suite run was blocked only by the sandbox denying
  Wayland socket `getsockopt`/`setsockopt`. The full suite was rerun with the
  required elevated sandbox access and passed, including `wayland/wlcore`.

## Files changed

- `cmd/keysymgen/internal/keysymdata/render.go`
- `cmd/keysymgen/internal/keysymdata/render_test.go`
- `cmd/keysymgen/internal/keysymdata/emit.go`
- `cmd/keysymgen/internal/keysymdata/emit_test.go`
- `cmd/keysymgen/main.go`
- `cmd/keysymgen/main_test.go`

## Self-review

Reviewed formatting, exported documentation, names, imports, generic helper
use, error propagation, atomic write ordering, cleanup behavior, and tests.
No must-fix, should-fix, or nit findings remain. All filesystem operation
errors include their affected path; cleanup and close errors are also retained
without allowing a failed generation to modify the existing destination.

## Concerns

None. The command was deliberately not executed against `keyboard/`; Task 3
will replace the existing declarations in the same change that adds generated
output.
