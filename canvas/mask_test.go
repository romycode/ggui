package canvas

import (
	"image"
	"testing"
)

// newMask builds an alpha mask of w x h anchored at origin, filled with cov.
func newMask(origin image.Point, w, h int, cov uint8) *image.Alpha {
	m := image.NewAlpha(image.Rectangle{Min: origin, Max: origin.Add(image.Pt(w, h))})
	for i := range m.Pix {
		m.Pix[i] = cov
	}
	return m
}

func TestDrawMaskFullCoverageWritesTheColor(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	red := Color{R: 255, A: 255}

	c.DrawMask(image.Pt(2, 3), newMask(image.Point{}, 3, 2, 255), red)

	if err := c.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
	for y := 3; y < 5; y++ {
		for x := 2; x < 5; x++ {
			if got := at(c, x, y); got != 0xFFFF0000 {
				t.Fatalf("pixel (%d, %d) = %#08x, want 0xFFFF0000", x, y, got)
			}
		}
	}
	// And nothing around it.
	for _, p := range []image.Point{{X: 1, Y: 3}, {X: 5, Y: 3}, {X: 2, Y: 2}, {X: 2, Y: 5}} {
		if got := at(c, p.X, p.Y); got != 0 {
			t.Fatalf("pixel (%d, %d) = %#08x, want untouched", p.X, p.Y, got)
		}
	}
}

// at is in physical pixels and the scale must not touch it: the mask was
// already rasterized at the canvas's resolution.
func TestDrawMaskIgnoresTheCanvasScale(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 2, 0)
	white := Color{R: 255, G: 255, B: 255, A: 255}

	c.DrawMask(image.Pt(3, 3), newMask(image.Point{}, 1, 1, 255), white)

	if got := at(c, 3, 3); got != 0xFFFFFFFF {
		t.Fatalf("pixel (3, 3) = %#08x, want the mask at its physical position", got)
	}
	if got := at(c, 6, 6); got != 0 {
		t.Fatalf("pixel (6, 6) = %#08x: the position was scaled", got)
	}
}

// A mask's Rect need not start at the origin; what matters is that its Min
// lands on at.
func TestDrawMaskPositionsRectMinAtThePoint(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	white := Color{R: 255, G: 255, B: 255, A: 255}

	c.DrawMask(image.Pt(1, 1), newMask(image.Pt(50, 60), 2, 2, 255), white)

	for y := 1; y < 3; y++ {
		for x := 1; x < 3; x++ {
			if got := at(c, x, y); got != 0xFFFFFFFF {
				t.Fatalf("pixel (%d, %d) = %#08x, want the mask drawn at (1, 1)", x, y, got)
			}
		}
	}
	if got := at(c, 3, 3); got != 0 {
		t.Fatalf("pixel (3, 3) = %#08x, want untouched", got)
	}
}

// Coverage runs through the canvas's own compositor, so it scales every
// channel and not just alpha. The halo bug would leave this near-white.
func TestDrawMaskCoverageGoesThroughTheOneCompositor(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF000000)
	white := Color{R: 255, G: 255, B: 255, A: 255}

	c.DrawMask(image.Pt(0, 0), newMask(image.Point{}, 1, 1, 128), white)

	got := at(c, 0, 0)
	a, r := got>>24&0xFF, got>>16&0xFF
	if a != 255 {
		t.Errorf("alpha = %d, want 255 over an opaque destination", a)
	}
	if r < 120 || r > 136 {
		t.Errorf("red = %d, want about 128; 255 would mean a second compositor", r)
	}
	if r > a {
		t.Errorf("result %#08x is not premultiplied: a channel exceeds alpha", got)
	}
}

func TestDrawMaskZeroCoveragePixelsAreLeftAlone(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF102030)

	m := newMask(image.Point{}, 2, 1, 0)
	m.Pix[1] = 255
	c.DrawMask(image.Pt(0, 0), m, Color{R: 255, A: 255})

	if got := at(c, 0, 0); got != 0xFF102030 {
		t.Errorf("zero-coverage pixel = %#08x, want the destination untouched", got)
	}
	if got := at(c, 1, 0); got != 0xFFFF0000 {
		t.Errorf("full-coverage pixel = %#08x, want the color", got)
	}
}

func TestDrawMaskClipsToTheVisibleRegion(t *testing.T) {
	const sentinel = 0xDEADBEEF
	c := newTestCanvas(t, 4, 4, 1, 8)
	fillPadding(c, sentinel)
	white := Color{R: 255, G: 255, B: 255, A: 255}

	// Straddles every edge at once.
	c.DrawMask(image.Pt(-2, -2), newMask(image.Point{}, 8, 8, 255), white)

	if err := c.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
	if !paddingIntact(c, sentinel) {
		t.Fatal("DrawMask wrote into the row padding")
	}
	for y := range 4 {
		for x := range 4 {
			if got := at(c, x, y); got != 0xFFFFFFFF {
				t.Fatalf("pixel (%d, %d) = %#08x, want the clipped mask", x, y, got)
			}
		}
	}
	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("no damage reported")
	}
	if dmg != (PixelRect{X: 0, Y: 0, Width: 4, Height: 4}) {
		t.Fatalf("damage = %+v, want the visible region only", dmg)
	}
}

func TestDrawMaskDamageIsTheDrawnRegion(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)

	c.DrawMask(image.Pt(3, 4), newMask(image.Point{}, 5, 2, 255), Color{R: 255, A: 255})

	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("no damage reported")
	}
	if dmg != (PixelRect{X: 3, Y: 4, Width: 5, Height: 2}) {
		t.Fatalf("damage = %+v, want {3 4 5 2}", dmg)
	}
}

func TestDrawMaskNoOps(t *testing.T) {
	white := Color{R: 255, G: 255, B: 255, A: 255}

	cases := []struct {
		name string
		draw func(*Canvas)
	}{
		{"transparent color", func(c *Canvas) {
			c.DrawMask(image.Pt(0, 0), newMask(image.Point{}, 4, 4, 255), Color{})
		}},
		{"empty mask", func(c *Canvas) {
			c.DrawMask(image.Pt(0, 0), image.NewAlpha(image.Rectangle{}), white)
		}},
		{"entirely off-screen", func(c *Canvas) {
			c.DrawMask(image.Pt(100, 100), newMask(image.Point{}, 4, 4, 255), white)
		}},
		{"off-screen the other way", func(c *Canvas) {
			c.DrawMask(image.Pt(-100, -100), newMask(image.Point{}, 4, 4, 255), white)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 8, 8, 1, 0)
			tc.draw(c)

			if err := c.Err(); err != nil {
				t.Fatalf("no-op reported an error: %v", err)
			}
			if _, ok := c.Damage(); ok {
				t.Error("a no-op reported damage")
			}
			for _, v := range c.Pixels() {
				if v != 0 {
					t.Fatal("a no-op wrote to the buffer")
				}
			}
		})
	}
}

// A position far outside int range must fall off the edge, not wrap back
// into the visible region.
func TestDrawMaskPositionCannotOverflowIntoView(t *testing.T) {
	const big = int(^uint(0) >> 1) // math.MaxInt

	for _, at := range []image.Point{
		{X: big, Y: big},
		{X: -big, Y: -big},
		{X: big, Y: 0},
		{X: 0, Y: -big},
	} {
		c := newTestCanvas(t, 8, 8, 1, 0)
		c.DrawMask(at, newMask(image.Point{}, 4, 4, 255), Color{R: 255, A: 255})

		if err := c.Err(); err != nil {
			t.Fatalf("at %v: canvas error: %v", at, err)
		}
		for i, v := range c.Pixels() {
			if v != 0 {
				t.Fatalf("at %v: pixel %d = %#08x, a wrapped position reached the buffer", at, i, v)
			}
		}
	}
}

func TestDrawMaskInvalidArguments(t *testing.T) {
	white := Color{R: 255, G: 255, B: 255, A: 255}

	cases := []struct {
		name string
		draw func(*Canvas)
	}{
		{"nil mask", func(c *Canvas) {
			c.DrawMask(image.Pt(0, 0), nil, white)
		}},
		{"stride below width", func(c *Canvas) {
			m := newMask(image.Point{}, 4, 4, 255)
			m.Stride = 2
			c.DrawMask(image.Pt(0, 0), m, white)
		}},
		{"pix too short for rect", func(c *Canvas) {
			m := newMask(image.Point{}, 4, 4, 255)
			m.Pix = m.Pix[:3]
			c.DrawMask(image.Pt(0, 0), m, white)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCanvas(t, 8, 8, 1, 0)
			tc.draw(c)

			if c.Err() == nil {
				t.Fatal("no error recorded")
			}
			for _, v := range c.Pixels() {
				if v != 0 {
					t.Fatal("an invalid call still drew")
				}
			}
		})
	}
}

func TestDrawMaskRespectsTheStickyError(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	c.FillRect(Rect{Width: -1, Height: 1}, Color{A: 255})
	first := c.Err()
	if first == nil {
		t.Fatal("setup did not record an error")
	}

	c.DrawMask(image.Pt(0, 0), newMask(image.Point{}, 4, 4, 255), Color{R: 255, A: 255})

	if c.Err() != first {
		t.Fatalf("Err = %v, want the first error kept", c.Err())
	}
	for _, v := range c.Pixels() {
		if v != 0 {
			t.Fatal("a poisoned canvas still drew")
		}
	}
}

func TestDrawMaskDoesNotAllocate(t *testing.T) {
	c := newTestCanvas(t, 64, 64, 1, 0)
	m := newMask(image.Point{}, 16, 16, 200)
	col := Color{R: 255, G: 255, B: 255, A: 255}

	if n := testing.AllocsPerRun(100, func() { c.DrawMask(image.Pt(4, 4), m, col) }); n != 0 {
		t.Fatalf("DrawMask allocates %v times per call, want 0", n)
	}
}
