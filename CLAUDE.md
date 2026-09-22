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
GGUI_REAL_WAYLAND=1 go test ./window -run Real -race -v   # opt-in: opens a window on the live session
go run ./cmd/waygenerator                  # regenerate *.gen.go from protocols/
go run ./cmd/docaudit -v                   # doc-comment coverage, plus what is missing
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
  called reentrantly from inside a listener. The only exceptions are `Close`, `Done` and
  `Err`, safe from any goroutine. With `eventloop` the UI is not that goroutine: it talks to
  it by messages, never by calling into `wlcore` (the one exception is closing: `Loop.Close`).
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

## `eventloop` — UI off the Wayland goroutine

Splits the goroutine that owns `Conn` from the one that owns widgets and canvas. `Loop.Run`
is the Wayland goroutine: `poll(2)` on the socket plus an eventfd, so it wakes for compositor
messages, for closures another goroutine `Post`s, and for its own timer
(`Deadline`/`OnTick`, meant for `Keyboard.NextRepeat`/`Tick`). It deliberately does **not**
use `DispatchUntil`: an already-expired deadline skips the read. `Inbox` carries events to
the UI without ever blocking (consecutive `Position`/`DragMove` collapse, pending key repeats
are capped, everything else is kept in order). `UI.Run` is the UI goroutine: it delivers
events, runs `UI.Do` closures (how a background task publishes a result) and paints when the
`FrameClock` allows, one frame in flight at a time.

The window must open before the application is ready: a `UI` starts in `PhaseLoading`,
`loader.go` draws with `canvas` and `math` only (a test holds its imports to that), and
`SetReady`/`Fail` end the phase. Key and pointer events are dropped until then.

Two rules a change here must keep: the UI never calls `wlcore`, and the wakeup handshake in
`Loop.takePosted` drains the eventfd **before** clearing `pending` (the other order deadlocks
the loop; a test seam pins it). Tests run with `-race`. See `docs/eventloop.md`.

## `window` — a ready-made Wayland window over `eventloop`

`window.Run(Config, init)` is the entry point an application uses. It binds the globals
(`wl_compositor`, `wl_shm` and `xdg_wm_base` are required; `wl_seat`, `wp_viewporter` and
`wp_fractional_scale_manager_v1` are optional), opens the xdg toplevel, answers the configure
handshake, keeps a two-buffer shm pool split across the two goroutines and drives the frame
clock; the application hands over a `Content` (`Paint`, `OnKey`, `OnPointer`, focus and
`OnResize` callbacks) and paints in logical units. HiDPI is automatic and needs no callback:
with both scale extensions the pool renders at whatever fractional scale `wp_fractional_scale_v1`
reports and a `wp_viewport` stretches it to the surface's logical size; with neither (or only
one), it falls back to the integer scale `wl_surface.preferred_buffer_scale` reports, core
protocol since `wl_surface` version 6 and so present on any modern compositor. The calling
goroutine becomes the Wayland goroutine, a UI goroutine runs every `Content` callback, and
`init` runs on a third one of its own while the loader is on screen. `Run` waits for the UI
goroutine and **never for `init`**, which may be parked in application code: it is told to stop
through `Window.Context`, and what it returns late is dropped. An `init` error (or panic,
recovered and logged with its stack) wins over how the connection ended, except that whatever
`init` reports after the window's context is cancelled is dropped, so an `init` that stops
because the window closed may just return `ctx.Err()` and the close stays orderly. Each `Paint`
draws one whole frame; a static UI stops committing. A `Paint` that leaves the canvas in error
(errors are sticky) costs its frame, which is not presented, and the frame's canvas is rebuilt
over the same mapping. See `docs/window.md`.

Two rules a change here must keep: the UI goroutine never calls `wlcore` (the pool and the
window queue closures with `Loop.Post` and learn results through `UI.Push`/`UI.Do`; the one
exception is `Window.Close`, which deliberately goes through `Loop.Close` so it closes the
socket from the caller and can break a Wayland goroutine stuck in a write, which a posted
close never could), and a buffer that arrives for a frame the pool already replaced is
destroyed on arrival, never adopted. The opt-in test in `window/real_test.go` runs the window
against the real compositor: it is skipped unless `GGUI_REAL_WAYLAND=1`, because it opens a
window on the live session.

## `internal/wltest` — fake compositor for tests

The far end of a socketpair, importable from `_test.go` files only. `Server` speaks just enough
of `wl_compositor`, `wl_shm`, `wl_seat`, `wp_viewporter`, `wp_fractional_scale_manager_v1` and
xdg-shell for a real client to open a window; it runs its own reading goroutine (never call
`Compositor.ReadRequest` on a connection a `Server` owns), **reads the pool pixels through the
mapped fd** so a test sees what the client painted, supports both release policies
(`ReleaseImmediately`, `ReleaseOnNextCommit`: a client that only works with one is broken),
injects configure, ping, close, focus, keys, pointer and the two scale-reporting events
(`SendPreferredScale`, `SendPreferredBufferScale`), and records in `Errors()` every protocol
violation it noticed. `NewServer` uses `t.Setenv`, so a test that uses it cannot be parallel.

Two rules a change here must keep: the fake must not accept what a real compositor would reject
(an `attach` before the first `ack_configure` is an error, and so is a buffer that does not fit
its pool; when the real compositor teaches a lesson the fake missed, tighten the fake), and
tests never use `time.Sleep` as synchronization: wait on a channel, a barrier or a bounded poll
of the fake's state. Check `Errors()` after the window has closed. Tests run with `-race`.

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
