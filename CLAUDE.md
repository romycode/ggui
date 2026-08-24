# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Language conventions

- Documentation (`docs/`, README, markdown files): Spanish (ES).
- Code and code comments: English (EN_US).

# Commands

```sh
go build ./...
go test ./...
go test ./canvas -run TestFillRectSubPixelCoverageIsExact   # single test
go test -tags oracle ./keyboard/...        # XKB oracle: needs cgo + libxkbcommon dev headers
go test ./canvas -run FuzzDrawing -fuzz FuzzDrawing
go test ./canvas -bench . -benchmem
go run ./cmd/waygenerator                  # regenerate *.gen.go from protocols/
make generate-protocols                    # re-download protocols/*.xml, then regenerate
go run ./example/wayland                   # examples need a live Wayland session
```

The generator's CWD must be the repo root: `main.go` hardcodes `run("protocols", "wayland/wlcore")`.

# Architecture

Pure-Go Wayland client — no cgo anywhere in the shipped code (the only cgo is
`keyboard/oracle_cgo.go`, behind the `oracle` build tag, used to differential-test
against the real libxkbcommon). Dependencies are limited to `golang.org/x/...`.

## `wayland/wlcore` — hand-written runtime + generated core

Hand-written, the generator never touches: `conn.go`, `proxy.go`, `wire.go`, `fixed.go`,
`registry.go`. Everything else in the package is `*.gen.go` generated from `wayland.xml`
and **overwritten on every run** — never edit a `.gen.go` file; change the templates in
`cmd/waygenerator/internal/codegen` instead.

Core protocol objects live in `wlcore` alongside the runtime (they are the core, not an
extension), so generated core code never qualifies itself with `wlcore.`. Extensions each
get their own sibling package (`xdgshell`, `viewporter`, `fractionalscale`, `tablet`,
`cursorshape`) and import `wlcore`.

Conventions the whole runtime rests on:

- **Single goroutine.** `objects`, `nextID`, `freeIDs`, `in`, `fds`, `oob` have no locks —
  the entire `Conn` API is driven from the goroutine that pumps. `Roundtrip()` cannot be
  called reentrantly from inside a listener.
- **Listeners** are structs of func fields set via `SetListener(XListener{...})`; nil fields
  mean "ignore" (and any fd in that event gets `DropFD`'d). `ProxyBase.OnClear` is how
  `Conn.Destroy` zeroes a type's listener without the runtime knowing the concrete type.
- **Object ids**: 1 is `wl_display`, built by hand in `Connect()`; client ids start at 2;
  `0xFF000000`+ is the server's range. `wl_display.delete_id` recycles into `freeIDs`.
- A malformed message is fatal, not recoverable: the stream is misaligned, so the caller
  closes the connection.

## `cmd/waygenerator` — XML → bindings

Four passes chained by `main.run`: `xmlmodel` (parse `protocols/*.xml`) → `symbols` (build
the XML-name → Go-name/package table) → `resolve` (resolve types, versions, cross-package
refs) → `codegen` (render templates, `format.Source`, write).

Adding a protocol means touching three places: the `manifest` in `xmlmodel.go`, the
`packageOf`/`prefixOf`/`suffixOf` maps in `symbols.go`, and the `download-protocols` target
in `makefile`. `codegen.packageDir` derives each output directory from `wayland/wlcore` by
substituting the last segment, so `main.go` never enumerates packages.

`codegen` is covered by golden files in `internal/codegen/testdata/*.golden`. There is no
`-update` flag — regenerate an expectation deliberately when the template change is intended.

**The generator↔runtime contract is a closed list** (table at the top of
`docs/waygenerator.md`). If a template needs something from `wlcore` that isn't on it,
either the contract is wrong or the template is overreaching; don't just widen the API.

## `canvas` — immediate-mode CPU rasterizer

Borrows the caller's `[]uint32` (ARGB8888, premultiplied, `Stride` in pixels, padding
allowed), never allocates or copies it. Logical units in, physical pixels out, via one
immutable scale factor. Errors are **sticky**: only `New` returns an error; the first bad
argument is recorded and every later call becomes a no-op until `Canvas.Err()` is checked.
Damage accumulates as a single union rect in physical pixels, ready for
`wl_surface.damage_buffer`.

Drawing ops are **zero-allocation, and that is asserted in `go test`** via
`testing.AllocsPerRun`, not just measured in benchmarks. Any change to a draw path must keep
it allocation-free. Fuzz targets check that nothing writes into row padding and that damage
stays inside the visible region.

## `keyboard` — XKB subset

`Compile`/`Keymap`/`State` (`xkbmini.go`) implement just the XKB v1 keymap a client needs;
`Composer` (`compose.go`) does canonical-NFC dead keys. Deliberately out of scope: actions,
includes, the X11 Compose file. The `oracle` build tag sweeps every keycode × group × the
256 modifier combinations against libxkbcommon 1.13.2 — the sweep is driven by the
*library's* keycode list, not `km.keys`, so a key `Compile` misses shows up as a failure.
The oracle currently reports zero mismatches on all five keymaps, so `docs/keyboard.md`
tracks what the oracle *cannot* reach rather than a gap list: `Composer` has no tests
(comparing it to libxkbcommon is meaningless — it implements canonical NFC, not X11's
Compose file).

## `docs/`

`docs/wlcore.md` and `docs/waygenerator.md` are the normative design specs for the runtime
and the generator, not just narrative — check them (and the upstream Wayland spec) before
making a protocol decision, rather than deriving behavior from the Go code alone.
`docs/archive/` holds frozen specs and implementation plans, one per feature and
dated; an undated file under `docs/` is living documentation that tracks the code.
