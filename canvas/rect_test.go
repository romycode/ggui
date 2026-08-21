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
