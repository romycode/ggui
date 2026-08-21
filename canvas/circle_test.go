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
