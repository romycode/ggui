# Canvas 2D Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `canvas`, a CPU immediate-mode 2D rasterizer that draws ARGB8888 premultiplied pixels straight into a caller-owned `[]uint32`, with HiDPI logical coordinates, antialiasing, sticky errors and accumulated damage.

**Architecture:** One public type, `Canvas`, over a borrowed `Buffer`. Every drawing method funnels into a single private compositor (`blendPixel` / `replacePixel`) so there is exactly one implementation of the premultiplied source-over formula. Two rasterization families: axis-aligned rectangles get **exact** analytic coverage; circles, rounded rectangles and lines get **approximate** coverage from a signed distance function, driven by one generic scan loop (`sdfFill[S shape]`) that monomorphizes per shape so no closure escapes to the heap.

**Tech Stack:** Go 1.27, stdlib only (`errors`, `fmt`, `math`). No new module dependencies. Generics are used for the SDF scan loop and the `ring[S]` stroke wrapper.

**Spec:** `docs/canvas.md` — the package's reference document, alongside `docs/wlcore.md` and `docs/waygenerator.md`. Authoritative for the public API surface, pixel format, error strings, coverage formula, damage rules and Wayland integration notes. The plan argues from it; executors read both. `docs/superpowers/specs/2026-08-21-canvas-design.md` is the frozen design snapshot this plan was written against — `docs/canvas.md` is the copy that gets updated as the implementation lands (Task 11).

## Global Constraints

- Module `github.com/romycode/ggui`, Go 1.27.0. New package lives at `canvas/`, import path `github.com/romycode/ggui/canvas`.
- **Stdlib only.** No `golang.org/x/image`, no new `go.mod` requires. `x/image/vector` is mentioned in the spec only as future `DrawMask` context.
- Package name is `canvas`. Every error string is prefixed `canvas: `.
- Code and code comments in **English (EN_US)**; the docs under `docs/` are **Spanish (ES)**. This is `CLAUDE.md`, it applies to every task.
- Pixel format is ARGB8888 **premultiplied**, logical value `0xAARRGGBB`. `Color` from callers is **not** premultiplied; the canvas premultiplies once per operation.
- **No gamma correction.** Compositing happens on sRGB values treated as linear. Deliberate, per spec.
- **Coverage scales all four channels, not just alpha.** `src' = (Sr·cov, Sg·cov, Sb·cov, Sa·cov)`, then `dst = src' + dst·(1 − Sa')`. Getting this wrong produces a bright halo on every antialiased edge and is the single most important invariant in the package.
- **No drawing method returns `error`.** The first error is stored and made sticky; every later operation is a no-op. Only `New` returns an error.
- An operation that records an error leaves the buffer **and** the accumulated damage exactly as they were.
- Zero dimensions, zero radius, zero stroke width and zero alpha are documented **no-ops**, not errors: they must not write pixels and must not extend damage. Negative values *are* errors.
- Padding between `Width` and `Stride` is never read and never written, including by `Clear`.
- **Zero allocations** in every successful drawing operation. Task 11 enforces this with `testing.AllocsPerRun`; do not introduce closures, interface boxing of non-pointer shapes, or `fmt` calls on the hot path.
- `Canvas` is not safe for concurrent use and contains no mutex. Do not add one.
- Every task must leave `go build ./... && go test ./...` green across the whole repository.

## File Structure

All files are created under `canvas/`. Nothing outside `canvas/` and `docs/` is modified by this plan.

| File | Responsibility |
| --- | --- |
| `canvas/doc.go` | Package doc comment: model, ownership, pixel format, sticky error, damage. |
| `canvas/types.go` | `Buffer`, `Point`, `Rect`, `PixelRect`, `Color`, `LineCap` and its constants. Pure data, no behaviour. |
| `canvas/errors.go` | `ErrInvalidArgument`, the `argError` type, and the small constructors every validation path calls. |
| `canvas/canvas.go` | The `Canvas` struct, `New` with all constructor validation, the read-only accessors. |
| `canvas/state.go` | Sticky error (`Err`, `fail`) and damage accumulation (`Damage`, `ResetDamage`, `addDamage`). Canvas state that is not geometry. |
| `canvas/compose.go` | `mul8`, `premultiply`, `coverage8`, `blendPixel`, `replacePixel`. The single compositor. |
| `canvas/clear.go` | `Clear`, `ClearRect`. Replace semantics, not source-over. |
| `canvas/rect.go` | `FillRect`, `StrokeRect`, and `axisCoverage` — the exact analytic coverage path. |
| `canvas/sdf.go` | The `shape` interface, the generic `sdfFill` scan loop, `ring[S]`, and the float helpers (`absf`, `clamp01`, …). |
| `canvas/circle.go` | `FillCircle`, `StrokeCircle` and `circleShape`. |
| `canvas/roundrect.go` | `FillRoundedRect`, `StrokeRoundedRect` and `roundRectShape`. |
| `canvas/line.go` | `Line`, `segmentShape` (round cap) and `boxShape` (butt/square caps). |
| `canvas/validate.go` | Shared per-operation argument validation helpers (`finite`, `nonNegative`, …) used by every drawing method. |

Tests live beside their unit: `types_test.go`, `errors_test.go`, `canvas_test.go`, `state_test.go`, `compose_test.go`, `clear_test.go`, `rect_test.go`, `circle_test.go`, `roundrect_test.go`, `line_test.go`, plus `fuzz_test.go` and `bench_test.go` added in Task 11. `testhelpers_test.go` holds the buffer builders every test file uses; it is created in Task 2 and grown as needed.

The split is by responsibility, not by layer: a shape's SDF, its bounds and its public method sit in one file, so adding an ellipse later touches exactly one new file.

---

## Task 1: `types.go` + `errors.go` — data types and the error shape

**Files:**
- Create: `canvas/types.go`
- Create: `canvas/errors.go`
- Create: `canvas/doc.go`
- Test: `canvas/errors_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Buffer struct { Pixels []uint32; Width, Height, Stride int }`
  - `type Point struct { X, Y float32 }`
  - `type Rect struct { X, Y, Width, Height float32 }`
  - `type PixelRect struct { X, Y, Width, Height int }`
  - `type Color struct { R, G, B, A uint8 }`
  - `type LineCap uint8` with `LineCapButt`, `LineCapSquare`, `LineCapRound`
  - `var ErrInvalidArgument error`
  - `func invalidArg(op, arg, detail string) error` — every validation path in Tasks 2 and 5-10 builds its error with this.

The error strings are copied verbatim from the spec, so they are the thing to test first. `ErrInvalidArgument` already carries the `canvas: ` prefix, so wrapping it with `%w` would print the prefix twice; a small error type with `Unwrap` gives both the exact string and a working `errors.Is`.

- [ ] **Step 1: Write the failing tests**

Create `canvas/errors_test.go`:

```go
package canvas

import (
	"errors"
	"testing"
)

func TestInvalidArgMessage(t *testing.T) {
	cases := []struct {
		op, arg, detail string
		want            string
	}{
		{
			"New", "buffer.Stride", "must be at least buffer.Width (got 100)",
			`canvas: New: invalid argument "buffer.Stride": must be at least buffer.Width (got 100)`,
		},
		{
			"New", "buffer.Pixels", "buffer is too short (need 4096, got 4000)",
			`canvas: New: invalid argument "buffer.Pixels": buffer is too short (need 4096, got 4000)`,
		},
		{
			"New", "scale", "must be finite and greater than zero (got 0)",
			`canvas: New: invalid argument "scale": must be finite and greater than zero (got 0)`,
		},
		{
			"FillCircle", "radius", "must not be negative (got -3)",
			`canvas: FillCircle: invalid argument "radius": must not be negative (got -3)`,
		},
		{
			"Line", "cap", "unknown LineCap(4)",
			`canvas: Line: invalid argument "cap": unknown LineCap(4)`,
		},
	}
	for _, c := range cases {
		got := invalidArg(c.op, c.arg, c.detail).Error()
		if got != c.want {
			t.Errorf("invalidArg(%q, %q, %q) =\n  %q\nwant\n  %q", c.op, c.arg, c.detail, got, c.want)
		}
	}
}

func TestInvalidArgIsErrInvalidArgument(t *testing.T) {
	err := invalidArg("FillRect", "rect.Width", "must not be negative (got -1)")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("invalidArg result does not match ErrInvalidArgument")
	}
}

func TestLineCapConstants(t *testing.T) {
	// The zero value must be LineCapButt: a zero-valued LineCap in a
	// caller's struct literal has to mean the plainest cap, not an error.
	var zero LineCap
	if zero != LineCapButt {
		t.Errorf("zero LineCap = %d, want LineCapButt (%d)", zero, LineCapButt)
	}
	if LineCapSquare != 1 || LineCapRound != 2 {
		t.Errorf("LineCap constants moved: Square=%d Round=%d, want 1 and 2", LineCapSquare, LineCapRound)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mkdir -p canvas && go test ./canvas/ -run 'TestInvalidArg|TestLineCap' -v`
Expected: FAIL — build error, `invalidArg`, `ErrInvalidArgument` and `LineCap` are undefined.

- [ ] **Step 3: Write the types**

Create `canvas/types.go`:

```go
package canvas

// Buffer describes borrowed pixel memory. It is a description, not a
// renderer: Canvas reads these four fields once in New and never resizes,
// reallocates, replaces or frees the storage behind Pixels.
type Buffer struct {
	// Pixels is the borrowed storage, one ARGB8888 premultiplied pixel
	// per element. The logical value of a pixel is 0xAARRGGBB.
	Pixels []uint32
	// Width is the visible physical width in pixels.
	Width int
	// Height is the visible physical height in pixels.
	Height int
	// Stride is the distance in uint32 elements between the start of two
	// consecutive rows. It is at least Width; anything beyond Width in a
	// row is padding that no canvas operation may read or write.
	Stride int
}

// Point is a position in logical units. Fractional values are the point:
// subpixel positioning is what the antialiasing exists for.
type Point struct {
	X float32
	Y float32
}

// Rect is an axis-aligned rectangle in logical units, anchored at its
// top-left corner. Width and Height must not be negative; zero is a
// documented no-op, not an error.
type Rect struct {
	X      float32
	Y      float32
	Width  float32
	Height float32
}

// PixelRect is integer geometry in physical pixels. Only Damage returns
// one — it is deliberately a separate type from Rect so a physical
// rectangle can never be passed to a drawing method that expects logical
// units.
type PixelRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Color is a straight (non-premultiplied) 8-bit RGBA color. Canvas
// premultiplies it once per operation; callers never premultiply.
type Color struct {
	R uint8
	G uint8
	B uint8
	A uint8
}

// LineCap selects how Line terminates each end of the segment.
type LineCap uint8

const (
	// LineCapButt ends exactly at the given points.
	LineCapButt LineCap = iota
	// LineCapSquare extends half the stroke width past each end.
	LineCapSquare
	// LineCapRound ends with a semicircle of radius half the stroke width.
	LineCapRound
)
```

- [ ] **Step 4: Write the errors**

Create `canvas/errors.go`:

```go
package canvas

import (
	"errors"
	"fmt"
)

// ErrInvalidArgument is the sentinel every argument-validation failure
// wraps. Match it with errors.Is; the wrapped error's message carries the
// operation, the argument and the constraint that was broken.
var ErrInvalidArgument = errors.New("canvas: invalid argument")

// argError reports a bad argument to a specific operation. It formats its
// own message instead of wrapping ErrInvalidArgument with %w because the
// sentinel already carries the "canvas: " prefix, and %w would print it
// twice. Unwrap keeps errors.Is working.
type argError struct {
	op     string
	arg    string
	detail string
}

func (e *argError) Error() string {
	return fmt.Sprintf("canvas: %s: invalid argument %q: %s", e.op, e.arg, e.detail)
}

func (e *argError) Unwrap() error { return ErrInvalidArgument }

// invalidArg builds the error every validation path returns. op is the
// exported method name ("New", "FillCircle"), arg names the offending
// argument as the caller wrote it ("radius", "buffer.Stride"), and detail
// states the constraint and the value received.
func invalidArg(op, arg, detail string) error {
	return &argError{op: op, arg: arg, detail: detail}
}
```

- [ ] **Step 5: Write the package doc**

Create `canvas/doc.go`:

```go
// Package canvas is an immediate-mode CPU rasterizer for 2D shapes.
//
// The caller owns the pixels. A Canvas borrows a [Buffer], validates that
// its description is coherent, and from then on writes straight into that
// memory: no copy, no allocation, no internal scene graph, no render
// loop. Each drawing call modifies the buffer immediately and returns.
//
// # Pixel format
//
// Every pixel is a uint32 holding ARGB8888 with premultiplied alpha, so
// its logical value is 0xAARRGGBB. Callers pass straight (non-premultiplied)
// [Color] values; the canvas premultiplies once per operation.
// Compositing happens on sRGB values treated as linear — there is no gamma
// correction, the same tradeoff Cairo and Skia make by default.
//
// # Coordinates
//
// The API takes logical units. The canvas is built with a logical size, a
// physical size and a scale factor, and multiplies geometry by that scale
// exactly once per shape. Logical sizes are integers because a fractional
// logical size cannot be expressed to a Wayland compositor; shape geometry
// is float32 because subpixel placement is the point.
//
// # Errors
//
// No drawing method returns an error. The first invalid argument is
// stored, every later operation becomes a no-op, and [Canvas.Err] reports
// it — check it once when the frame is done. Every possible error is a
// programming bug rather than a runtime condition, so a per-call error
// return would be thirty unreachable branches in a paint function. Only
// [New] returns an error, because there is no object yet to attach it to.
//
// # Damage
//
// A Canvas accumulates the union of everything it actually wrote, in
// physical pixels, which is exactly what wl_surface.damage_buffer wants.
// See [Canvas.Damage] and [Canvas.ResetDamage].
//
// A Canvas is not safe for concurrent use.
package canvas
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — `TestInvalidArgMessage`, `TestInvalidArgIsErrInvalidArgument`, `TestLineCapConstants`.

Also run: `go vet ./canvas/`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add canvas/types.go canvas/errors.go canvas/doc.go canvas/errors_test.go
git commit -m "canvas: add public data types and the invalid-argument error shape"
```

---

## Task 2: `canvas.go` — the `Canvas` struct, `New`, and the accessors

**Files:**
- Create: `canvas/canvas.go`
- Create: `canvas/testhelpers_test.go`
- Test: `canvas/canvas_test.go`

**Interfaces:**
- Consumes: `Buffer`, `PixelRect`, `invalidArg` (Task 1).
- Produces:
  - `func New(buffer Buffer, width, height int, scale float32) (*Canvas, error)`
  - `func (c *Canvas) Width() int`, `Height() int` — logical
  - `func (c *Canvas) PixelWidth() int`, `PixelHeight() int` — physical
  - `func (c *Canvas) Scale() float32`, `Stride() int`, `Pixels() []uint32`
  - The `Canvas` struct with the fields Tasks 3-10 use: `buf Buffer`, `w, h int`, `scale float32`, `err error`, `dmg PixelRect`, `hasDmg bool`.
  - Test helper `func newTestCanvas(t *testing.T, w, h int, scale float32, stride int) *Canvas` — used by every later test file.

Task 3 implements `Err`/`Damage` over the `err`/`dmg`/`hasDmg` fields declared here, so the struct is complete from this task on and no later task edits it.

- [ ] **Step 1: Write the failing tests**

Create `canvas/canvas_test.go`:

```go
package canvas

import (
	"errors"
	"math"
	"testing"
)

func TestNewValid(t *testing.T) {
	// 800x600 logical at 1.5 => 1200x900 physical, stride padded to 1216.
	px := make([]uint32, 1216*900)
	c, err := New(Buffer{Pixels: px, Width: 1200, Height: 900, Stride: 1216}, 800, 600, 1.5)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if c.Width() != 800 || c.Height() != 600 {
		t.Errorf("logical size = %dx%d, want 800x600", c.Width(), c.Height())
	}
	if c.PixelWidth() != 1200 || c.PixelHeight() != 900 {
		t.Errorf("physical size = %dx%d, want 1200x900", c.PixelWidth(), c.PixelHeight())
	}
	if c.Scale() != 1.5 {
		t.Errorf("Scale() = %v, want 1.5", c.Scale())
	}
	if c.Stride() != 1216 {
		t.Errorf("Stride() = %d, want 1216", c.Stride())
	}
}

func TestNewBorrowsBufferWithoutCopying(t *testing.T) {
	px := make([]uint32, 64*64)
	c, err := New(Buffer{Pixels: px, Width: 64, Height: 64, Stride: 64}, 64, 64, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.Pixels()
	if len(got) != len(px) || cap(got) != cap(px) {
		t.Fatalf("Pixels() len/cap = %d/%d, want %d/%d", len(got), cap(got), len(px), cap(px))
	}
	// Same backing array: writing through one is visible through the other.
	px[10] = 0xDEADBEEF
	if got[10] != 0xDEADBEEF {
		t.Error("Pixels() returned a copy, want the borrowed slice")
	}
}

func TestNewAcceptsSubPixelRoundingDifference(t *testing.T) {
	// 801 logical at 1.5 is 1201.5 physical; both 1201 and 1202 are within
	// the one-pixel tolerance the spec grants platform rounding policies.
	for _, pw := range []int{1201, 1202} {
		px := make([]uint32, pw*10)
		_, err := New(Buffer{Pixels: px, Width: pw, Height: 10, Stride: pw}, 801, 10, 1.5)
		if err != nil {
			t.Errorf("New with physical width %d: unexpected error: %v", pw, err)
		}
	}
}

func TestNewRejectsIncoherentPhysicalSize(t *testing.T) {
	// 800 logical at 1.5 is 1200; 1400 is far outside the tolerance and must
	// not be accepted just because the buffer is big enough.
	px := make([]uint32, 1400*900)
	_, err := New(Buffer{Pixels: px, Width: 1400, Height: 900, Stride: 1400}, 800, 600, 1.5)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New with oversized buffer: err = %v, want ErrInvalidArgument", err)
	}
}

func TestNewRejectsShortBuffer(t *testing.T) {
	// Needs (Height-1)*Stride + Width = 9*64 + 64 = 640 elements.
	px := make([]uint32, 639)
	_, err := New(Buffer{Pixels: px, Width: 64, Height: 10, Stride: 64}, 64, 10, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	want := `canvas: New: invalid argument "buffer.Pixels": buffer is too short (need 640, got 639)`
	if err.Error() != want {
		t.Errorf("err =\n  %q\nwant\n  %q", err, want)
	}
}

func TestNewAcceptsBufferWithoutTrailingPadding(t *testing.T) {
	// The last row needs no padding after it: (10-1)*64 + 60 = 636 suffices
	// even though Stride*Height would be 640.
	px := make([]uint32, 636)
	if _, err := New(Buffer{Pixels: px, Width: 60, Height: 10, Stride: 64}, 60, 10, 1); err != nil {
		t.Errorf("New: unexpected error: %v", err)
	}
}

func TestNewRejectsBadArguments(t *testing.T) {
	ok := Buffer{Pixels: make([]uint32, 64*64), Width: 64, Height: 64, Stride: 64}
	cases := []struct {
		name   string
		buffer Buffer
		w, h   int
		scale  float32
	}{
		{"zero logical width", ok, 0, 64, 1},
		{"zero logical height", ok, 64, 0, 1},
		{"negative logical width", ok, -64, 64, 1},
		{"zero scale", ok, 64, 64, 0},
		{"negative scale", ok, 64, 64, -1},
		{"NaN scale", ok, 64, 64, float32(math.NaN())},
		{"Inf scale", ok, 64, 64, float32(math.Inf(1))},
		{"zero physical width", Buffer{Pixels: ok.Pixels, Width: 0, Height: 64, Stride: 64}, 64, 64, 1},
		{"zero physical height", Buffer{Pixels: ok.Pixels, Width: 64, Height: 0, Stride: 64}, 64, 64, 1},
		{"negative physical width", Buffer{Pixels: ok.Pixels, Width: -64, Height: 64, Stride: 64}, 64, 64, 1},
		{"stride below width", Buffer{Pixels: ok.Pixels, Width: 64, Height: 64, Stride: 63}, 64, 64, 1},
		{"nil pixels", Buffer{Pixels: nil, Width: 64, Height: 64, Stride: 64}, 64, 64, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.buffer, tc.w, tc.h, tc.scale)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
			if c != nil {
				t.Error("New returned a non-nil Canvas alongside an error")
			}
		})
	}
}

func TestNewRejectsOverflowingGeometry(t *testing.T) {
	// (Height-1)*Stride + Width overflows int on 32-bit and is astronomically
	// larger than any real slice on 64-bit; either way it must be rejected
	// as a length problem, never panic and never wrap around to something
	// small enough to pass the check.
	const huge = math.MaxInt32
	c, err := New(Buffer{Pixels: make([]uint32, 16), Width: huge, Height: huge, Stride: huge}, huge, huge, 1)
	if err == nil {
		t.Fatal("New accepted overflowing geometry")
	}
	if c != nil {
		t.Error("New returned a non-nil Canvas alongside an error")
	}
}

func TestNewStrideEqualToWidthIsValid(t *testing.T) {
	px := make([]uint32, 32*16)
	if _, err := New(Buffer{Pixels: px, Width: 32, Height: 16, Stride: 32}, 32, 16, 1); err != nil {
		t.Errorf("New with compact buffer: %v", err)
	}
}
```

Create `canvas/testhelpers_test.go`:

```go
package canvas

import "testing"

// newTestCanvas builds a scale-aware canvas whose physical size is the
// logical size times scale, rounded down, with the requested stride. Pass
// stride 0 for a compact buffer. The pixel slice is deliberately longer
// than required so tests can assert that trailing memory stays untouched.
func newTestCanvas(t *testing.T, w, h int, scale float32, stride int) *Canvas {
	t.Helper()
	pw := int(float32(w) * scale)
	ph := int(float32(h) * scale)
	if stride == 0 {
		stride = pw
	}
	if stride < pw {
		t.Fatalf("newTestCanvas: stride %d below physical width %d", stride, pw)
	}
	c, err := New(
		Buffer{Pixels: make([]uint32, stride*ph), Width: pw, Height: ph, Stride: stride},
		w, h, scale,
	)
	if err != nil {
		t.Fatalf("newTestCanvas: %v", err)
	}
	return c
}

// at returns the pixel at physical coordinates (x, y).
func at(c *Canvas, x, y int) uint32 {
	return c.Pixels()[y*c.Stride()+x]
}

// fillAll writes v into every visible pixel, leaving padding alone. Tests
// use it to establish a known background without going through Clear,
// which is itself under test.
func fillAll(c *Canvas, v uint32) {
	for y := range c.PixelHeight() {
		row := y * c.Stride()
		for x := range c.PixelWidth() {
			c.Pixels()[row+x] = v
		}
	}
}

// paddingIntact reports whether every padding element still holds sentinel.
// Call fillPadding first.
func paddingIntact(c *Canvas, sentinel uint32) bool {
	for y := range c.PixelHeight() {
		row := y * c.Stride()
		for x := c.PixelWidth(); x < c.Stride(); x++ {
			if c.Pixels()[row+x] != sentinel {
				return false
			}
		}
	}
	return true
}

// fillPadding writes sentinel into every padding element.
func fillPadding(c *Canvas, sentinel uint32) {
	for y := range c.PixelHeight() {
		row := y * c.Stride()
		for x := c.PixelWidth(); x < c.Stride(); x++ {
			c.Pixels()[row+x] = sentinel
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run TestNew -v`
Expected: FAIL — build error, `New` and `Canvas` are undefined.

- [ ] **Step 3: Write `New` and the accessors**

Create `canvas/canvas.go`:

```go
package canvas

import (
	"fmt"
	"math"
)

// Canvas draws into borrowed pixel memory. Create one with [New]; the
// buffer, the sizes and the scale are immutable for its lifetime. If any
// of them changes, discard the Canvas and build another over the new
// buffer — the object is cheap precisely because it allocates nothing.
//
// A Canvas is not safe for concurrent use.
type Canvas struct {
	buf Buffer

	// w and h are the logical size. Integers on purpose: with fractional
	// scaling this size ends up in wp_viewport.set_destination, which takes
	// int32, so a canvas 800.5 logical units wide is not expressible to a
	// compositor and must not be constructible here.
	w, h int

	// scale is physical pixels per logical unit. Finite and greater than
	// zero, integral or fractional.
	scale float32

	// err is the first error recorded by any operation. Once set it is
	// never cleared and every later operation is a no-op. See [Canvas.Err].
	err error

	// dmg is the union of the clipped physical bounding boxes of everything
	// actually written since the last reset; hasDmg says whether anything
	// has been written at all. See [Canvas.Damage].
	dmg    PixelRect
	hasDmg bool
}

// New validates the buffer description against the logical size and scale
// and returns a Canvas drawing into buffer.Pixels. The pixels are borrowed:
// New does not copy, allocate, resize or take ownership of them, and the
// caller must keep them valid and mapped for as long as the Canvas is used.
//
// width and height are the logical size in the units every drawing method
// takes; scale is physical pixels per logical unit. The physical size lives
// in buffer.Width and buffer.Height and is chosen by the platform
// integration, which owns the rounding policy — New only checks that it
// matches width and height scaled, to within one physical pixel per axis.
//
// New is the only function in the package that returns an error; drawing
// methods record theirs on the Canvas instead. See [Canvas.Err].
func New(buffer Buffer, width, height int, scale float32) (*Canvas, error) {
	const op = "New"

	if width <= 0 {
		return nil, invalidArg(op, "width", fmt.Sprintf("must be greater than zero (got %d)", width))
	}
	if height <= 0 {
		return nil, invalidArg(op, "height", fmt.Sprintf("must be greater than zero (got %d)", height))
	}
	if !isFinite32(scale) || scale <= 0 {
		return nil, invalidArg(op, "scale", fmt.Sprintf("must be finite and greater than zero (got %v)", scale))
	}
	if buffer.Width <= 0 {
		return nil, invalidArg(op, "buffer.Width", fmt.Sprintf("must be greater than zero (got %d)", buffer.Width))
	}
	if buffer.Height <= 0 {
		return nil, invalidArg(op, "buffer.Height", fmt.Sprintf("must be greater than zero (got %d)", buffer.Height))
	}
	if buffer.Stride < buffer.Width {
		return nil, invalidArg(op, "buffer.Stride", fmt.Sprintf("must be at least buffer.Width (got %d)", buffer.Stride))
	}

	// The physical size must correspond to the logical size scaled, with
	// less than one physical pixel of slack per axis. The tolerance accepts
	// whatever rounding policy the platform applied without making the
	// canvas pick one, and it does not authorize an arbitrarily oversized
	// buffer.
	if d := absf(float32(buffer.Width) - float32(width)*scale); !(d < 1) {
		return nil, invalidArg(op, "buffer.Width", fmt.Sprintf(
			"must be width*scale to within one pixel (got %d, want ~%v)", buffer.Width, float32(width)*scale))
	}
	if d := absf(float32(buffer.Height) - float32(height)*scale); !(d < 1) {
		return nil, invalidArg(op, "buffer.Height", fmt.Sprintf(
			"must be height*scale to within one pixel (got %d, want ~%v)", buffer.Height, float32(height)*scale))
	}

	// Required length stops at the last visible pixel: no padding is needed
	// after the final row. Computed in int64 so a hostile or buggy
	// description cannot wrap around into a small, passing value.
	required64 := int64(buffer.Height-1)*int64(buffer.Stride) + int64(buffer.Width)
	if required64 > int64(math.MaxInt32) && required64 > int64(len(buffer.Pixels)) {
		return nil, invalidArg(op, "buffer.Pixels", fmt.Sprintf(
			"buffer is too short (need %d, got %d)", required64, len(buffer.Pixels)))
	}
	if required64 > int64(len(buffer.Pixels)) {
		return nil, invalidArg(op, "buffer.Pixels", fmt.Sprintf(
			"buffer is too short (need %d, got %d)", required64, len(buffer.Pixels)))
	}

	return &Canvas{buf: buffer, w: width, h: height, scale: scale}, nil
}

// Width returns the logical width the drawing methods take coordinates in.
func (c *Canvas) Width() int { return c.w }

// Height returns the logical height the drawing methods take coordinates in.
func (c *Canvas) Height() int { return c.h }

// Scale returns physical pixels per logical unit.
func (c *Canvas) Scale() float32 { return c.scale }

// PixelWidth returns the visible physical width, the width everything is
// clipped against.
func (c *Canvas) PixelWidth() int { return c.buf.Width }

// PixelHeight returns the visible physical height, the height everything is
// clipped against.
func (c *Canvas) PixelHeight() int { return c.buf.Height }

// Stride returns the distance in uint32 elements between the start of two
// rows. It is at least PixelWidth; the difference is padding the canvas
// never touches.
func (c *Canvas) Stride() int { return c.buf.Stride }

// Pixels returns the borrowed storage, not a copy. It is a deliberate
// escape hatch: whoever writes through it takes on the guarantees the
// canvas otherwise makes on its own — stay inside the visible region,
// leave the padding alone, and only store premultiplied values. Damage
// does not record writes made this way.
func (c *Canvas) Pixels() []uint32 { return c.buf.Pixels }

// isFinite32 reports whether v is neither NaN nor an infinity.
func isFinite32(v float32) bool {
	f := float64(v)
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// absf is math.Abs for float32 without the float64 round trip.
func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
```

Note on the two identical-looking length checks: the first exists so the
overflow case reports the true 64-bit requirement rather than a wrapped
`int`. Collapse them into one `if required64 > int64(len(buffer.Pixels))`
if you prefer — the tests only require that overflowing geometry is
rejected without panicking.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — every `TestNew*` case plus Task 1's tests.

Run: `go vet ./canvas/`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add canvas/canvas.go canvas/canvas_test.go canvas/testhelpers_test.go
git commit -m "canvas: add Canvas, New with full validation, and read-only accessors"
```

---

## Task 3: `state.go` — sticky error and accumulated damage

**Files:**
- Create: `canvas/state.go`
- Test: `canvas/state_test.go`

**Interfaces:**
- Consumes: the `Canvas` struct fields `err`, `dmg`, `hasDmg` (Task 2), `PixelRect` (Task 1).
- Produces:
  - `func (c *Canvas) Err() error`
  - `func (c *Canvas) Damage() (PixelRect, bool)`
  - `func (c *Canvas) ResetDamage()`
  - `func (c *Canvas) fail(err error)` — records the first error; later calls are ignored. Every drawing method in Tasks 5-10 calls this and returns.
  - `func (c *Canvas) addDamage(r PixelRect)` — unions a clipped physical rectangle into the accumulator. Ignores empty rectangles.
  - `func (c *Canvas) clipRect(x0, y0, x1, y1 float32) (PixelRect, bool)` — converts a fractional physical bounding box to the integer pixel range to scan, clipped to the visible region. Returns `false` when nothing is visible. Tasks 5-10 all start their raster loop with this.

`clipRect` lives here rather than in a shape file because every shape needs it and it is where the "damage never leaves the visible region" invariant is enforced once.

- [ ] **Step 1: Write the failing tests**

Create `canvas/state_test.go`:

```go
package canvas

import (
	"errors"
	"testing"
)

func TestErrIsNilOnFreshCanvas(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	if err := c.Err(); err != nil {
		t.Errorf("Err() on a fresh canvas = %v, want nil", err)
	}
}

func TestFailKeepsTheFirstError(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	first := invalidArg("FillRect", "rect.Width", "must not be negative (got -1)")
	second := invalidArg("FillCircle", "radius", "must not be negative (got -2)")

	c.fail(first)
	c.fail(second)

	if got := c.Err(); got != first {
		t.Errorf("Err() = %v, want the first error %v", got, first)
	}
	if !errors.Is(c.Err(), ErrInvalidArgument) {
		t.Error("stored error no longer matches ErrInvalidArgument")
	}
}

func TestFailedCanvasIsPoisonedForever(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.fail(invalidArg("FillRect", "rect.Width", "must not be negative (got -1)"))
	if !c.failed() {
		t.Fatal("failed() = false after fail()")
	}
	// There is no API to clear it: confirm the accessor keeps reporting.
	if c.Err() == nil {
		t.Error("Err() went back to nil")
	}
}

func TestDamageEmptyOnFreshCanvas(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	if _, ok := c.Damage(); ok {
		t.Error("Damage() reported ok on a canvas that has not been written")
	}
}

func TestAddDamageUnionsRectangles(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.addDamage(PixelRect{X: 2, Y: 3, Width: 4, Height: 4}) // x 2..6, y 3..7
	c.addDamage(PixelRect{X: 8, Y: 1, Width: 2, Height: 2}) // x 8..10, y 1..3

	got, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() reported not-ok after two writes")
	}
	want := PixelRect{X: 2, Y: 1, Width: 8, Height: 6} // x 2..10, y 1..7
	if got != want {
		t.Errorf("Damage() = %+v, want %+v", got, want)
	}
}

func TestAddDamageIgnoresEmptyRectangles(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.addDamage(PixelRect{X: 5, Y: 5, Width: 0, Height: 3})
	c.addDamage(PixelRect{X: 5, Y: 5, Width: 3, Height: 0})
	if _, ok := c.Damage(); ok {
		t.Error("empty rectangles extended the damage")
	}
}

func TestResetDamage(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.addDamage(PixelRect{X: 1, Y: 1, Width: 2, Height: 2})
	c.ResetDamage()
	if _, ok := c.Damage(); ok {
		t.Error("Damage() still reports ok after ResetDamage")
	}
	// Accumulation restarts cleanly rather than resurrecting the old box.
	c.addDamage(PixelRect{X: 10, Y: 10, Width: 1, Height: 1})
	got, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok after reset and a new write")
	}
	want := PixelRect{X: 10, Y: 10, Width: 1, Height: 1}
	if got != want {
		t.Errorf("Damage() = %+v, want %+v", got, want)
	}
}

func TestClipRect(t *testing.T) {
	c := newTestCanvas(t, 20, 10, 1, 0) // 20x10 physical
	cases := []struct {
		name           string
		x0, y0, x1, y1 float32
		want           PixelRect
		wantOK         bool
	}{
		{"whole pixels", 2, 3, 6, 7, PixelRect{2, 3, 4, 4}, true},
		{"fractional expands outward", 2.3, 3.7, 5.1, 6.2, PixelRect{2, 3, 4, 4}, true},
		{"clipped left and top", -5, -5, 3, 3, PixelRect{0, 0, 3, 3}, true},
		{"clipped right and bottom", 18, 8, 40, 40, PixelRect{18, 8, 2, 2}, true},
		{"entirely left", -20, 0, -1, 10, PixelRect{}, false},
		{"entirely right", 25, 0, 40, 10, PixelRect{}, false},
		{"entirely above", 0, -20, 20, -1, PixelRect{}, false},
		{"entirely below", 0, 12, 20, 30, PixelRect{}, false},
		{"empty box", 5, 5, 5, 5, PixelRect{}, false},
		{"inverted box", 8, 8, 2, 2, PixelRect{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := c.clipRect(tc.x0, tc.y0, tc.x1, tc.y1)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Errorf("clipRect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestClipRectNeverLeavesVisibleRegion(t *testing.T) {
	c := newTestCanvas(t, 20, 10, 1, 64) // stride 64, visible 20x10
	got, ok := c.clipRect(-1000, -1000, 1000, 1000)
	if !ok {
		t.Fatal("clipRect over the whole plane reported not-ok")
	}
	if got.X < 0 || got.Y < 0 || got.X+got.Width > c.PixelWidth() || got.Y+got.Height > c.PixelHeight() {
		t.Errorf("clipRect = %+v, outside the visible %dx%d region", got, c.PixelWidth(), c.PixelHeight())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run 'TestErr|TestFail|TestDamage|TestAddDamage|TestReset|TestClipRect' -v`
Expected: FAIL — build error, `fail`, `failed`, `addDamage`, `clipRect`, `Err`, `Damage`, `ResetDamage` are undefined.

- [ ] **Step 3: Write the implementation**

Create `canvas/state.go`:

```go
package canvas

import "math"

// Err returns the first error any operation recorded, or nil. Drawing
// methods never return an error: they record it here and turn every later
// operation into a no-op, so a paint function calls Err once when the
// frame is finished instead of branching after each of thirty primitives.
//
// The error is permanent. There is no way to clear it, because every error
// the package can produce is a programming bug — a NaN coordinate, a
// negative radius, an unknown LineCap — and those reproduce on the first
// run rather than depending on runtime conditions. The cost is real and
// worth stating: one bad argument in the first shape leaves the rest of
// the frame unpainted, silently, until this call. Discard a poisoned
// Canvas and build another over the same buffer.
//
//	c.Clear(bg)
//	c.FillRoundedRect(box, 6, panelBG)
//	c.StrokeRect(box, 1, border)
//	if err := c.Err(); err != nil {
//		return err
//	}
func (c *Canvas) Err() error { return c.err }

// fail records err if this is the first one. Drawing methods call it and
// return immediately, before writing any pixel and without touching the
// accumulated damage.
func (c *Canvas) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

// failed reports whether the canvas is poisoned. Every drawing method
// starts with this check.
func (c *Canvas) failed() bool { return c.err != nil }

// Damage returns the union of the physical bounding boxes of everything
// written since the last [Canvas.ResetDamage], and whether anything was
// written at all. The rectangle is in physical pixels, which is exactly
// what wl_surface.damage_buffer takes — no conversion in between.
//
// Operations that write nothing do not extend it: zero alpha, a zero
// dimension, a shape entirely outside the canvas, or any call made while
// the canvas is in error.
//
// Resetting is the caller's job, after the commit, not the canvas's.
//
// Only one rectangle is accumulated, so two small changes in opposite
// corners damage everything between them. A list of rectangles with a
// merge heuristic is the natural evolution, but not before a measured case
// asks for it.
func (c *Canvas) Damage() (PixelRect, bool) {
	if !c.hasDmg {
		return PixelRect{}, false
	}
	return c.dmg, true
}

// ResetDamage forgets the accumulated region. Call it after attaching,
// damaging and committing the buffer.
//
// With a buffer pool, note that each buffer carries its own history: if
// frame N painted buffer A and frame N+1 paints buffer B, B still holds
// frame N-1, so damaging only what changed since frame N leaves B stale.
// Accumulating the last few frames' damage, or repainting the union, is
// the platform layer's decision — the canvas only reports what it touched.
func (c *Canvas) ResetDamage() {
	c.dmg = PixelRect{}
	c.hasDmg = false
}

// addDamage unions an already-clipped physical rectangle into the
// accumulator. Empty rectangles are ignored so a no-op cannot extend the
// damage.
func (c *Canvas) addDamage(r PixelRect) {
	if r.Width <= 0 || r.Height <= 0 {
		return
	}
	if !c.hasDmg {
		c.dmg = r
		c.hasDmg = true
		return
	}
	x0 := min(c.dmg.X, r.X)
	y0 := min(c.dmg.Y, r.Y)
	x1 := max(c.dmg.X+c.dmg.Width, r.X+r.Width)
	y1 := max(c.dmg.Y+c.dmg.Height, r.Y+r.Height)
	c.dmg = PixelRect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// clipRect turns a fractional physical bounding box into the half-open
// integer pixel range to scan, clipped to the visible region. It expands
// outward — floor on the low edge, ceil on the high edge — so a box that
// only grazes a pixel still includes it, and callers add their own
// antialiasing margin before calling.
//
// It returns false when nothing visible remains, which is the single place
// that guarantees no raster loop and no damage rectangle ever leaves the
// visible region.
func (c *Canvas) clipRect(x0, y0, x1, y1 float32) (PixelRect, bool) {
	// NaN fails every comparison below, so an unvalidated NaN reaching here
	// produces an empty range rather than an unbounded loop. Arguments are
	// still validated up front by each operation; this is the backstop.
	if !(x1 > x0) || !(y1 > y0) {
		return PixelRect{}, false
	}

	ix0 := floorToInt(x0)
	iy0 := floorToInt(y0)
	ix1 := ceilToInt(x1)
	iy1 := ceilToInt(y1)

	ix0 = max(ix0, 0)
	iy0 = max(iy0, 0)
	ix1 = min(ix1, c.buf.Width)
	iy1 = min(iy1, c.buf.Height)

	if ix1 <= ix0 || iy1 <= iy0 {
		return PixelRect{}, false
	}
	return PixelRect{X: ix0, Y: iy0, Width: ix1 - ix0, Height: iy1 - iy0}, true
}

// floorToInt clamps as well as floors: a coordinate far outside int range
// must saturate rather than wrap into the visible region.
func floorToInt(v float32) int {
	f := math.Floor(float64(v))
	if f < math.MinInt32 {
		return math.MinInt32
	}
	if f > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(f)
}

// ceilToInt is floorToInt's counterpart for the high edge of a range.
func ceilToInt(v float32) int {
	f := math.Ceil(float64(v))
	if f < math.MinInt32 {
		return math.MinInt32
	}
	if f > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(f)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-3.

- [ ] **Step 5: Commit**

```bash
git add canvas/state.go canvas/state_test.go
git commit -m "canvas: add sticky error, damage accumulation and bounding-box clipping"
```

---

## Task 4: `compose.go` — the single compositor

**Files:**
- Create: `canvas/compose.go`
- Test: `canvas/compose_test.go`

**Interfaces:**
- Consumes: `Color` (Task 1), the `Canvas` struct (Task 2).
- Produces:
  - `func mul8(a, b uint32) uint32`
  - `func premultiply(c Color) uint32`
  - `func coverage8(cov float32) uint32`
  - `func (c *Canvas) blendPixel(i int, src, cov uint32)` — source-over, the path every `Fill*`/`Stroke*`/`Line` pixel takes.
  - `func (c *Canvas) replacePixel(i int, src, cov uint32)` — linear interpolation toward src, the path `Clear`/`ClearRect` take.

This is the task the whole package is organized around. Every shape ends here, so the premultiplied source-over formula exists exactly once; when text arrives, `x/image/vector` rasterizes glyphs to an `*image.Alpha` coverage mask, which is the same color+coverage pair per pixel a circle produces, and `DrawMask` becomes a reuse of `blendPixel` rather than a second compositor with its own premultiplication bugs. `DrawMask` is **not** part of this plan; only the internal entry point it would use.

- [ ] **Step 1: Write the failing tests**

Create `canvas/compose_test.go`:

```go
package canvas

import "testing"

func TestMul8ExactAtExtremes(t *testing.T) {
	// The approximation must be exact at the endpoints, or opaque fills stop
	// being opaque and transparent ones stop being invisible.
	cases := []struct{ a, b, want uint32 }{
		{255, 255, 255},
		{255, 0, 0},
		{0, 255, 0},
		{0, 0, 0},
		{128, 255, 128},
		{255, 128, 128},
	}
	for _, c := range cases {
		if got := mul8(c.a, c.b); got != c.want {
			t.Errorf("mul8(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestMul8ApproximatesExactDivision(t *testing.T) {
	// Every input pair must land within one of the true a*b/255.
	for a := uint32(0); a <= 255; a++ {
		for b := uint32(0); b <= 255; b++ {
			got := mul8(a, b)
			want := (a*b + 127) / 255
			diff := int(got) - int(want)
			if diff < -1 || diff > 1 {
				t.Fatalf("mul8(%d, %d) = %d, want within 1 of %d", a, b, got, want)
			}
			if got > 255 {
				t.Fatalf("mul8(%d, %d) = %d, out of 8-bit range", a, b, got)
			}
		}
	}
}

func TestPremultiply(t *testing.T) {
	cases := []struct {
		name string
		in   Color
		want uint32
	}{
		{"opaque red", Color{R: 255, A: 255}, 0xFFFF0000},
		{"opaque white", Color{R: 255, G: 255, B: 255, A: 255}, 0xFFFFFFFF},
		{"fully transparent", Color{R: 255, G: 255, B: 255, A: 0}, 0x00000000},
		// The spec's worked example: ~50% red is 0x80800000, not 0x80FF0000.
		{"half red", Color{R: 255, A: 128}, 0x80800000},
		{"zero value", Color{}, 0x00000000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := premultiply(c.in); got != c.want {
				t.Errorf("premultiply(%+v) = %#08x, want %#08x", c.in, got, c.want)
			}
		})
	}
}

func TestCoverage8(t *testing.T) {
	cases := []struct {
		in   float32
		want uint32
	}{
		{0, 0},
		{1, 255},
		{0.5, 128},
		{-1, 0},   // clamped, not wrapped
		{2, 255},  // clamped, not wrapped
	}
	for _, c := range cases {
		if got := coverage8(c.in); got != c.want {
			t.Errorf("coverage8(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBlendPixelFullCoverageOpaque(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF000000) // opaque black
	c.blendPixel(0, premultiply(Color{R: 255, G: 255, B: 255, A: 255}), 255)
	if got := c.Pixels()[0]; got != 0xFFFFFFFF {
		t.Errorf("opaque white over black = %#08x, want 0xFFFFFFFF", got)
	}
}

func TestBlendPixelZeroCoverageIsNoOp(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF123456)
	c.blendPixel(0, premultiply(Color{R: 255, A: 255}), 0)
	if got := c.Pixels()[0]; got != 0xFF123456 {
		t.Errorf("zero coverage changed the pixel to %#08x", got)
	}
}

// TestBlendPixelCoverageScalesAllChannels is the halo test. Compositing a
// premultiplied source at partial coverage must scale R, G and B along with
// A. Scaling only A leaves the color too bright for its new alpha, and on a
// dark background that shows up as a light fringe around every antialiased
// edge. On a white background the bug is invisible, which is exactly why
// this test uses black.
func TestBlendPixelCoverageScalesAllChannels(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF000000) // opaque black

	src := premultiply(Color{R: 255, G: 255, B: 255, A: 255}) // 0xFFFFFFFF
	c.blendPixel(0, src, 128)                                 // ~50% coverage

	got := c.Pixels()[0]
	a := got >> 24 & 0xFF
	r := got >> 16 & 0xFF
	g := got >> 8 & 0xFF
	b := got & 0xFF

	if a != 255 {
		t.Errorf("alpha = %d, want 255 (opaque source over opaque dest)", a)
	}
	// White at half coverage over black must be mid grey, not near-white.
	for name, v := range map[string]uint32{"R": r, "G": g, "B": b} {
		if v < 120 || v > 136 {
			t.Errorf("%s = %d, want ~128; the halo bug (scaling only alpha) gives 255", name, v)
		}
	}
	// And the result must stay a valid premultiplied value.
	if r > a || g > a || b > a {
		t.Errorf("result %#08x is not premultiplied: a channel exceeds alpha", got)
	}
}

func TestBlendPixelSourceOverSemiTransparent(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF0000FF) // opaque blue

	// 50% opaque red at full coverage over opaque blue: alpha stays 255,
	// red comes up to ~128, blue drops to ~127.
	c.blendPixel(0, premultiply(Color{R: 255, A: 128}), 255)

	got := c.Pixels()[0]
	a, r, g, b := got>>24&0xFF, got>>16&0xFF, got>>8&0xFF, got&0xFF
	if a != 255 {
		t.Errorf("alpha = %d, want 255", a)
	}
	if r < 120 || r > 136 {
		t.Errorf("red = %d, want ~128", r)
	}
	if g != 0 {
		t.Errorf("green = %d, want 0", g)
	}
	if b < 119 || b > 135 {
		t.Errorf("blue = %d, want ~127", b)
	}
}

func TestBlendPixelOntoTransparentDestination(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0x00000000)
	c.blendPixel(0, premultiply(Color{R: 255, G: 255, B: 255, A: 255}), 128)
	got := c.Pixels()[0]
	a, r := got>>24&0xFF, got>>16&0xFF
	if a < 120 || a > 136 {
		t.Errorf("alpha = %d, want ~128", a)
	}
	if r != a {
		t.Errorf("premultiplied white must have r == a; got r=%d a=%d", r, a)
	}
}

func TestReplacePixel(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)

	// Full coverage replaces outright, ignoring source-over: clearing with a
	// fully transparent color must produce 0x00000000, not leave white.
	c.replacePixel(0, 0x00000000, 255)
	if got := c.Pixels()[0]; got != 0x00000000 {
		t.Errorf("full-coverage clear = %#08x, want 0x00000000", got)
	}

	// Zero coverage leaves the destination alone.
	c.replacePixel(1, 0x00000000, 0)
	if got := c.Pixels()[1]; got != 0xFFFFFFFF {
		t.Errorf("zero-coverage clear = %#08x, want the old value", got)
	}

	// Partial coverage interpolates linearly between the two, which is not
	// what source-over would do: source-over with a transparent source is a
	// no-op, while a half-covered clear must halve the destination.
	c.replacePixel(2, 0x00000000, 128)
	got := c.Pixels()[2]
	a := got >> 24 & 0xFF
	if a < 120 || a > 136 {
		t.Errorf("half-coverage clear alpha = %d, want ~128 (source-over would leave 255)", a)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run 'TestMul8|TestPremultiply|TestCoverage8|TestBlendPixel|TestReplacePixel' -v`
Expected: FAIL — build error, `mul8`, `premultiply`, `coverage8`, `blendPixel`, `replacePixel` are undefined.

- [ ] **Step 3: Write the compositor**

Create `canvas/compose.go`:

```go
package canvas

// mul8 multiplies two 8-bit values as if they were fractions of 255. It is
// the standard approximation of (a*b)/255: exact at 0 and 255, within one
// everywhere else, and free of division.
func mul8(a, b uint32) uint32 {
	t := a*b + 0x80
	return (t + (t >> 8)) >> 8
}

// premultiply converts a straight [Color] into the packed premultiplied
// 0xAARRGGBB the buffer stores. Each operation calls it once, before its
// raster loop, never per pixel.
func premultiply(c Color) uint32 {
	a := uint32(c.A)
	r := mul8(uint32(c.R), a)
	g := mul8(uint32(c.G), a)
	b := mul8(uint32(c.B), a)
	return a<<24 | r<<16 | g<<8 | b
}

// coverage8 quantizes geometric coverage in [0,1] to [0,255]. Out-of-range
// input is clamped rather than allowed to wrap: an SDF evaluated at a
// degenerate shape can produce values slightly outside the interval, and a
// wrap there would paint a bright pixel in the middle of a smooth edge.
// NaN fails both comparisons and falls through to zero.
func coverage8(cov float32) uint32 {
	if !(cov > 0) {
		return 0
	}
	if cov >= 1 {
		return 255
	}
	return uint32(cov*255 + 0.5)
}

// blendPixel composites a premultiplied source over the pixel at index i
// with the given coverage, the source-over every Fill, Stroke and Line
// pixel goes through.
//
// Coverage scales all four channels, not just alpha:
//
//	src' = (Sr·cov, Sg·cov, Sb·cov, Sa·cov)
//	dst  = src' + dst·(1 − Sa')
//
// That is the direct consequence of working premultiplied and the classic
// mistake with the format. Scaling only A leaves RGB too bright for its new
// alpha and produces a light halo on every antialiased edge — invisible
// against white, obvious against black.
func (c *Canvas) blendPixel(i int, src, cov uint32) {
	if cov == 0 {
		return
	}

	sa := src >> 24 & 0xFF
	sr := src >> 16 & 0xFF
	sg := src >> 8 & 0xFF
	sb := src & 0xFF

	if cov != 255 {
		sa = mul8(sa, cov)
		sr = mul8(sr, cov)
		sg = mul8(sg, cov)
		sb = mul8(sb, cov)
	}

	// Fully opaque after coverage: nothing of the destination survives, so
	// skip the read-modify-write entirely.
	if sa == 255 {
		c.buf.Pixels[i] = sa<<24 | sr<<16 | sg<<8 | sb
		return
	}
	if sa == 0 && sr == 0 && sg == 0 && sb == 0 {
		return
	}

	dst := c.buf.Pixels[i]
	inv := 255 - sa
	da := sa + mul8(dst>>24&0xFF, inv)
	dr := sr + mul8(dst>>16&0xFF, inv)
	dg := sg + mul8(dst>>8&0xFF, inv)
	db := sb + mul8(dst&0xFF, inv)

	c.buf.Pixels[i] = da<<24 | dr<<16 | dg<<8 | db
}

// replacePixel writes src over the pixel at index i, interpolating toward
// it by cov. This is what Clear and ClearRect use: clearing replaces
// content rather than compositing, so clearing with a fully transparent
// color yields 0x00000000 instead of leaving the old pixel untouched.
//
// At a subpixel boundary the two are mixed linearly:
//
//	dst = lerp(dst, src, cov)
//
// which is a plain interpolation, deliberately not a source-over.
func (c *Canvas) replacePixel(i int, src, cov uint32) {
	if cov == 0 {
		return
	}
	if cov == 255 {
		c.buf.Pixels[i] = src
		return
	}

	dst := c.buf.Pixels[i]
	inv := 255 - cov

	a := mul8(src>>24&0xFF, cov) + mul8(dst>>24&0xFF, inv)
	r := mul8(src>>16&0xFF, cov) + mul8(dst>>16&0xFF, inv)
	g := mul8(src>>8&0xFF, cov) + mul8(dst>>8&0xFF, inv)
	b := mul8(src&0xFF, cov) + mul8(dst&0xFF, inv)

	c.buf.Pixels[i] = a<<24 | r<<16 | g<<8 | b
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-4. `TestMul8ApproximatesExactDivision` walks all 65 536 pairs and still runs in milliseconds.

- [ ] **Step 5: Commit**

```bash
git add canvas/compose.go canvas/compose_test.go
git commit -m "canvas: add the single compositor (mul8, premultiply, blend, replace)"
```

---

## Task 5: `validate.go` + `rect.go` — argument validation and `FillRect`

**Files:**
- Create: `canvas/validate.go`
- Create: `canvas/rect.go`
- Test: `canvas/rect_test.go`

**Interfaces:**
- Consumes: `invalidArg`, `isFinite32` (Tasks 1-2), `clipRect`, `fail`, `failed`, `addDamage` (Task 3), `premultiply`, `coverage8`, `blendPixel` (Task 4).
- Produces:
  - `func finite(op, arg string, v float32) error`
  - `func nonNegative(op, arg string, v float32) error`
  - `func measure(op, arg string, v float32) error` — finite **and** non-negative, what every radius and stroke width needs.
  - `func validRect(op string, r Rect) error`
  - `func validPoint(op, arg string, p Point) error`
  - `func axisCoverage(lo, hi float32, i int) float32` — exact 1-D coverage, reused by `StrokeRect` (Task 6) and `ClearRect` (Task 7).
  - `func (c *Canvas) FillRect(rect Rect, color Color)`

This is the first task that draws, so it is where the eleven-step operation pipeline from the spec gets its reference implementation: validate, bail if poisoned, scale once, compute the bounding box, clip, premultiply, scan, cover, index, composite, union the damage. Tasks 6-10 all follow the same shape.

- [ ] **Step 1: Write the failing tests**

Create `canvas/rect_test.go`:

```go
package canvas

import (
	"errors"
	"math"
	"testing"
)

func TestAxisCoverage(t *testing.T) {
	cases := []struct {
		name   string
		lo, hi float32
		i      int
		want   float32
	}{
		{"pixel fully inside", 0, 10, 5, 1},
		{"pixel fully outside left", 4, 10, 2, 0},
		{"pixel fully outside right", 0, 4, 6, 0},
		{"left edge half", 2.5, 10, 2, 0.5},
		{"right edge quarter", 0, 6.25, 6, 0.25},
		{"span narrower than a pixel", 3.25, 3.75, 3, 0.5},
		{"exact pixel boundary low", 3, 10, 3, 1},
		{"exact pixel boundary high", 0, 3, 3, 0},
		{"empty span", 5, 5, 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := axisCoverage(tc.lo, tc.hi, tc.i)
			if absf(got-tc.want) > 1e-6 {
				t.Errorf("axisCoverage(%v, %v, %d) = %v, want %v", tc.lo, tc.hi, tc.i, got, tc.want)
			}
		})
	}
}

func TestFillRectWholePixelsOpaque(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillRect(Rect{X: 2, Y: 3, Width: 4, Height: 2}, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	for y := range 10 {
		for x := range 10 {
			inside := x >= 2 && x < 6 && y >= 3 && y < 5
			want := uint32(0xFF000000)
			if inside {
				want = 0xFFFFFFFF
			}
			if got := at(c, x, y); got != want {
				t.Errorf("pixel (%d,%d) = %#08x, want %#08x", x, y, got, want)
			}
		}
	}
}

func TestFillRectAppliesScale(t *testing.T) {
	// 10x10 logical at scale 2 => 20x20 physical. A logical 1,1..3,3 square
	// must land on physical 2,2..6,6 with sharp edges.
	c := newTestCanvas(t, 10, 10, 2, 0)
	fillAll(c, 0xFF000000)
	c.FillRect(Rect{X: 1, Y: 1, Width: 2, Height: 2}, Color{R: 255, G: 255, B: 255, A: 255})

	if got := at(c, 2, 2); got != 0xFFFFFFFF {
		t.Errorf("physical (2,2) = %#08x, want opaque white", got)
	}
	if got := at(c, 5, 5); got != 0xFFFFFFFF {
		t.Errorf("physical (5,5) = %#08x, want opaque white", got)
	}
	if got := at(c, 6, 6); got != 0xFF000000 {
		t.Errorf("physical (6,6) = %#08x, want untouched black", got)
	}
	if got := at(c, 1, 1); got != 0xFF000000 {
		t.Errorf("physical (1,1) = %#08x, want untouched black", got)
	}
}

func TestFillRectSubPixelCoverageIsExact(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	// Covers exactly half of column 2 and all of rows 0..1.
	c.FillRect(Rect{X: 2.5, Y: 0, Width: 1.5, Height: 2}, Color{R: 255, G: 255, B: 255, A: 255})

	half := at(c, 2, 0) >> 16 & 0xFF
	if half < 120 || half > 136 {
		t.Errorf("half-covered pixel red = %d, want ~128", half)
	}
	if got := at(c, 3, 0); got != 0xFFFFFFFF {
		t.Errorf("fully covered pixel = %#08x, want opaque white", got)
	}
	if got := at(c, 4, 0); got != 0xFF000000 {
		t.Errorf("uncovered pixel = %#08x, want untouched", got)
	}
}

func TestFillRectRespectsStridePadding(t *testing.T) {
	c := newTestCanvas(t, 8, 4, 1, 32) // visible 8x4, stride 32
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.FillRect(Rect{X: 0, Y: 0, Width: 8, Height: 4}, Color{R: 255, A: 255})

	if !paddingIntact(c, sentinel) {
		t.Error("FillRect wrote into the row padding")
	}
	if got := at(c, 7, 3); got != 0xFFFF0000 {
		t.Errorf("last visible pixel = %#08x, want opaque red", got)
	}
}

func TestFillRectClipsToCanvas(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFF000000)
	// Straddles the top-left corner.
	c.FillRect(Rect{X: -4, Y: -4, Width: 6, Height: 6}, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("a partially offscreen rectangle is not an error, got %v", err)
	}
	if got := at(c, 0, 0); got != 0xFFFFFFFF {
		t.Errorf("(0,0) = %#08x, want the visible part painted", got)
	}
	if got := at(c, 2, 2); got != 0xFF000000 {
		t.Errorf("(2,2) = %#08x, want untouched", got)
	}
	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok after a partially visible fill")
	}
	want := PixelRect{X: 0, Y: 0, Width: 2, Height: 2}
	if dmg != want {
		t.Errorf("Damage() = %+v, want %+v (clipped, never negative)", dmg, want)
	}
}

func TestFillRectFullyOutsideIsNotAnError(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillRect(Rect{X: 100, Y: 100, Width: 4, Height: 4}, Color{R: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if _, ok := c.Damage(); ok {
		t.Error("a fully offscreen rectangle extended the damage")
	}
	if got := at(c, 0, 0); got != 0xFF000000 {
		t.Errorf("(0,0) = %#08x, want untouched", got)
	}
}

func TestFillRectNoOps(t *testing.T) {
	cases := []struct {
		name  string
		rect  Rect
		color Color
	}{
		{"zero width", Rect{X: 1, Y: 1, Width: 0, Height: 4}, Color{R: 255, A: 255}},
		{"zero height", Rect{X: 1, Y: 1, Width: 4, Height: 0}, Color{R: 255, A: 255}},
		{"zero alpha", Rect{X: 1, Y: 1, Width: 4, Height: 4}, Color{R: 255, A: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 8, 8, 1, 0)
			fillAll(c, 0xFF000000)
			c.FillRect(tc.rect, tc.color)

			if err := c.Err(); err != nil {
				t.Fatalf("a no-op must not be an error, got %v", err)
			}
			if _, ok := c.Damage(); ok {
				t.Error("a no-op extended the damage")
			}
			for y := range 8 {
				for x := range 8 {
					if at(c, x, y) != 0xFF000000 {
						t.Fatalf("a no-op wrote pixel (%d,%d)", x, y)
					}
				}
			}
		})
	}
}

func TestFillRectInvalidArguments(t *testing.T) {
	cases := []struct {
		name string
		rect Rect
		want string
	}{
		{
			"negative width", Rect{X: 1, Y: 1, Width: -4, Height: 4},
			`canvas: FillRect: invalid argument "rect.Width": must not be negative (got -4)`,
		},
		{
			"negative height", Rect{X: 1, Y: 1, Width: 4, Height: -4},
			`canvas: FillRect: invalid argument "rect.Height": must not be negative (got -4)`,
		},
		{
			"NaN x", Rect{X: float32(math.NaN()), Y: 1, Width: 4, Height: 4},
			`canvas: FillRect: invalid argument "rect.X": must be finite (got NaN)`,
		},
		{
			"Inf height", Rect{X: 1, Y: 1, Width: 4, Height: float32(math.Inf(1))},
			`canvas: FillRect: invalid argument "rect.Height": must be finite (got +Inf)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 8, 8, 1, 0)
			fillAll(c, 0xFF000000)
			c.FillRect(tc.rect, Color{R: 255, A: 255})

			err := c.Err()
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Err() = %v, want ErrInvalidArgument", err)
			}
			if err.Error() != tc.want {
				t.Errorf("Err() =\n  %q\nwant\n  %q", err, tc.want)
			}
			if _, ok := c.Damage(); ok {
				t.Error("an invalid call extended the damage")
			}
			for y := range 8 {
				for x := range 8 {
					if at(c, x, y) != 0xFF000000 {
						t.Fatalf("an invalid call wrote pixel (%d,%d)", x, y)
					}
				}
			}
		})
	}
}

func TestFillRectStickyErrorSuppressesLaterDrawing(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFF000000)

	c.FillRect(Rect{X: 0, Y: 0, Width: -1, Height: 1}, Color{R: 255, A: 255})
	first := c.Err()
	if first == nil {
		t.Fatal("the invalid call recorded no error")
	}

	// A perfectly valid call afterwards must do nothing at all.
	c.FillRect(Rect{X: 0, Y: 0, Width: 8, Height: 8}, Color{R: 255, G: 255, B: 255, A: 255})

	if c.Err() != first {
		t.Errorf("Err() = %v, want the first error preserved", c.Err())
	}
	if got := at(c, 4, 4); got != 0xFF000000 {
		t.Errorf("a poisoned canvas painted pixel (4,4) = %#08x", got)
	}
	if _, ok := c.Damage(); ok {
		t.Error("a poisoned canvas extended the damage")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run 'TestAxisCoverage|TestFillRect' -v`
Expected: FAIL — build error, `axisCoverage` and `FillRect` are undefined.

- [ ] **Step 3: Write the validation helpers**

Create `canvas/validate.go`:

```go
package canvas

import "fmt"

// finite rejects NaN and infinities. Every float32 argument goes through it
// before anything is scaled, because a non-finite value poisons the whole
// bounding-box computation downstream.
func finite(op, arg string, v float32) error {
	if !isFinite32(v) {
		return invalidArg(op, arg, fmt.Sprintf("must be finite (got %v)", v))
	}
	return nil
}

// nonNegative rejects values below zero. Zero itself is never an error: it
// is the documented no-op for dimensions, radii and stroke widths, so a
// theme with radius 0 does not force a branch at the call site.
func nonNegative(op, arg string, v float32) error {
	if v < 0 {
		return invalidArg(op, arg, fmt.Sprintf("must not be negative (got %v)", v))
	}
	return nil
}

// measure is the pair of checks every radius and stroke width needs.
func measure(op, arg string, v float32) error {
	if err := finite(op, arg, v); err != nil {
		return err
	}
	return nonNegative(op, arg, v)
}

// validRect checks all four fields of a logical rectangle. Field names in
// the error match what the caller wrote, so "rect.Width", not "width".
func validRect(op string, r Rect) error {
	if err := finite(op, "rect.X", r.X); err != nil {
		return err
	}
	if err := finite(op, "rect.Y", r.Y); err != nil {
		return err
	}
	if err := measure(op, "rect.Width", r.Width); err != nil {
		return err
	}
	return measure(op, "rect.Height", r.Height)
}

// validPoint checks both coordinates of a point. arg is the parameter name
// as declared ("from", "to", "center"), and the error names the component.
func validPoint(op, arg string, p Point) error {
	if err := finite(op, arg+".X", p.X); err != nil {
		return err
	}
	return finite(op, arg+".Y", p.Y)
}
```

- [ ] **Step 4: Write `axisCoverage` and `FillRect`**

Create `canvas/rect.go`:

```go
package canvas

// axisCoverage returns the length of the overlap between the span [lo, hi)
// and the unit interval [i, i+1) — the exact one-dimensional coverage of
// pixel i.
//
// For an axis-aligned rectangle the two-dimensional coverage of a pixel is
// simply the product of its two axis coverages, so rectangles are rendered
// with the true covered area rather than the distance approximation the
// curved shapes use.
func axisCoverage(lo, hi float32, i int) float32 {
	a := float32(i)
	b := a + 1
	if lo > a {
		a = lo
	}
	if hi < b {
		b = hi
	}
	if b <= a {
		return 0
	}
	return b - a
}

// FillRect fills an axis-aligned rectangle, given in logical units and
// anchored at its top-left corner, compositing source-over.
//
// Coverage is exact: fractional edges are antialiased by the true covered
// area of each pixel, not an approximation.
//
// A zero width or height is a no-op, as is a fully transparent color;
// neither writes pixels nor extends the damage. Negative dimensions and
// non-finite values are errors — see [Canvas.Err]. A rectangle that falls
// partly or entirely outside the canvas is clipped, not rejected.
func (c *Canvas) FillRect(rect Rect, color Color) {
	const op = "FillRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || color.A == 0 {
		return
	}

	// Scale the whole geometry once. Positions and sizes keep their
	// fractions all the way to rasterization: rounding them separately is
	// what produces gaps and inconsistent thicknesses.
	x0 := rect.X * c.scale
	y0 := rect.Y * c.scale
	x1 := (rect.X + rect.Width) * c.scale
	y1 := (rect.Y + rect.Height) * c.scale

	box, ok := c.clipRect(x0, y0, x1, y1)
	if !ok {
		return
	}

	src := premultiply(color)
	c.fillAxisRect(box, x0, y0, x1, y1, src)
	c.addDamage(box)
}

// fillAxisRect composites the axis-aligned physical rectangle
// [x0,x1) x [y0,y1) over the pixels of box with exact coverage. box must
// already be clipped to the visible region.
func (c *Canvas) fillAxisRect(box PixelRect, x0, y0, x1, y1 float32, src uint32) {
	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		covY := axisCoverage(y0, y1, y)
		if covY <= 0 {
			continue
		}
		row := y * c.buf.Stride
		for x := box.X; x < xEnd; x++ {
			covX := axisCoverage(x0, x1, x)
			if covX <= 0 {
				continue
			}
			c.blendPixel(row+x, src, coverage8(covX*covY))
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-5.

Run: `go vet ./canvas/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add canvas/validate.go canvas/rect.go canvas/rect_test.go
git commit -m "canvas: add argument validation and FillRect with exact coverage"
```

---

## Task 6: `rect.go` — `StrokeRect`

**Files:**
- Modify: `canvas/rect.go` (append; do not touch `axisCoverage` or `FillRect`)
- Test: `canvas/rect_test.go` (append)

**Interfaces:**
- Consumes: `axisCoverage`, `validRect`, `measure`, `fillAxisRect` (Task 5) and the Task 3/4 primitives.
- Produces: `func (c *Canvas) StrokeRect(rect Rect, width float32, color Color)`

Borders are drawn entirely **inward**, so applying one never changes a shape's outer bounding box. That makes the stroke the exact set difference of two axis-aligned rectangles, and since both have exact analytic coverage, so does their difference — no approximation, and no double-compositing at the corners where four separate edge rectangles would overlap.

- [ ] **Step 1: Write the failing tests**

Append to `canvas/rect_test.go`:

```go
func TestStrokeRectDrawsInward(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeRect(Rect{X: 2, Y: 2, Width: 6, Height: 6}, 1, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	for y := range 10 {
		for x := range 10 {
			inOuter := x >= 2 && x < 8 && y >= 2 && y < 8
			inHole := x >= 3 && x < 7 && y >= 3 && y < 7
			want := uint32(0xFF000000)
			if inOuter && !inHole {
				want = 0xFFFFFFFF
			}
			if got := at(c, x, y); got != want {
				t.Errorf("pixel (%d,%d) = %#08x, want %#08x", x, y, got, want)
			}
		}
	}
}

func TestStrokeRectBoundingBoxUnchangedByWidth(t *testing.T) {
	// The outer edge stays put no matter how thick the border gets: pixel
	// (1,1) is outside the rectangle and must never be painted.
	for _, w := range []float32{1, 2, 3} {
		c := newTestCanvas(t, 10, 10, 1, 0)
		fillAll(c, 0xFF000000)
		c.StrokeRect(Rect{X: 2, Y: 2, Width: 6, Height: 6}, w, Color{R: 255, A: 255})
		if got := at(c, 1, 1); got != 0xFF000000 {
			t.Errorf("width %v painted outside the rectangle at (1,1): %#08x", w, got)
		}
		if got := at(c, 8, 8); got != 0xFF000000 {
			t.Errorf("width %v painted outside the rectangle at (8,8): %#08x", w, got)
		}
	}
}

func TestStrokeRectThickerThanHalfBecomesSolidFill(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	// The rectangle is 6 wide; a border of 4 leaves no interior at all.
	c.StrokeRect(Rect{X: 2, Y: 2, Width: 6, Height: 6}, 4, Color{R: 255, G: 255, B: 255, A: 255})

	for y := 2; y < 8; y++ {
		for x := 2; x < 8; x++ {
			if got := at(c, x, y); got != 0xFFFFFFFF {
				t.Errorf("pixel (%d,%d) = %#08x, want a solid fill", x, y, got)
			}
		}
	}
}

func TestStrokeRectCornersAreNotDoubleComposited(t *testing.T) {
	// Four overlapping edge rectangles would composite the corners twice and
	// leave them darker than the sides. With a semi-transparent color over a
	// known background, a corner pixel and a side pixel must match exactly.
	c := newTestCanvas(t, 12, 12, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeRect(Rect{X: 2, Y: 2, Width: 8, Height: 8}, 1, Color{R: 255, G: 255, B: 255, A: 128})

	corner := at(c, 2, 2)
	side := at(c, 5, 2)
	if corner != side {
		t.Errorf("corner %#08x != side %#08x: the corner was composited twice", corner, side)
	}
}

func TestStrokeRectSubPixelWidth(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeRect(Rect{X: 2, Y: 2, Width: 6, Height: 6}, 0.5, Color{R: 255, G: 255, B: 255, A: 255})

	// The border covers half of the edge pixel row, so it composites at ~50%.
	edge := at(c, 4, 2) >> 16 & 0xFF
	if edge < 120 || edge > 136 {
		t.Errorf("half-width border pixel red = %d, want ~128", edge)
	}
	if got := at(c, 4, 4); got != 0xFF000000 {
		t.Errorf("interior pixel = %#08x, want untouched", got)
	}
}

func TestStrokeRectNoOps(t *testing.T) {
	cases := []struct {
		name  string
		rect  Rect
		width float32
		color Color
	}{
		{"zero width border", Rect{X: 2, Y: 2, Width: 4, Height: 4}, 0, Color{R: 255, A: 255}},
		{"zero rect width", Rect{X: 2, Y: 2, Width: 0, Height: 4}, 1, Color{R: 255, A: 255}},
		{"zero rect height", Rect{X: 2, Y: 2, Width: 4, Height: 0}, 1, Color{R: 255, A: 255}},
		{"zero alpha", Rect{X: 2, Y: 2, Width: 4, Height: 4}, 1, Color{R: 255, A: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 8, 8, 1, 0)
			fillAll(c, 0xFF000000)
			c.StrokeRect(tc.rect, tc.width, tc.color)

			if err := c.Err(); err != nil {
				t.Fatalf("a no-op must not be an error, got %v", err)
			}
			if _, ok := c.Damage(); ok {
				t.Error("a no-op extended the damage")
			}
			for y := range 8 {
				for x := range 8 {
					if at(c, x, y) != 0xFF000000 {
						t.Fatalf("a no-op wrote pixel (%d,%d)", x, y)
					}
				}
			}
		})
	}
}

func TestStrokeRectRejectsNegativeWidth(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	c.StrokeRect(Rect{X: 1, Y: 1, Width: 4, Height: 4}, -2, Color{R: 255, A: 255})
	want := `canvas: StrokeRect: invalid argument "width": must not be negative (got -2)`
	if got := c.Err(); got == nil || got.Error() != want {
		t.Errorf("Err() = %v, want %q", got, want)
	}
}

func TestStrokeRectDamage(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	c.StrokeRect(Rect{X: 2, Y: 3, Width: 5, Height: 4}, 1, Color{R: 255, A: 255})
	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok after StrokeRect")
	}
	want := PixelRect{X: 2, Y: 3, Width: 5, Height: 4}
	if dmg != want {
		t.Errorf("Damage() = %+v, want %+v (the outer box, borders being inward)", dmg, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run TestStrokeRect -v`
Expected: FAIL — build error, `StrokeRect` is undefined.

- [ ] **Step 3: Write `StrokeRect`**

Append to `canvas/rect.go`:

```go
// StrokeRect draws a border on an axis-aligned rectangle, given in logical
// units, compositing source-over.
//
// The border is drawn entirely inward: the outer bounding box is exactly
// the rectangle passed in, and thickening the border never grows the shape.
// If width exceeds half the shorter side the interior disappears and the
// result is visually a solid fill.
//
// Coverage is exact — the stroke is the set difference of two axis-aligned
// rectangles — so the corners composite once, not twice.
//
// A zero width, a zero rectangle dimension and a fully transparent color
// are all no-ops. Negative values and non-finite values are errors.
func (c *Canvas) StrokeRect(rect Rect, width float32, color Color) {
	const op = "StrokeRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || width == 0 || color.A == 0 {
		return
	}

	ox0 := rect.X * c.scale
	oy0 := rect.Y * c.scale
	ox1 := (rect.X + rect.Width) * c.scale
	oy1 := (rect.Y + rect.Height) * c.scale
	w := width * c.scale

	box, ok := c.clipRect(ox0, oy0, ox1, oy1)
	if !ok {
		return
	}

	src := premultiply(color)

	// The hole is the outer rectangle deflated by the stroke width on every
	// side. When it collapses there is nothing to subtract and the exact
	// fill path handles the whole thing.
	ix0, iy0 := ox0+w, oy0+w
	ix1, iy1 := ox1-w, oy1-w
	if !(ix1 > ix0) || !(iy1 > iy0) {
		c.fillAxisRect(box, ox0, oy0, ox1, oy1, src)
		c.addDamage(box)
		return
	}

	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		covOuterY := axisCoverage(oy0, oy1, y)
		if covOuterY <= 0 {
			continue
		}
		covInnerY := axisCoverage(iy0, iy1, y)
		row := y * c.buf.Stride
		for x := box.X; x < xEnd; x++ {
			covOuterX := axisCoverage(ox0, ox1, x)
			if covOuterX <= 0 {
				continue
			}
			// The hole is a subset of the outer rectangle, so the difference
			// is never negative.
			cov := covOuterX*covOuterY - axisCoverage(ix0, ix1, x)*covInnerY
			c.blendPixel(row+x, src, coverage8(cov))
		}
	}
	c.addDamage(box)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-6.

- [ ] **Step 5: Commit**

```bash
git add canvas/rect.go canvas/rect_test.go
git commit -m "canvas: add StrokeRect as the exact difference of two rectangles"
```

---

## Task 7: `clear.go` — `Clear` and `ClearRect`

**Files:**
- Create: `canvas/clear.go`
- Test: `canvas/clear_test.go`

**Interfaces:**
- Consumes: `axisCoverage`, `validRect` (Task 5), `premultiply`, `coverage8`, `replacePixel` (Task 4), `clipRect`, `addDamage` (Task 3).
- Produces:
  - `func (c *Canvas) Clear(color Color)`
  - `func (c *Canvas) ClearRect(rect Rect, color Color)`

Clearing **replaces** content instead of compositing it, which is the whole reason `replacePixel` exists separately. The consequence that catches people out: a fully transparent color is a no-op for `FillRect` and a meaningful operation for `ClearRect` — the first has nothing to contribute, the second resets pixels to `0x00000000`.

`Clear` takes no argument that can be invalid — every field of `Color` is a `uint8` — so it has no validation beyond the poisoned-canvas check.

- [ ] **Step 1: Write the failing tests**

Create `canvas/clear_test.go`:

```go
package canvas

import (
	"errors"
	"testing"
)

func TestClearReplacesEveryVisiblePixel(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.Clear(Color{R: 0x12, G: 0x34, B: 0x56, A: 0xFF})

	want := uint32(0xFF123456)
	for y := range 4 {
		for x := range 6 {
			if got := at(c, x, y); got != want {
				t.Fatalf("pixel (%d,%d) = %#08x, want %#08x", x, y, got, want)
			}
		}
	}
}

func TestClearWithTransparentProducesZero(t *testing.T) {
	// The distinguishing test: source-over with a transparent source would
	// leave the buffer untouched. Clear must zero it.
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.Clear(Color{})

	for y := range 4 {
		for x := range 4 {
			if got := at(c, x, y); got != 0x00000000 {
				t.Fatalf("pixel (%d,%d) = %#08x, want 0x00000000", x, y, got)
			}
		}
	}
}

func TestClearNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 32)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.Clear(Color{R: 255, A: 255})

	if !paddingIntact(c, sentinel) {
		t.Error("Clear wrote into the row padding")
	}
}

func TestClearDamagesTheWholeVisibleRegion(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 32)
	c.Clear(Color{})
	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok after Clear")
	}
	want := PixelRect{X: 0, Y: 0, Width: 6, Height: 4}
	if dmg != want {
		t.Errorf("Damage() = %+v, want %+v (visible region, not the stride)", dmg, want)
	}
}

func TestClearIsNoOpOnPoisonedCanvas(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillRect(Rect{Width: -1, Height: 1}, Color{A: 255})
	c.Clear(Color{R: 255, G: 255, B: 255, A: 255})

	if got := at(c, 0, 0); got != 0xFF000000 {
		t.Errorf("Clear ran on a poisoned canvas: (0,0) = %#08x", got)
	}
}

func TestClearRectReplacesWholePixels(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.ClearRect(Rect{X: 2, Y: 2, Width: 3, Height: 3}, Color{})

	for y := range 8 {
		for x := range 8 {
			want := uint32(0xFFFFFFFF)
			if x >= 2 && x < 5 && y >= 2 && y < 5 {
				want = 0x00000000
			}
			if got := at(c, x, y); got != want {
				t.Fatalf("pixel (%d,%d) = %#08x, want %#08x", x, y, got, want)
			}
		}
	}
}

func TestClearRectInterpolatesAtSubPixelBoundaries(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	// The left edge lands mid-pixel in column 2.
	c.ClearRect(Rect{X: 2.5, Y: 0, Width: 2, Height: 8}, Color{})

	partial := at(c, 2, 0)
	a := partial >> 24 & 0xFF
	if a < 120 || a > 136 {
		t.Errorf("half-cleared pixel alpha = %d, want ~128 (lerp, not source-over)", a)
	}
	if got := at(c, 3, 0); got != 0x00000000 {
		t.Errorf("fully cleared pixel = %#08x, want 0x00000000", got)
	}
	if got := at(c, 5, 0); got != 0xFFFFFFFF {
		t.Errorf("untouched pixel = %#08x, want white", got)
	}
}

func TestClearRectWithTransparentColorStillWrites(t *testing.T) {
	// Unlike FillRect, a zero-alpha ClearRect is not a no-op: erasing to
	// transparent is the operation's whole purpose.
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.ClearRect(Rect{X: 0, Y: 0, Width: 4, Height: 4}, Color{})

	if got := at(c, 0, 0); got != 0x00000000 {
		t.Errorf("(0,0) = %#08x, want 0x00000000", got)
	}
	if _, ok := c.Damage(); !ok {
		t.Error("a transparent ClearRect did not extend the damage")
	}
}

func TestClearRectAppliesScaleAndClips(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 2, 0) // 16x16 physical
	fillAll(c, 0xFFFFFFFF)
	c.ClearRect(Rect{X: -2, Y: -2, Width: 4, Height: 4}, Color{})

	if err := c.Err(); err != nil {
		t.Fatalf("a partially offscreen ClearRect is not an error: %v", err)
	}
	if got := at(c, 0, 0); got != 0x00000000 {
		t.Errorf("(0,0) = %#08x, want cleared", got)
	}
	if got := at(c, 4, 4); got != 0xFFFFFFFF {
		t.Errorf("(4,4) = %#08x, want untouched", got)
	}
	dmg, _ := c.Damage()
	want := PixelRect{X: 0, Y: 0, Width: 4, Height: 4}
	if dmg != want {
		t.Errorf("Damage() = %+v, want %+v", dmg, want)
	}
}

func TestClearRectNoOpsAndErrors(t *testing.T) {
	t.Run("zero width is a no-op", func(t *testing.T) {
		c := newTestCanvas(t, 4, 4, 1, 0)
		fillAll(c, 0xFFFFFFFF)
		c.ClearRect(Rect{X: 1, Y: 1, Width: 0, Height: 2}, Color{})
		if err := c.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
		if _, ok := c.Damage(); ok {
			t.Error("a no-op extended the damage")
		}
		if got := at(c, 1, 1); got != 0xFFFFFFFF {
			t.Errorf("a no-op wrote (1,1) = %#08x", got)
		}
	})

	t.Run("negative height is an error", func(t *testing.T) {
		c := newTestCanvas(t, 4, 4, 1, 0)
		fillAll(c, 0xFFFFFFFF)
		c.ClearRect(Rect{X: 1, Y: 1, Width: 2, Height: -2}, Color{})
		err := c.Err()
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Err() = %v, want ErrInvalidArgument", err)
		}
		want := `canvas: ClearRect: invalid argument "rect.Height": must not be negative (got -2)`
		if err.Error() != want {
			t.Errorf("Err() =\n  %q\nwant\n  %q", err, want)
		}
		if got := at(c, 1, 1); got != 0xFFFFFFFF {
			t.Error("an invalid ClearRect modified the buffer")
		}
	})
}

func TestClearRectNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 32)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.ClearRect(Rect{X: 0, Y: 0, Width: 6, Height: 4}, Color{})
	if !paddingIntact(c, sentinel) {
		t.Error("ClearRect wrote into the row padding")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run TestClear -v`
Expected: FAIL — build error, `Clear` and `ClearRect` are undefined.

- [ ] **Step 3: Write `Clear` and `ClearRect`**

Create `canvas/clear.go`:

```go
package canvas

// Clear replaces every visible pixel with color. It replaces rather than
// composites, so clearing with a fully transparent color yields
// 0x00000000 instead of leaving the previous contents in place.
//
// The padding between the visible width and the stride is never written.
// Clear always damages the whole visible region.
func (c *Canvas) Clear(color Color) {
	// No validation: every field of Color is a uint8, so there is no
	// argument here that can be invalid.
	if c.failed() {
		return
	}

	src := premultiply(color)
	for y := range c.buf.Height {
		row := y * c.buf.Stride
		line := c.buf.Pixels[row : row+c.buf.Width]
		for i := range line {
			line[i] = src
		}
	}
	c.addDamage(PixelRect{X: 0, Y: 0, Width: c.buf.Width, Height: c.buf.Height})
}

// ClearRect replaces the pixels under an axis-aligned rectangle, given in
// logical units, with color. Like [Canvas.Clear] it replaces rather than
// composites: a fully transparent color erases to 0x00000000, which is why
// this is not the no-op that a transparent [Canvas.FillRect] is.
//
// Pixels fully inside the rectangle are replaced outright. At a subpixel
// boundary the old value and the clear color are mixed linearly by
// coverage — an interpolation, deliberately not a source-over.
//
// A zero width or height is a no-op. Negative dimensions and non-finite
// values are errors. The rectangle is clipped to the canvas.
func (c *Canvas) ClearRect(rect Rect, color Color) {
	const op = "ClearRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 {
		return
	}

	x0 := rect.X * c.scale
	y0 := rect.Y * c.scale
	x1 := (rect.X + rect.Width) * c.scale
	y1 := (rect.Y + rect.Height) * c.scale

	box, ok := c.clipRect(x0, y0, x1, y1)
	if !ok {
		return
	}

	src := premultiply(color)
	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		covY := axisCoverage(y0, y1, y)
		if covY <= 0 {
			continue
		}
		row := y * c.buf.Stride
		for x := box.X; x < xEnd; x++ {
			covX := axisCoverage(x0, x1, x)
			if covX <= 0 {
				continue
			}
			c.replacePixel(row+x, src, coverage8(covX*covY))
		}
	}
	c.addDamage(box)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-7.

- [ ] **Step 5: Commit**

```bash
git add canvas/clear.go canvas/clear_test.go
git commit -m "canvas: add Clear and ClearRect with replace semantics"
```

---

## Task 8: `sdf.go` + `circle.go` — the distance-field machinery and circles

**Files:**
- Create: `canvas/sdf.go`
- Create: `canvas/circle.go`
- Test: `canvas/circle_test.go`

**Interfaces:**
- Consumes: `clipRect`, `addDamage` (Task 3), `premultiply`, `coverage8`, `blendPixel` (Task 4), `validPoint`, `measure` (Task 5), `absf` (Task 2).
- Produces:
  - `type shape interface { signedDistance(x, y float32) float32; bounds() (x0, y0, x1, y1 float32) }`
  - `func sdfFill[S shape](c *Canvas, s S, src uint32) (PixelRect, bool)`
  - `type ring[S shape] struct { inner S; half float32 }` — the inward-stroke wrapper, reused by `StrokeRoundedRect` (Task 9)
  - `func clamp01(v float32) float32`, `func hypotf(x, y float32) float32`
  - `const aaMargin = 1`
  - `type circleShape struct { cx, cy, r float32 }`
  - `func (c *Canvas) FillCircle(center Point, radius float32, color Color)`
  - `func (c *Canvas) StrokeCircle(center Point, radius, width float32, color Color)`

Two design points to get right, because Tasks 9 and 10 inherit both:

**Generics, not closures or interface values.** `sdfFill` is generic over the concrete shape type so the compiler monomorphizes each call site: the `signedDistance` calls devirtualize and the shape value stays on the stack. Passing a `func(x, y float32) float32` closure, or boxing the shape into a `shape` interface value, would put it on the heap and break the zero-allocation requirement that Task 11 tests for.

**One `ring[S]` instead of a stroke variant per shape.** An inward stroke of width `w` is the band where the fill's signed distance lies in `[-w, 0]`, which is `abs(d + w/2) - w/2`. Writing it once means `StrokeCircle` and `StrokeRoundedRect` cannot disagree about what "inward" means.

- [ ] **Step 1: Write the failing tests**

Create `canvas/circle_test.go`:

```go
package canvas

import (
	"errors"
	"math"
	"testing"
)

func TestClamp01(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1},
	}
	for _, c := range cases {
		if got := clamp01(c.in); got != c.want {
			t.Errorf("clamp01(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCircleShapeSignedDistance(t *testing.T) {
	s := circleShape{cx: 10, cy: 10, r: 4}
	cases := []struct {
		name string
		x, y float32
		want float32
	}{
		{"center", 10, 10, -4},
		{"on the boundary", 14, 10, 0},
		{"outside", 16, 10, 2},
		{"inside", 12, 10, -2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.signedDistance(tc.x, tc.y); absf(got-tc.want) > 1e-4 {
				t.Errorf("signedDistance(%v,%v) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}

	x0, y0, x1, y1 := s.bounds()
	if x0 != 6 || y0 != 6 || x1 != 14 || y1 != 14 {
		t.Errorf("bounds() = (%v,%v,%v,%v), want (6,6,14,14)", x0, y0, x1, y1)
	}
}

func TestRingSignedDistance(t *testing.T) {
	// A disc of radius 8 with a 2-wide inward stroke: the band is the set of
	// points 6..8 from the center.
	r := ring[circleShape]{inner: circleShape{cx: 0, cy: 0, r: 8}, half: 1}
	cases := []struct {
		name string
		x    float32
		want float32
	}{
		{"outer edge of the band", 8, 0},
		{"inner edge of the band", 6, 0},
		{"middle of the band", 7, -1},
		{"inside the hole", 4, 2},
		{"outside the disc", 10, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.signedDistance(tc.x, 0); absf(got-tc.want) > 1e-4 {
				t.Errorf("signedDistance(%v,0) = %v, want %v", tc.x, got, tc.want)
			}
		})
	}

	// A stroke never grows the shape, so the band's bounds are the disc's.
	x0, y0, x1, y1 := r.bounds()
	if x0 != -8 || y0 != -8 || x1 != 8 || y1 != 8 {
		t.Errorf("bounds() = (%v,%v,%v,%v), want the inner shape's (-8,-8,8,8)", x0, y0, x1, y1)
	}
}

func TestFillCircleCenterIsOpaqueAndOutsideIsUntouched(t *testing.T) {
	c := newTestCanvas(t, 20, 20, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillCircle(Point{X: 10, Y: 10}, 5, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if got := at(c, 10, 10); got != 0xFFFFFFFF {
		t.Errorf("center = %#08x, want opaque white", got)
	}
	// Well outside the radius and its antialiasing margin.
	if got := at(c, 1, 1); got != 0xFF000000 {
		t.Errorf("far corner = %#08x, want untouched", got)
	}
	// The rim composites partially. Pixel (14,10) has its center at
	// (14.5,10.5), a signed distance of about -0.47, so coverage lands near
	// 0.97 — inside, but not fully covered.
	rim := at(c, 14, 10) >> 16 & 0xFF
	if rim == 0 || rim == 255 {
		t.Errorf("rim pixel red = %d, want a partial value (antialiasing)", rim)
	}
}

func TestFillCircleIsSymmetric(t *testing.T) {
	// The circle is centered on a pixel corner, so the four quadrants must
	// mirror exactly. Asymmetry means the pixel-center offset is wrong.
	c := newTestCanvas(t, 20, 20, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillCircle(Point{X: 10, Y: 10}, 6, Color{R: 255, G: 255, B: 255, A: 255})

	for dy := 1; dy <= 8; dy++ {
		for dx := 1; dx <= 8; dx++ {
			a := at(c, 10-dx, 10-dy)
			b := at(c, 9+dx, 10-dy)
			cc := at(c, 10-dx, 9+dy)
			d := at(c, 9+dx, 9+dy)
			if a != b || a != cc || a != d {
				t.Fatalf("quadrants disagree at offset (%d,%d): %#08x %#08x %#08x %#08x", dx, dy, a, b, cc, d)
			}
		}
	}
}

func TestFillCircleAppliesScale(t *testing.T) {
	c := newTestCanvas(t, 20, 20, 2, 0) // 40x40 physical
	fillAll(c, 0xFF000000)
	c.FillCircle(Point{X: 10, Y: 10}, 5, Color{R: 255, G: 255, B: 255, A: 255})

	// Physically a radius-10 circle at (20,20): (28,20) is inside, (32,20) out.
	if got := at(c, 28, 20); got != 0xFFFFFFFF {
		t.Errorf("physical (28,20) = %#08x, want inside the scaled circle", got)
	}
	if got := at(c, 32, 20); got != 0xFF000000 {
		t.Errorf("physical (32,20) = %#08x, want outside the scaled circle", got)
	}
}

func TestFillCircleClipsAndDamages(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillCircle(Point{X: 0, Y: 0}, 3, Color{R: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("a partially offscreen circle is not an error: %v", err)
	}
	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok")
	}
	if dmg.X < 0 || dmg.Y < 0 || dmg.X+dmg.Width > 10 || dmg.Y+dmg.Height > 10 {
		t.Errorf("Damage() = %+v, outside the visible region", dmg)
	}
}

func TestFillCircleFullyOutside(t *testing.T) {
	c := newTestCanvas(t, 10, 10, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillCircle(Point{X: 100, Y: 100}, 3, Color{R: 255, A: 255})
	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if _, ok := c.Damage(); ok {
		t.Error("a fully offscreen circle extended the damage")
	}
}

func TestFillCircleNoOps(t *testing.T) {
	t.Run("zero radius", func(t *testing.T) {
		c := newTestCanvas(t, 10, 10, 1, 0)
		fillAll(c, 0xFF000000)
		c.FillCircle(Point{X: 5, Y: 5}, 0, Color{R: 255, A: 255})
		if _, ok := c.Damage(); ok {
			t.Error("zero radius extended the damage")
		}
		if got := at(c, 5, 5); got != 0xFF000000 {
			t.Errorf("zero radius wrote (5,5) = %#08x", got)
		}
	})
	t.Run("zero alpha", func(t *testing.T) {
		c := newTestCanvas(t, 10, 10, 1, 0)
		fillAll(c, 0xFF000000)
		c.FillCircle(Point{X: 5, Y: 5}, 3, Color{A: 0})
		if _, ok := c.Damage(); ok {
			t.Error("zero alpha extended the damage")
		}
		if got := at(c, 5, 5); got != 0xFF000000 {
			t.Errorf("zero alpha wrote (5,5) = %#08x", got)
		}
	})
}

func TestFillCircleInvalidArguments(t *testing.T) {
	t.Run("negative radius", func(t *testing.T) {
		c := newTestCanvas(t, 10, 10, 1, 0)
		c.FillCircle(Point{X: 5, Y: 5}, -3, Color{R: 255, A: 255})
		err := c.Err()
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Err() = %v, want ErrInvalidArgument", err)
		}
		want := `canvas: FillCircle: invalid argument "radius": must not be negative (got -3)`
		if err.Error() != want {
			t.Errorf("Err() =\n  %q\nwant\n  %q", err, want)
		}
	})
	t.Run("NaN center", func(t *testing.T) {
		c := newTestCanvas(t, 10, 10, 1, 0)
		c.FillCircle(Point{X: float32(math.NaN()), Y: 5}, 3, Color{R: 255, A: 255})
		want := `canvas: FillCircle: invalid argument "center.X": must be finite (got NaN)`
		if got := c.Err(); got == nil || got.Error() != want {
			t.Errorf("Err() = %v, want %q", got, want)
		}
	})
}

func TestStrokeCircleLeavesAHole(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeCircle(Point{X: 12, Y: 12}, 8, 2, Color{R: 255, G: 255, B: 255, A: 255})

	if got := at(c, 12, 12); got != 0xFF000000 {
		t.Errorf("the middle of the ring = %#08x, want untouched", got)
	}
	// Inside the band: 7 pixels out from the center along x.
	if got := at(c, 19, 12); got == 0xFF000000 {
		t.Error("the band itself was not painted")
	}
	// Outside the disc entirely.
	if got := at(c, 22, 12); got != 0xFF000000 {
		t.Errorf("outside the disc = %#08x, want untouched", got)
	}
}

func TestStrokeCircleDrawsInward(t *testing.T) {
	// Thickening the stroke must not push past the original radius.
	for _, w := range []float32{1, 3, 5} {
		c := newTestCanvas(t, 24, 24, 1, 0)
		fillAll(c, 0xFF000000)
		c.StrokeCircle(Point{X: 12, Y: 12}, 6, w, Color{R: 255, A: 255})
		// (12+7, 12) is a full pixel outside radius 6 and must stay black.
		if got := at(c, 19, 12); got != 0xFF000000 {
			t.Errorf("width %v painted outside the radius: (19,12) = %#08x", w, got)
		}
	}
}

func TestStrokeCircleWiderThanRadiusFillsTheDisc(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeCircle(Point{X: 12, Y: 12}, 5, 20, Color{R: 255, G: 255, B: 255, A: 255})
	if got := at(c, 12, 12); got != 0xFFFFFFFF {
		t.Errorf("center = %#08x, want a solid disc when the stroke exceeds the radius", got)
	}
}

func TestStrokeCircleNoOpsAndErrors(t *testing.T) {
	t.Run("zero width", func(t *testing.T) {
		c := newTestCanvas(t, 12, 12, 1, 0)
		fillAll(c, 0xFF000000)
		c.StrokeCircle(Point{X: 6, Y: 6}, 4, 0, Color{R: 255, A: 255})
		if _, ok := c.Damage(); ok {
			t.Error("zero width extended the damage")
		}
	})
	t.Run("negative width", func(t *testing.T) {
		c := newTestCanvas(t, 12, 12, 1, 0)
		c.StrokeCircle(Point{X: 6, Y: 6}, 4, -1, Color{R: 255, A: 255})
		want := `canvas: StrokeCircle: invalid argument "width": must not be negative (got -1)`
		if got := c.Err(); got == nil || got.Error() != want {
			t.Errorf("Err() = %v, want %q", got, want)
		}
	})
}

func TestCircleNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 12, 12, 1, 40)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.FillCircle(Point{X: 6, Y: 6}, 20, Color{R: 255, A: 255}) // far larger than the canvas
	if !paddingIntact(c, sentinel) {
		t.Error("FillCircle wrote into the row padding")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run 'TestClamp01|TestCircle|TestRing|TestFillCircle|TestStrokeCircle' -v`
Expected: FAIL — build error, `clamp01`, `circleShape`, `ring`, `FillCircle`, `StrokeCircle` are undefined.

- [ ] **Step 3: Write the SDF machinery**

Create `canvas/sdf.go`:

```go
package canvas

import "math"

// shape is anything that can report, for a point in physical pixels, its
// signed distance to its own boundary — negative inside, positive outside —
// together with the physical bounding box that contains it.
//
// Implementations are small value types, never pointers. [sdfFill] is
// generic over the concrete type so each call site is monomorphized: the
// signedDistance calls devirtualize and the shape value stays on the stack.
// Boxing a shape into an interface value, or passing a closure instead,
// would move it to the heap and break the package's zero-allocation
// guarantee for drawing operations.
type shape interface {
	// signedDistance returns the distance from (x, y) to the boundary, in
	// physical pixels, negative inside the shape.
	signedDistance(x, y float32) float32
	// bounds returns the half-open physical box [x0,x1) x [y0,y1) that
	// contains the shape, before any antialiasing margin.
	bounds() (x0, y0, x1, y1 float32)
}

// aaMargin is how far outside its bounds a shape can still tint a pixel.
// Coverage is clamp(0.5-d, 0, 1), which reaches zero half a pixel out, so
// one whole pixel is a comfortable margin.
const aaMargin = 1

// sdfFill rasterizes s, compositing src source-over with coverage derived
// from the signed distance at each pixel center:
//
//	cov = clamp(0.5 - d, 0, 1)
//
// This estimates the covered area, it does not compute it. The error grows
// where the boundary curves sharply within a single pixel — very small
// radii — and it is the same approximation every rasterizer of this class
// makes. It is written down so nobody measures it in six months and
// believes they found a bug. Axis-aligned rectangles do not go through
// here; they get exact coverage from axisCoverage instead.
//
// It returns the clipped box it scanned and whether anything was scanned,
// so the caller can union that into the damage.
func sdfFill[S shape](c *Canvas, s S, src uint32) (PixelRect, bool) {
	x0, y0, x1, y1 := s.bounds()
	box, ok := c.clipRect(x0-aaMargin, y0-aaMargin, x1+aaMargin, y1+aaMargin)
	if !ok {
		return PixelRect{}, false
	}

	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		row := y * c.buf.Stride
		py := float32(y) + 0.5
		for x := box.X; x < xEnd; x++ {
			cov := 0.5 - s.signedDistance(float32(x)+0.5, py)
			if !(cov > 0) {
				continue
			}
			c.blendPixel(row+x, src, coverage8(cov))
		}
	}
	return box, true
}

// ring turns a filled shape into an inward stroke of width 2*half: the
// band running from the shape's boundary to that depth inside it. With d
// the fill's signed distance, the band is where d lies in [-2*half, 0],
// and its own signed distance is abs(d + half) - half.
//
// Strokes are always inward — applying one never grows the outer bounding
// box — which is why bounds is simply the inner shape's.
//
// Callers must not use a ring whose band would swallow the shape entirely
// (2*half at or beyond the shape's deepest point): the deepest point then
// lands exactly on the band boundary and comes out half-covered. Fill the
// shape instead; StrokeCircle and StrokeRoundedRect both do.
type ring[S shape] struct {
	inner S
	// half is half the stroke width, in physical pixels.
	half float32
}

func (r ring[S]) signedDistance(x, y float32) float32 {
	return absf(r.inner.signedDistance(x, y)+r.half) - r.half
}

func (r ring[S]) bounds() (x0, y0, x1, y1 float32) { return r.inner.bounds() }

// clamp01 constrains v to [0,1]. NaN fails both comparisons and yields 0.
func clamp01(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// hypotf is sqrt(x*x + y*y). math.Hypot's overflow-safe scaling is not
// needed here — the inputs are pixel distances — and costs several times
// more per pixel.
func hypotf(x, y float32) float32 {
	return float32(math.Sqrt(float64(x*x + y*y)))
}
```

- [ ] **Step 4: Write the circle**

Create `canvas/circle.go`:

```go
package canvas

// circleShape is a disc in physical pixels.
type circleShape struct {
	cx, cy float32
	r      float32
}

func (s circleShape) signedDistance(x, y float32) float32 {
	return hypotf(x-s.cx, y-s.cy) - s.r
}

func (s circleShape) bounds() (x0, y0, x1, y1 float32) {
	return s.cx - s.r, s.cy - s.r, s.cx + s.r, s.cy + s.r
}

// FillCircle fills a disc given by its center and radius in logical units,
// compositing source-over.
//
// Coverage is approximated from the distance to the boundary rather than
// computed exactly; see the note on [sdfFill]. The approximation is least
// accurate at very small radii.
//
// A zero radius is a no-op, as is a fully transparent color. A negative
// radius and non-finite coordinates are errors. A circle outside the canvas
// is clipped, not rejected.
func (c *Canvas) FillCircle(center Point, radius float32, color Color) {
	const op = "FillCircle"

	if c.failed() {
		return
	}
	if err := validPoint(op, "center", center); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if radius == 0 || color.A == 0 {
		return
	}

	s := circleShape{cx: center.X * c.scale, cy: center.Y * c.scale, r: radius * c.scale}
	if box, ok := sdfFill(c, s, premultiply(color)); ok {
		c.addDamage(box)
	}
}

// StrokeCircle draws a ring on the disc given by center and radius, in
// logical units, compositing source-over.
//
// The stroke goes entirely inward, so the outer edge stays at radius no
// matter how thick it gets. A width at or beyond the radius closes the hole
// and the result is a solid disc.
//
// A zero radius or zero width is a no-op, as is a fully transparent color.
// Negative values and non-finite coordinates are errors.
func (c *Canvas) StrokeCircle(center Point, radius, width float32, color Color) {
	const op = "StrokeCircle"

	if c.failed() {
		return
	}
	if err := validPoint(op, "center", center); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	if radius == 0 || width == 0 || color.A == 0 {
		return
	}

	r := radius * c.scale
	w := width * c.scale
	disc := circleShape{cx: center.X * c.scale, cy: center.Y * c.scale, r: r}
	src := premultiply(color)

	// A band at or beyond the radius leaves no hole. Fill the disc outright
	// rather than clamping the ring: a ring exactly that deep puts its
	// deepest point on the band boundary, which would render the very center
	// at half coverage.
	if w >= r {
		if box, ok := sdfFill(c, disc, src); ok {
			c.addDamage(box)
		}
		return
	}

	if box, ok := sdfFill(c, ring[circleShape]{inner: disc, half: w / 2}, src); ok {
		c.addDamage(box)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-8.

Run: `go vet ./canvas/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add canvas/sdf.go canvas/circle.go canvas/circle_test.go
git commit -m "canvas: add the SDF scan loop, the inward-stroke ring, and circles"
```

---

## Task 9: `roundrect.go` — rounded rectangles

**Files:**
- Create: `canvas/roundrect.go`
- Test: `canvas/roundrect_test.go`

**Interfaces:**
- Consumes: `shape`, `sdfFill`, `ring`, `hypotf` (Task 8), `validRect`, `measure` (Task 5), `FillRect`, `StrokeRect` (Tasks 5-6).
- Produces:
  - `type roundRectShape struct { cx, cy, hw, hh, r float32 }`
  - `func (c *Canvas) FillRoundedRect(rect Rect, radius float32, color Color)`
  - `func (c *Canvas) StrokeRoundedRect(rect Rect, radius, width float32, color Color)`

Radius zero is **valid** and delegates to the unrounded shape, which also means it takes the exact-coverage path rather than the approximation. That is deliberate: a theme with `radius: 0` must not force a branch at the call site, and it should not silently get worse antialiasing than `FillRect` would give.

The radius is clamped to `min(width, height) / 2` before anything is rasterized, so an oversized radius produces a stadium rather than a self-intersecting distance field.

- [ ] **Step 1: Write the failing tests**

Create `canvas/roundrect_test.go`:

```go
package canvas

import (
	"errors"
	"testing"
)

func TestRoundRectShapeSignedDistance(t *testing.T) {
	// 20x10 box centered at (10,10), corner radius 3.
	s := roundRectShape{cx: 10, cy: 10, hw: 10, hh: 5, r: 3}
	cases := []struct {
		name string
		x, y float32
		want float32
	}{
		{"center", 10, 10, -5},
		{"on the right edge", 20, 10, 0},
		{"outside the right edge", 22, 10, 2},
		{"on the top edge", 10, 5, 0},
		// The corner of the bounding box is outside the rounded shape by
		// r*(sqrt(2)-1) measured from the corner arc's center.
		{"bounding-box corner", 20, 5, 3*1.4142136 - 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.signedDistance(tc.x, tc.y); absf(got-tc.want) > 1e-3 {
				t.Errorf("signedDistance(%v,%v) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}

	x0, y0, x1, y1 := s.bounds()
	if x0 != 0 || y0 != 5 || x1 != 20 || y1 != 15 {
		t.Errorf("bounds() = (%v,%v,%v,%v), want (0,5,20,15)", x0, y0, x1, y1)
	}
}

func TestFillRoundedRectCutsTheCorners(t *testing.T) {
	c := newTestCanvas(t, 20, 20, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillRoundedRect(Rect{X: 4, Y: 4, Width: 12, Height: 12}, 4, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if got := at(c, 10, 10); got != 0xFFFFFFFF {
		t.Errorf("center = %#08x, want opaque white", got)
	}
	// The extreme corner pixel of the bounding box is cut away.
	if got := at(c, 4, 4); got != 0xFF000000 {
		t.Errorf("corner (4,4) = %#08x, want cut away by the radius", got)
	}
	// The middle of each edge stays flush with the bounding box.
	if got := at(c, 10, 4); got != 0xFFFFFFFF {
		t.Errorf("top edge midpoint = %#08x, want opaque white", got)
	}
	if got := at(c, 4, 10); got != 0xFFFFFFFF {
		t.Errorf("left edge midpoint = %#08x, want opaque white", got)
	}
}

func TestFillRoundedRectZeroRadiusEqualsFillRect(t *testing.T) {
	rect := Rect{X: 2.5, Y: 3, Width: 6.25, Height: 4}
	color := Color{R: 200, G: 100, B: 50, A: 200}

	rounded := newTestCanvas(t, 16, 16, 1, 0)
	fillAll(rounded, 0xFF000000)
	rounded.FillRoundedRect(rect, 0, color)

	plain := newTestCanvas(t, 16, 16, 1, 0)
	fillAll(plain, 0xFF000000)
	plain.FillRect(rect, color)

	for y := range 16 {
		for x := range 16 {
			if a, b := at(rounded, x, y), at(plain, x, y); a != b {
				t.Fatalf("pixel (%d,%d): rounded %#08x != plain %#08x", x, y, a, b)
			}
		}
	}
	rd, _ := rounded.Damage()
	pd, _ := plain.Damage()
	if rd != pd {
		t.Errorf("damage differs: rounded %+v, plain %+v", rd, pd)
	}
}

func TestFillRoundedRectClampsOversizedRadius(t *testing.T) {
	// A radius past half the shorter side must clamp to a stadium, not blow
	// up the distance field.
	c := newTestCanvas(t, 20, 20, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillRoundedRect(Rect{X: 4, Y: 8, Width: 12, Height: 4}, 100, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("an oversized radius is clamped, not an error: %v", err)
	}
	if got := at(c, 10, 9); got != 0xFFFFFFFF {
		t.Errorf("center = %#08x, want opaque white", got)
	}
	// Nothing escapes the rectangle's bounding box.
	if got := at(c, 10, 7); got != 0xFF000000 {
		t.Errorf("above the rectangle = %#08x, want untouched", got)
	}
	if got := at(c, 10, 12); got != 0xFF000000 {
		t.Errorf("below the rectangle = %#08x, want untouched", got)
	}
}

func TestFillRoundedRectNoOpsAndErrors(t *testing.T) {
	t.Run("zero width is a no-op", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		fillAll(c, 0xFF000000)
		c.FillRoundedRect(Rect{X: 2, Y: 2, Width: 0, Height: 4}, 2, Color{R: 255, A: 255})
		if err := c.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
		if _, ok := c.Damage(); ok {
			t.Error("a no-op extended the damage")
		}
	})
	t.Run("negative radius is an error", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		c.FillRoundedRect(Rect{X: 2, Y: 2, Width: 4, Height: 4}, -2, Color{R: 255, A: 255})
		err := c.Err()
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Err() = %v, want ErrInvalidArgument", err)
		}
		want := `canvas: FillRoundedRect: invalid argument "radius": must not be negative (got -2)`
		if err.Error() != want {
			t.Errorf("Err() =\n  %q\nwant\n  %q", err, want)
		}
	})
}

func TestStrokeRoundedRectLeavesAHoleAndStaysInward(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeRoundedRect(Rect{X: 4, Y: 4, Width: 16, Height: 16}, 4, 2, Color{R: 255, G: 255, B: 255, A: 255})

	if got := at(c, 12, 12); got != 0xFF000000 {
		t.Errorf("the middle = %#08x, want untouched", got)
	}
	if got := at(c, 12, 4); got == 0xFF000000 {
		t.Error("the top edge of the band was not painted")
	}
	if got := at(c, 12, 2); got != 0xFF000000 {
		t.Errorf("above the rectangle = %#08x, want untouched (strokes go inward)", got)
	}
	if got := at(c, 12, 21); got != 0xFF000000 {
		t.Errorf("below the rectangle = %#08x, want untouched (strokes go inward)", got)
	}
}

func TestStrokeRoundedRectZeroRadiusEqualsStrokeRect(t *testing.T) {
	rect := Rect{X: 2, Y: 2, Width: 10, Height: 8}
	color := Color{R: 30, G: 200, B: 90, A: 255}

	rounded := newTestCanvas(t, 16, 16, 1, 0)
	fillAll(rounded, 0xFF000000)
	rounded.StrokeRoundedRect(rect, 0, 1.5, color)

	plain := newTestCanvas(t, 16, 16, 1, 0)
	fillAll(plain, 0xFF000000)
	plain.StrokeRect(rect, 1.5, color)

	for y := range 16 {
		for x := range 16 {
			if a, b := at(rounded, x, y), at(plain, x, y); a != b {
				t.Fatalf("pixel (%d,%d): rounded %#08x != plain %#08x", x, y, a, b)
			}
		}
	}
}

func TestStrokeRoundedRectThickBecomesSolid(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.StrokeRoundedRect(Rect{X: 6, Y: 6, Width: 12, Height: 12}, 3, 20, Color{R: 255, G: 255, B: 255, A: 255})
	if got := at(c, 12, 12); got != 0xFFFFFFFF {
		t.Errorf("center = %#08x, want solid when the stroke swallows the interior", got)
	}
}

func TestStrokeRoundedRectNoOpsAndErrors(t *testing.T) {
	t.Run("zero stroke width", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		fillAll(c, 0xFF000000)
		c.StrokeRoundedRect(Rect{X: 2, Y: 2, Width: 8, Height: 8}, 2, 0, Color{R: 255, A: 255})
		if _, ok := c.Damage(); ok {
			t.Error("zero width extended the damage")
		}
	})
	t.Run("negative stroke width", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		c.StrokeRoundedRect(Rect{X: 2, Y: 2, Width: 8, Height: 8}, 2, -1, Color{R: 255, A: 255})
		want := `canvas: StrokeRoundedRect: invalid argument "width": must not be negative (got -1)`
		if got := c.Err(); got == nil || got.Error() != want {
			t.Errorf("Err() = %v, want %q", got, want)
		}
	})
}

func TestRoundedRectNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 12, 12, 1, 40)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.FillRoundedRect(Rect{X: -5, Y: -5, Width: 40, Height: 40}, 6, Color{R: 255, A: 255})
	if !paddingIntact(c, sentinel) {
		t.Error("FillRoundedRect wrote into the row padding")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run 'TestRoundRect|TestFillRoundedRect|TestStrokeRoundedRect' -v`
Expected: FAIL — build error, `roundRectShape`, `FillRoundedRect`, `StrokeRoundedRect` are undefined.

- [ ] **Step 3: Write the rounded rectangle**

Create `canvas/roundrect.go`:

```go
package canvas

// roundRectShape is an axis-aligned rectangle with all four corners rounded
// by the same radius, expressed around its center in physical pixels. r is
// already clamped to min(hw, hh) by the constructor below.
type roundRectShape struct {
	cx, cy float32 // center
	hw, hh float32 // half extents
	r      float32 // corner radius
}

func (s roundRectShape) signedDistance(x, y float32) float32 {
	// Fold into the first quadrant, shrink the box by the radius, then take
	// the distance to that smaller box and subtract the radius back. The
	// min(max(...), 0) term is what makes the distance correct on the inside
	// as well as the outside.
	qx := absf(x-s.cx) - (s.hw - s.r)
	qy := absf(y-s.cy) - (s.hh - s.r)
	return hypotf(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - s.r
}

func (s roundRectShape) bounds() (x0, y0, x1, y1 float32) {
	return s.cx - s.hw, s.cy - s.hh, s.cx + s.hw, s.cy + s.hh
}

// newRoundRectShape builds the shape for a physical rectangle, clamping the
// corner radius to half the shorter side so an oversized radius yields a
// stadium instead of a self-intersecting distance field.
func newRoundRectShape(x0, y0, x1, y1, radius float32) roundRectShape {
	hw := (x1 - x0) / 2
	hh := (y1 - y0) / 2
	return roundRectShape{
		cx: x0 + hw,
		cy: y0 + hh,
		hw: hw,
		hh: hh,
		r:  min(radius, min(hw, hh)),
	}
}

// FillRoundedRect fills an axis-aligned rectangle with all four corners
// rounded by the same radius, in logical units, compositing source-over.
//
// A zero radius is valid and equivalent to [Canvas.FillRect] — including
// its exact coverage — so a theme whose corner radius is 0 needs no branch
// at the call site. A radius larger than half the shorter side is clamped
// to that maximum.
//
// A zero width or height is a no-op, as is a fully transparent color.
// Negative dimensions or radius, and non-finite values, are errors.
func (c *Canvas) FillRoundedRect(rect Rect, radius float32, color Color) {
	const op = "FillRoundedRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || color.A == 0 {
		return
	}
	if radius == 0 {
		// Arguments are already validated, so this cannot fail; taking the
		// rectangle path also keeps the exact coverage.
		c.FillRect(rect, color)
		return
	}

	s := newRoundRectShape(
		rect.X*c.scale,
		rect.Y*c.scale,
		(rect.X+rect.Width)*c.scale,
		(rect.Y+rect.Height)*c.scale,
		radius*c.scale,
	)
	if box, ok := sdfFill(c, s, premultiply(color)); ok {
		c.addDamage(box)
	}
}

// StrokeRoundedRect draws a border on a rounded rectangle, in logical
// units, compositing source-over.
//
// Like every stroke in this package it is drawn entirely inward, so the
// outer bounding box is exactly the rectangle passed in. A width past the
// available interior closes the hole and the result is a solid fill.
//
// A zero radius is equivalent to [Canvas.StrokeRect]. A zero width, a zero
// rectangle dimension and a fully transparent color are no-ops. Negative
// values and non-finite values are errors.
func (c *Canvas) StrokeRoundedRect(rect Rect, radius, width float32, color Color) {
	const op = "StrokeRoundedRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || width == 0 || color.A == 0 {
		return
	}
	if radius == 0 {
		c.StrokeRect(rect, width, color)
		return
	}

	inner := newRoundRectShape(
		rect.X*c.scale,
		rect.Y*c.scale,
		(rect.X+rect.Width)*c.scale,
		(rect.Y+rect.Height)*c.scale,
		radius*c.scale,
	)

	src := premultiply(color)

	// The shape's deepest point is min(hw, hh) inside its boundary. A band
	// that deep leaves no interior, so fill instead of stroking — the same
	// reasoning as StrokeCircle: a ring exactly that deep would render its
	// deepest point at half coverage.
	w := width * c.scale
	if w >= min(inner.hw, inner.hh) {
		if box, ok := sdfFill(c, inner, src); ok {
			c.addDamage(box)
		}
		return
	}

	if box, ok := sdfFill(c, ring[roundRectShape]{inner: inner, half: w / 2}, src); ok {
		c.addDamage(box)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-9.

- [ ] **Step 5: Commit**

```bash
git add canvas/roundrect.go canvas/roundrect_test.go
git commit -m "canvas: add FillRoundedRect and StrokeRoundedRect"
```

---

## Task 10: `line.go` — lines and the three caps

**Files:**
- Create: `canvas/line.go`
- Test: `canvas/line_test.go`

**Interfaces:**
- Consumes: `shape`, `sdfFill`, `hypotf`, `clamp01` (Task 8), `circleShape` (Task 8), `fillAxisRect`, `clipRect` (Tasks 3, 5), `validPoint`, `measure` (Task 5).
- Produces:
  - `type segmentShape struct { ax, ay, dx, dy, invLen2, half float32 }` — the capsule, `LineCapRound`
  - `type boxShape struct { cx, cy, ux, uy, heT, heS float32 }` — a rectangle in the line's own frame, covering both `LineCapButt` and `LineCapSquare`
  - `func (c *Canvas) Line(from, to Point, width float32, cap LineCap, color Color)`

Butt and square differ only in the half-extent along the axis — square adds half the stroke width at each end — so one rotated-box shape serves both instead of two near-identical distance functions.

The degenerate case where `from == to` needs its own branch: the line's direction is undefined, so there is no local frame to build. Each cap then collapses to the shape it describes — round to a circle, square to an axis-aligned square, butt to nothing at all.

The parameter is named `cap`, shadowing the builtin, because that is the name the spec's public signature uses. The builtin is not needed anywhere in the function.

- [ ] **Step 1: Write the failing tests**

Create `canvas/line_test.go`:

```go
package canvas

import (
	"errors"
	"math"
	"testing"
)

func TestSegmentShapeSignedDistance(t *testing.T) {
	// Horizontal segment from (10,10) to (20,10), stroke width 4.
	s := segmentShape{ax: 10, ay: 10, dx: 10, dy: 0, invLen2: 1.0 / 100, half: 2}
	cases := []struct {
		name string
		x, y float32
		want float32
	}{
		{"on the axis", 15, 10, -2},
		{"on the edge", 15, 12, 0},
		{"outside sideways", 15, 14, 2},
		{"past the end, inside the round cap", 21, 10, -1},
		{"past the end, outside the cap", 23, 10, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.signedDistance(tc.x, tc.y); absf(got-tc.want) > 1e-4 {
				t.Errorf("signedDistance(%v,%v) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestBoxShapeSignedDistanceAndBounds(t *testing.T) {
	// Axis-aligned box centered at (10,10), 12 long by 4 wide.
	s := boxShape{cx: 10, cy: 10, ux: 1, uy: 0, heT: 6, heS: 2}
	cases := []struct {
		name string
		x, y float32
		want float32
	}{
		{"center", 10, 10, -2},
		{"on the long edge", 10, 12, 0},
		{"on the short edge", 16, 10, 0},
		{"past the short edge", 18, 10, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.signedDistance(tc.x, tc.y); absf(got-tc.want) > 1e-4 {
				t.Errorf("signedDistance(%v,%v) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}

	x0, y0, x1, y1 := s.bounds()
	if x0 != 4 || y0 != 8 || x1 != 16 || y1 != 12 {
		t.Errorf("bounds() = (%v,%v,%v,%v), want (4,8,16,12)", x0, y0, x1, y1)
	}

	// Rotated 90 degrees, the axis-aligned extent swaps.
	r := boxShape{cx: 10, cy: 10, ux: 0, uy: 1, heT: 6, heS: 2}
	x0, y0, x1, y1 = r.bounds()
	if x0 != 8 || y0 != 4 || x1 != 12 || y1 != 16 {
		t.Errorf("rotated bounds() = (%v,%v,%v,%v), want (8,4,12,16)", x0, y0, x1, y1)
	}
}

func TestLineButtStopsAtTheEndpoints(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.Line(Point{X: 6, Y: 12}, Point{X: 18, Y: 12}, 4, LineCapButt, Color{R: 255, G: 255, B: 255, A: 255})

	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if got := at(c, 12, 12); got != 0xFFFFFFFF {
		t.Errorf("mid-line = %#08x, want opaque white", got)
	}
	// One pixel before the start and one past the end must stay clear.
	if got := at(c, 4, 12); got != 0xFF000000 {
		t.Errorf("before the start = %#08x, want untouched with a butt cap", got)
	}
	if got := at(c, 19, 12); got != 0xFF000000 {
		t.Errorf("past the end = %#08x, want untouched with a butt cap", got)
	}
}

func TestLineSquareExtendsHalfAWidth(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.Line(Point{X: 6, Y: 12}, Point{X: 18, Y: 12}, 4, LineCapSquare, Color{R: 255, G: 255, B: 255, A: 255})

	// Half of 4 is 2, so the line now runs from x=4 to x=20.
	if got := at(c, 4, 12); got != 0xFFFFFFFF {
		t.Errorf("(4,12) = %#08x, want painted by the square cap", got)
	}
	if got := at(c, 19, 12); got != 0xFFFFFFFF {
		t.Errorf("(19,12) = %#08x, want painted by the square cap", got)
	}
	if got := at(c, 21, 12); got != 0xFF000000 {
		t.Errorf("(21,12) = %#08x, want beyond even the square cap", got)
	}
	// The cap is square, not round: the corner of the extension is filled.
	if got := at(c, 4, 10); got != 0xFFFFFFFF {
		t.Errorf("cap corner (4,10) = %#08x, want filled by a square cap", got)
	}
}

func TestLineRoundCapIsRounded(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.Line(Point{X: 6, Y: 12}, Point{X: 18, Y: 12}, 4, LineCapRound, Color{R: 255, G: 255, B: 255, A: 255})

	// Straight out from the endpoint, inside the semicircle.
	if got := at(c, 5, 12); got != 0xFFFFFFFF {
		t.Errorf("(5,12) = %#08x, want inside the round cap", got)
	}
	// The corner a square cap fills solid is only grazed by the arc. Compare
	// against the square cap rather than against the background: the arc
	// clips (4,10) to partial coverage and misses (4,9) entirely.
	if got := at(c, 4, 10); got == 0xFFFFFFFF {
		t.Errorf("cap corner (4,10) = %#08x, want partially cut away by a round cap", got)
	}
	if got := at(c, 4, 9); got != 0xFF000000 {
		t.Errorf("(4,9) = %#08x, want outside the round cap entirely", got)
	}
}

func TestLineDiagonal(t *testing.T) {
	c := newTestCanvas(t, 24, 24, 1, 0)
	fillAll(c, 0xFF000000)
	c.Line(Point{X: 4, Y: 4}, Point{X: 20, Y: 20}, 3, LineCapButt, Color{R: 255, G: 255, B: 255, A: 255})

	if got := at(c, 12, 12); got != 0xFFFFFFFF {
		t.Errorf("on the diagonal = %#08x, want opaque white", got)
	}
	// Well off the diagonal, both sides.
	if got := at(c, 4, 20); got != 0xFF000000 {
		t.Errorf("(4,20) = %#08x, want untouched", got)
	}
	if got := at(c, 20, 4); got != 0xFF000000 {
		t.Errorf("(20,4) = %#08x, want untouched", got)
	}
}

func TestLineCoincidentPoints(t *testing.T) {
	t.Run("butt draws nothing", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		fillAll(c, 0xFF000000)
		c.Line(Point{X: 8, Y: 8}, Point{X: 8, Y: 8}, 4, LineCapButt, Color{R: 255, G: 255, B: 255, A: 255})
		if err := c.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
		if _, ok := c.Damage(); ok {
			t.Error("a zero-length butt line extended the damage")
		}
		for y := range 16 {
			for x := range 16 {
				if at(c, x, y) != 0xFF000000 {
					t.Fatalf("a zero-length butt line wrote pixel (%d,%d)", x, y)
				}
			}
		}
	})

	t.Run("square draws a square", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		fillAll(c, 0xFF000000)
		c.Line(Point{X: 8, Y: 8}, Point{X: 8, Y: 8}, 4, LineCapSquare, Color{R: 255, G: 255, B: 255, A: 255})
		// A 4x4 square centered on (8,8): corners at (6,6) and (9,9).
		if got := at(c, 6, 6); got != 0xFFFFFFFF {
			t.Errorf("corner (6,6) = %#08x, want filled", got)
		}
		if got := at(c, 9, 9); got != 0xFFFFFFFF {
			t.Errorf("corner (9,9) = %#08x, want filled", got)
		}
		if got := at(c, 5, 8); got != 0xFF000000 {
			t.Errorf("(5,8) = %#08x, want outside the square", got)
		}
	})

	t.Run("round draws a circle", func(t *testing.T) {
		c := newTestCanvas(t, 16, 16, 1, 0)
		fillAll(c, 0xFF000000)
		c.Line(Point{X: 8, Y: 8}, Point{X: 8, Y: 8}, 6, LineCapRound, Color{R: 255, G: 255, B: 255, A: 255})
		if got := at(c, 8, 8); got != 0xFFFFFFFF {
			t.Errorf("center = %#08x, want filled", got)
		}
		// The square's corner is cut away by the arc.
		if got := at(c, 5, 5); got != 0xFF000000 {
			t.Errorf("(5,5) = %#08x, want cut away by the round cap", got)
		}
	})
}

func TestLineAppliesScale(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 2, 0) // 32x32 physical
	fillAll(c, 0xFF000000)
	c.Line(Point{X: 4, Y: 8}, Point{X: 12, Y: 8}, 2, LineCapButt, Color{R: 255, G: 255, B: 255, A: 255})

	// Physically from (8,16) to (24,16), 4 wide: rows 14..17.
	if got := at(c, 16, 16); got != 0xFFFFFFFF {
		t.Errorf("physical (16,16) = %#08x, want on the scaled line", got)
	}
	if got := at(c, 16, 19); got != 0xFF000000 {
		t.Errorf("physical (16,19) = %#08x, want off the scaled line", got)
	}
}

func TestLineNoOps(t *testing.T) {
	cases := []struct {
		name  string
		width float32
		color Color
	}{
		{"zero width", 0, Color{R: 255, A: 255}},
		{"zero alpha", 3, Color{R: 255, A: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 16, 16, 1, 0)
			fillAll(c, 0xFF000000)
			c.Line(Point{X: 2, Y: 2}, Point{X: 12, Y: 12}, tc.width, LineCapRound, tc.color)
			if err := c.Err(); err != nil {
				t.Fatalf("a no-op must not be an error, got %v", err)
			}
			if _, ok := c.Damage(); ok {
				t.Error("a no-op extended the damage")
			}
		})
	}
}

func TestLineInvalidArguments(t *testing.T) {
	cases := []struct {
		name  string
		from  Point
		to    Point
		width float32
		cap   LineCap
		want  string
	}{
		{
			"unknown cap", Point{X: 1, Y: 1}, Point{X: 5, Y: 5}, 2, LineCap(4),
			`canvas: Line: invalid argument "cap": unknown LineCap(4)`,
		},
		{
			"negative width", Point{X: 1, Y: 1}, Point{X: 5, Y: 5}, -2, LineCapButt,
			`canvas: Line: invalid argument "width": must not be negative (got -2)`,
		},
		{
			"NaN from", Point{X: float32(math.NaN()), Y: 1}, Point{X: 5, Y: 5}, 2, LineCapButt,
			`canvas: Line: invalid argument "from.X": must be finite (got NaN)`,
		},
		{
			"Inf to", Point{X: 1, Y: 1}, Point{X: 5, Y: float32(math.Inf(-1))}, 2, LineCapButt,
			`canvas: Line: invalid argument "to.Y": must be finite (got -Inf)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 16, 16, 1, 0)
			fillAll(c, 0xFF000000)
			c.Line(tc.from, tc.to, tc.width, tc.cap, Color{R: 255, A: 255})

			err := c.Err()
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Err() = %v, want ErrInvalidArgument", err)
			}
			if err.Error() != tc.want {
				t.Errorf("Err() =\n  %q\nwant\n  %q", err, tc.want)
			}
			if _, ok := c.Damage(); ok {
				t.Error("an invalid call extended the damage")
			}
		})
	}
}

func TestLineNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 12, 12, 1, 40)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	for _, lc := range []LineCap{LineCapButt, LineCapSquare, LineCapRound} {
		c.Line(Point{X: -20, Y: -20}, Point{X: 40, Y: 40}, 9, lc, Color{R: 255, A: 255})
	}
	if !paddingIntact(c, sentinel) {
		t.Error("Line wrote into the row padding")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./canvas/ -run 'TestSegmentShape|TestBoxShape|TestLine' -v`
Expected: FAIL — build error, `segmentShape`, `boxShape` and `Line` are undefined.

- [ ] **Step 3: Write the line**

Create `canvas/line.go`:

```go
package canvas

import (
	"fmt"
	"math"
)

// segmentShape is a capsule: every point within half of the stroke width of
// the segment. It is the LineCapRound shape.
type segmentShape struct {
	ax, ay  float32 // start, physical
	dx, dy  float32 // end minus start
	invLen2 float32 // 1 / (dx*dx + dy*dy), precomputed so the loop divides nothing
	half    float32 // half the stroke width
}

func (s segmentShape) signedDistance(x, y float32) float32 {
	pax := x - s.ax
	pay := y - s.ay
	// Project onto the segment and clamp: outside the ends the nearest point
	// is the endpoint itself, which is exactly what rounds the caps.
	h := clamp01((pax*s.dx + pay*s.dy) * s.invLen2)
	return hypotf(pax-s.dx*h, pay-s.dy*h) - s.half
}

func (s segmentShape) bounds() (x0, y0, x1, y1 float32) {
	bx := s.ax + s.dx
	by := s.ay + s.dy
	return min(s.ax, bx) - s.half, min(s.ay, by) - s.half,
		max(s.ax, bx) + s.half, max(s.ay, by) + s.half
}

// boxShape is a rectangle expressed in the line's own frame: one axis runs
// along the segment, the other across it. Butt and square caps differ only
// in heT — square adds half the stroke width at each end — so one shape
// serves both.
type boxShape struct {
	cx, cy   float32 // center, physical
	ux, uy   float32 // unit vector along the line
	heT, heS float32 // half extents along and across the line
}

func (s boxShape) signedDistance(x, y float32) float32 {
	dx := x - s.cx
	dy := y - s.cy
	t := dx*s.ux + dy*s.uy  // along
	u := -dx*s.uy + dy*s.ux // across
	qt := absf(t) - s.heT
	qu := absf(u) - s.heS
	return hypotf(max(qt, 0), max(qu, 0)) + min(max(qt, qu), 0)
}

func (s boxShape) bounds() (x0, y0, x1, y1 float32) {
	// Axis-aligned extent of a rotated box: project both half extents onto
	// each axis. The across-axis unit vector is (-uy, ux).
	ex := absf(s.ux)*s.heT + absf(s.uy)*s.heS
	ey := absf(s.uy)*s.heT + absf(s.ux)*s.heS
	return s.cx - ex, s.cy - ey, s.cx + ex, s.cy + ey
}

// Line draws a straight segment of the given width between two points in
// logical units, compositing source-over. The width is centered on the
// segment, half to each side.
//
// The caps behave as [LineCapButt], [LineCapSquare] and [LineCapRound]
// describe. If the two points coincide the segment has no direction, and
// each cap collapses to the shape it names: round draws a circle, square
// draws a square, and butt draws nothing at all.
//
// A zero width is a no-op, as is a fully transparent color. A negative
// width, non-finite coordinates and an unrecognized cap are errors.
//
// The cap parameter shadows the builtin of the same name; the builtin is
// not needed here, and this is the name the API contract uses.
func (c *Canvas) Line(from Point, to Point, width float32, cap LineCap, color Color) {
	const op = "Line"

	if c.failed() {
		return
	}
	if err := validPoint(op, "from", from); err != nil {
		c.fail(err)
		return
	}
	if err := validPoint(op, "to", to); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	switch cap {
	case LineCapButt, LineCapSquare, LineCapRound:
	default:
		c.fail(invalidArg(op, "cap", fmt.Sprintf("unknown LineCap(%d)", uint8(cap))))
		return
	}
	if width == 0 || color.A == 0 {
		return
	}

	ax := from.X * c.scale
	ay := from.Y * c.scale
	bx := to.X * c.scale
	by := to.Y * c.scale
	half := width * c.scale / 2

	dx := bx - ax
	dy := by - ay
	len2 := dx*dx + dy*dy
	src := premultiply(color)

	if len2 == 0 {
		c.degenerateLine(ax, ay, half, cap, src)
		return
	}

	if cap == LineCapRound {
		s := segmentShape{ax: ax, ay: ay, dx: dx, dy: dy, invLen2: 1 / len2, half: half}
		if box, ok := sdfFill(c, s, src); ok {
			c.addDamage(box)
		}
		return
	}

	length := float32(math.Sqrt(float64(len2)))
	heT := length / 2
	if cap == LineCapSquare {
		heT += half
	}
	s := boxShape{
		cx:  (ax + bx) / 2,
		cy:  (ay + by) / 2,
		ux:  dx / length,
		uy:  dy / length,
		heT: heT,
		heS: half,
	}
	if box, ok := sdfFill(c, s, src); ok {
		c.addDamage(box)
	}
}

// degenerateLine handles from == to, where the segment has no direction and
// therefore no local frame to build. Each cap collapses to the shape it
// describes.
func (c *Canvas) degenerateLine(x, y, half float32, cap LineCap, src uint32) {
	switch cap {
	case LineCapButt:
		// A zero-length butt line covers no area at all.
		return

	case LineCapRound:
		if box, ok := sdfFill(c, circleShape{cx: x, cy: y, r: half}, src); ok {
			c.addDamage(box)
		}

	case LineCapSquare:
		// Axis-aligned, so this takes the exact-coverage path rather than
		// the distance approximation.
		x0, y0 := x-half, y-half
		x1, y1 := x+half, y+half
		if box, ok := c.clipRect(x0, y0, x1, y1); ok {
			c.fillAxisRect(box, x0, y0, x1, y1, src)
			c.addDamage(box)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./canvas/ -v`
Expected: PASS — all of Tasks 1-10.

Run: `go vet ./canvas/`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add canvas/line.go canvas/line_test.go
git commit -m "canvas: add Line with butt, square and round caps"
```

---

## Task 11: Acceptance — fuzzing, zero-allocation proof, benchmarks, docs

**Files:**
- Create: `canvas/fuzz_test.go`
- Create: `canvas/bench_test.go`
- Modify: `docs/canvas.md` (the `## Estado` section only)

**Interfaces:**
- Consumes: the entire public API from Tasks 1-10.
- Produces: no new package API. This task proves the cross-cutting guarantees that no single earlier task could test on its own, and brings the reference doc in line with what shipped.

Three properties are only meaningful across the whole surface, which is why they land here rather than being sprinkled through Tasks 5-10:

1. **No operation ever escapes the visible region or panics**, whatever the arguments — the fuzz target.
2. **No successful drawing operation allocates** — `testing.AllocsPerRun` in a normal test, so a regression fails `go test`, not just a benchmark somebody remembers to run.
3. **The borrowed slice keeps its identity, length and capacity**, and padding stays untouched, across every operation.

- [ ] **Step 1: Write the fuzz target**

Create `canvas/fuzz_test.go`:

```go
package canvas

import (
	"math"
	"testing"
)

// FuzzNew feeds unconstrained descriptions to the constructor. It must
// either reject them or return a canvas that is coherent — never panic,
// and never accept a buffer it would then read past.
func FuzzNew(f *testing.F) {
	f.Add(64, 48, 64, 48, 64, float32(1))
	f.Add(0, 0, 0, 0, 0, float32(0))
	f.Add(800, 600, 1200, 900, 1216, float32(1.5))
	f.Add(-1, -1, -1, -1, -1, float32(-1))
	f.Add(math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32, float32(1))

	f.Fuzz(func(t *testing.T, lw, lh, pw, ph, stride int, scale float32) {
		// Cap the allocation so the fuzzer cannot OOM the machine; the
		// interesting validation paths all trigger well below this.
		const maxPixels = 1 << 20
		n := 0
		if pw > 0 && ph > 0 && stride > 0 && ph <= maxPixels && stride <= maxPixels {
			if p := ph * stride; p > 0 && p <= maxPixels {
				n = p
			}
		}
		px := make([]uint32, n)

		c, err := New(Buffer{Pixels: px, Width: pw, Height: ph, Stride: stride}, lw, lh, scale)
		if err != nil {
			if c != nil {
				t.Fatal("New returned both a canvas and an error")
			}
			return
		}

		// A canvas that was accepted must describe memory it can actually
		// address end to end.
		need := (c.PixelHeight()-1)*c.Stride() + c.PixelWidth()
		if need > len(c.Pixels()) {
			t.Fatalf("New accepted a buffer needing %d elements but holding %d", need, len(c.Pixels()))
		}
		if c.Stride() < c.PixelWidth() {
			t.Fatalf("New accepted stride %d below physical width %d", c.Stride(), c.PixelWidth())
		}
		if c.Width() <= 0 || c.Height() <= 0 {
			t.Fatalf("New accepted a non-positive logical size %dx%d", c.Width(), c.Height())
		}
	})
}

// FuzzDrawing builds a valid canvas and throws arbitrary geometry at every
// drawing method. Nothing may panic, nothing may write outside the visible
// region, the damage must stay inside it, and the borrowed slice must keep
// its identity.
func FuzzDrawing(f *testing.F) {
	f.Add(float32(1), float32(4), float32(4), float32(8), float32(6), float32(2), float32(1), uint8(2), uint8(200))
	f.Add(float32(1.5), float32(-100), float32(-100), float32(1e9), float32(1e9), float32(1e9), float32(1e9), uint8(0), uint8(255))
	f.Add(float32(2), float32(math.NaN()), float32(0), float32(4), float32(4), float32(1), float32(1), uint8(1), uint8(0))
	f.Add(float32(1.25), float32(0), float32(0), float32(0), float32(0), float32(0), float32(0), uint8(3), uint8(128))

	f.Fuzz(func(t *testing.T, scale, x, y, w, h, radius, width float32, cap uint8, alpha uint8) {
		if !(scale > 0) || scale > 4 || math.IsNaN(float64(scale)) {
			t.Skip()
		}

		const lw, lh = 24, 18
		const strideExtra = 7
		pw := int(float32(lw) * scale)
		ph := int(float32(lh) * scale)
		if pw <= 0 || ph <= 0 {
			t.Skip()
		}
		stride := pw + strideExtra

		px := make([]uint32, ph*stride)
		c, err := New(Buffer{Pixels: px, Width: pw, Height: ph, Stride: stride}, lw, lh, scale)
		if err != nil {
			t.Skip() // the rounding did not satisfy the tolerance; not this target's job
		}

		const sentinel = 0xCAFEBABE
		fillPadding(c, sentinel)

		origLen, origCap := len(px), cap(px)

		rect := Rect{X: x, Y: y, Width: w, Height: h}
		color := Color{R: 200, G: 100, B: 50, A: alpha}
		lineCap := LineCap(cap)

		c.Clear(Color{})
		c.ClearRect(rect, color)
		c.FillRect(rect, color)
		c.StrokeRect(rect, width, color)
		c.FillRoundedRect(rect, radius, color)
		c.StrokeRoundedRect(rect, radius, width, color)
		c.FillCircle(Point{X: x, Y: y}, radius, color)
		c.StrokeCircle(Point{X: x, Y: y}, radius, width, color)
		c.Line(Point{X: x, Y: y}, Point{X: x + w, Y: y + h}, width, lineCap, color)

		if !paddingIntact(c, sentinel) {
			t.Fatal("an operation wrote into the row padding")
		}
		if dmg, ok := c.Damage(); ok {
			if dmg.X < 0 || dmg.Y < 0 || dmg.Width <= 0 || dmg.Height <= 0 ||
				dmg.X+dmg.Width > pw || dmg.Y+dmg.Height > ph {
				t.Fatalf("Damage() = %+v, outside the visible %dx%d region", dmg, pw, ph)
			}
		}
		if len(px) != origLen || cap(px) != origCap {
			t.Fatalf("the borrowed slice changed: len %d->%d, cap %d->%d", origLen, len(px), origCap, cap(px))
		}
		if &px[0] != &c.Pixels()[0] {
			t.Fatal("the canvas swapped the borrowed storage")
		}
	})
}
```

- [ ] **Step 2: Run the fuzz targets briefly**

Run: `go test ./canvas/ -run 'FuzzNew|FuzzDrawing' -v`
Expected: PASS — the seed corpus alone runs as ordinary tests.

Run: `go test ./canvas/ -run FuzzDrawing -fuzz FuzzDrawing -fuzztime 30s`
Expected: no failures. Repeat for `FuzzNew`. Commit any `testdata/fuzz` corpus entry a failure produces, after fixing the bug it found.

- [ ] **Step 3: Write the zero-allocation test and the benchmarks**

Create `canvas/bench_test.go`:

```go
package canvas

import "testing"

// benchCanvas is a 1920x1080-at-scale-1 canvas with a padded stride, the
// closest thing to a real panel surface.
func benchCanvas(scale float32) *Canvas {
	const lw, lh = 1920, 1080
	pw := int(float32(lw) * scale)
	ph := int(float32(lh) * scale)
	stride := pw + 16
	c, err := New(
		Buffer{Pixels: make([]uint32, ph*stride), Width: pw, Height: ph, Stride: stride},
		lw, lh, scale,
	)
	if err != nil {
		panic(err)
	}
	return c
}

// TestZeroAllocations is the requirement the spec states, enforced by
// go test rather than by a benchmark somebody has to remember to run. A
// drawing operation that starts allocating has almost certainly boxed a
// shape into an interface value or captured one in a closure.
func TestZeroAllocations(t *testing.T) {
	c := newTestCanvas(t, 200, 200, 1, 216)
	opaque := Color{R: 200, G: 100, B: 50, A: 255}
	translucent := Color{R: 200, G: 100, B: 50, A: 128}
	rect := Rect{X: 10.5, Y: 12.25, Width: 60, Height: 40}

	cases := []struct {
		name string
		fn   func()
	}{
		{"Clear", func() { c.Clear(opaque) }},
		{"ClearRect", func() { c.ClearRect(rect, Color{}) }},
		{"FillRect", func() { c.FillRect(rect, translucent) }},
		{"StrokeRect", func() { c.StrokeRect(rect, 1.5, translucent) }},
		{"FillRoundedRect", func() { c.FillRoundedRect(rect, 6, translucent) }},
		{"StrokeRoundedRect", func() { c.StrokeRoundedRect(rect, 6, 1.5, translucent) }},
		{"FillCircle", func() { c.FillCircle(Point{X: 100, Y: 100}, 30, translucent) }},
		{"StrokeCircle", func() { c.StrokeCircle(Point{X: 100, Y: 100}, 30, 2, translucent) }},
		{"LineButt", func() { c.Line(Point{X: 5, Y: 5}, Point{X: 180, Y: 150}, 2, LineCapButt, translucent) }},
		{"LineRound", func() { c.Line(Point{X: 5, Y: 5}, Point{X: 180, Y: 150}, 2, LineCapRound, translucent) }},
		{"LineSquare", func() { c.Line(Point{X: 5, Y: 5}, Point{X: 180, Y: 150}, 2, LineCapSquare, translucent) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := testing.AllocsPerRun(20, tc.fn); n != 0 {
				t.Errorf("%s allocated %v times per call, want 0", tc.name, n)
			}
		})
	}
	if err := c.Err(); err != nil {
		t.Fatalf("Err() = %v; the benchmark shapes must all be valid", err)
	}
}

func BenchmarkNew(b *testing.B) {
	px := make([]uint32, 1080*1936)
	buf := Buffer{Pixels: px, Width: 1920, Height: 1080, Stride: 1936}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := New(buf, 1920, 1080, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClear(b *testing.B) {
	for _, tc := range []struct {
		name   string
		stride int
	}{{"compact", 1920}, {"padded", 1936}} {
		b.Run(tc.name, func(b *testing.B) {
			c, err := New(
				Buffer{Pixels: make([]uint32, 1080*tc.stride), Width: 1920, Height: 1080, Stride: tc.stride},
				1920, 1080, 1,
			)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				c.Clear(Color{R: 20, G: 20, B: 20, A: 255})
			}
		})
	}
}

// BenchmarkShapes separates the fixed cost of transforming a shape from the
// cost of touching more pixels: the same logical geometry at four scales.
func BenchmarkShapes(b *testing.B) {
	opaque := Color{R: 200, G: 100, B: 50, A: 255}
	translucent := Color{R: 200, G: 100, B: 50, A: 128}
	rect := Rect{X: 100.5, Y: 100.25, Width: 300, Height: 200}

	shapes := []struct {
		name string
		fn   func(c *Canvas, color Color)
	}{
		{"FillRect", func(c *Canvas, col Color) { c.FillRect(rect, col) }},
		{"StrokeRect", func(c *Canvas, col Color) { c.StrokeRect(rect, 2, col) }},
		{"FillRoundedRect", func(c *Canvas, col Color) { c.FillRoundedRect(rect, 8, col) }},
		{"StrokeRoundedRect", func(c *Canvas, col Color) { c.StrokeRoundedRect(rect, 8, 2, col) }},
		{"FillCircle", func(c *Canvas, col Color) { c.FillCircle(Point{X: 400, Y: 300}, 120, col) }},
		{"StrokeCircle", func(c *Canvas, col Color) { c.StrokeCircle(Point{X: 400, Y: 300}, 120, 3, col) }},
		{"Line", func(c *Canvas, col Color) { c.Line(Point{X: 50, Y: 50}, Point{X: 900, Y: 700}, 3, LineCapRound, col) }},
		// Half the shape hangs off the left edge, so the clipping path is
		// measured too, not just the fully visible case.
		{"FillRectClipped", func(c *Canvas, col Color) {
			c.FillRect(Rect{X: -200, Y: 100, Width: 300, Height: 200}, col)
		}},
	}

	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		c := benchCanvas(scale)
		for _, s := range shapes {
			for _, alpha := range []struct {
				name  string
				color Color
			}{{"opaque", opaque}, {"translucent", translucent}} {
				b.Run(s.name+"/scale"+formatScale(scale)+"/"+alpha.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						s.fn(c, alpha.color)
					}
				})
			}
		}
		if err := c.Err(); err != nil {
			b.Fatalf("Err() = %v", err)
		}
	}
}

func formatScale(s float32) string {
	switch s {
	case 1:
		return "1"
	case 1.25:
		return "1.25"
	case 1.5:
		return "1.5"
	case 2:
		return "2"
	}
	return "other"
}
```

- [ ] **Step 4: Run the whole suite and the benchmarks**

Run: `go test ./canvas/ -v`
Expected: PASS, including every `TestZeroAllocations` subtest.

Run: `go test ./canvas/ -bench . -benchtime 100x -run '^$'`
Expected: every benchmark reports `0 B/op` and `0 allocs/op`. Record the resulting numbers in the commit message as the baseline; the spec deliberately sets no millisecond budget yet.

Run: `go build ./... && go test ./... && go vet ./...`
Expected: green across the whole repository.

- [ ] **Step 5: Update the reference doc**

`docs/canvas.md` is the reference document for this package, alongside `docs/wlcore.md` and `docs/waygenerator.md`, and it is in **Spanish**. Replace only its `## Estado` section:

```markdown
## Estado

Implementado en `canvas/`. La primera versión cubre todo el alcance
descrito aquí: buffer prestado, ARGB8888 premultiplicado, HiDPI con escala
inmutable, rectángulos, rectángulos redondeados, círculos y líneas con sus
tres terminaciones, error pegajoso y damage acumulado.

Plan de implementación:
`docs/superpowers/plans/2026-08-21-canvas-implementation.md`.
Diseño original congelado:
`docs/superpowers/specs/2026-08-21-canvas-design.md`.

Sin cerrar todavía, por orden de probabilidad de que haga falta: `DrawMask`
para texto, lista de rectángulos dañados y clipping rectangular propio.
```

Leave the rest of the document as it is: the design decisions it records are still the contract, and the sections on rasterization, errors and la integración con Wayland describe exactly what shipped. If any behaviour ended up differing from what a section states, fix the **document** to match the code and say so in the commit message — the doc is the reference, not a historical record.

- [ ] **Step 6: Commit**

```bash
git add canvas/fuzz_test.go canvas/bench_test.go docs/canvas.md
git commit -m "canvas: add fuzzing, zero-allocation proof and benchmarks"
```

If fuzzing produced corpus entries worth keeping:

```bash
git add canvas/testdata/fuzz
git commit -m "canvas: add fuzz corpus entries for the cases they caught"
```

---

## Self-Review Notes

Checked against `docs/canvas.md`, section by section:

- *Alcance inicial* — every included bullet maps to a task; every excluded one (Renderer, layout, text, ellipses, Bézier, arbitrary transforms, custom clipping, damage lists, buffer management, DPI detection, goroutine synchronization, gamma) is absent from the plan and named in Global Constraints or the doc's own evolution list.
- *Descriptor del buffer*, *Validación del constructor* — Task 2.
- *Coordenadas y HiDPI* — Task 2 (`New`, tolerance, immutability) and every drawing task's "scale once" step.
- *Formato de píxel y color*, *Composición y opacidad*, *Un solo compositor* — Task 4, with the halo test as the load-bearing case.
- *Tipos públicos* — Task 1.
- *API pública inicial* — `New` and the accessors in Task 2, `Err`/`Damage`/`ResetDamage` in Task 3, `Clear`/`ClearRect` in Task 7, `FillRect`/`StrokeRect` in Tasks 5-6, `FillRoundedRect`/`StrokeRoundedRect` in Task 9, `FillCircle`/`StrokeCircle` in Task 8, `Line` in Task 10. All twelve drawing methods and all eight accessors are accounted for.
- *Errores* — Task 1 (shape and strings), Task 3 (stickiness), and a no-op/invalid-argument case in every drawing task.
- *Regiones dañadas* — Task 3 (accumulator), plus a damage assertion in Tasks 5-10 and the region invariant in Task 11's fuzz target.
- *Semántica de las figuras*, *Recorte* — Tasks 5-10, one test per documented rule.
- *Rasterización y antialiasing* — the exact path in Task 5, the approximate path in Task 8, both documented in code where the spec asks for it to be written down.
- *Rendimiento* — Task 11 (`TestZeroAllocations`, benchmarks across the four scales, clipped and unclipped, opaque and translucent).
- *Pruebas* — every unit-test bullet has a named test; the "pruebas visuales" bullets are covered as pixel assertions (antialiasing, corners, stroke widths, caps, overlap on light **and** dark backgrounds via the compositor tests, compact vs padded buffers via the padding tests) rather than as rendered images, since the package has no image encoder and the spec does not ask for golden files.
- *Integración con Wayland* — documentation only; `Damage()` returning physical `PixelRect` is what makes it work, and that is Task 3.

Deliberate deviations from a literal reading of the spec, all of them narrowing:

- `StrokeRect` uses exact coverage (difference of two rectangles) instead of the SDF path the spec's rasterization section implies for strokes generally. Strictly better and simpler; the spec only promises approximation for "círculos, rectángulos redondeados y líneas".
- `FillRoundedRect` with radius 0 delegates to `FillRect`, so it inherits exact coverage rather than the approximation. The spec requires equivalence to the unrounded shape, which this satisfies more strongly.
- `StrokeCircle` and `StrokeRoundedRect` treat an oversized width as a solid fill instead of erroring, matching "si supera el espacio interior disponible, desaparece la parte vacía". They delegate to the fill rather than clamping the ring, because a ring exactly as deep as the shape puts the deepest point on its own boundary and would render it at half coverage.
