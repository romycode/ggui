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
