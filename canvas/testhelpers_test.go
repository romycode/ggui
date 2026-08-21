package canvas

import "testing"

// newTestCanvas builds a scale-aware canvas whose physical size is the
// logical size times scale, rounded down, with the requested stride. Pass
// stride 0 for a compact buffer.
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

// fillPadding writes sentinel into every padding element.
func fillPadding(c *Canvas, sentinel uint32) {
	for y := range c.PixelHeight() {
		row := y * c.Stride()
		for x := c.PixelWidth(); x < c.Stride(); x++ {
			c.Pixels()[row+x] = sentinel
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
