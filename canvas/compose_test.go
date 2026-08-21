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
		{-1, 0},  // clamped, not wrapped
		{2, 255}, // clamped, not wrapped
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
