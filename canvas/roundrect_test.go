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
