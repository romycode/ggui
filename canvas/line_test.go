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
