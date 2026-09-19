package canvas

import (
	"image"
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

	// capValue, not cap: this function needs the cap builtin below to check
	// that the borrowed slice keeps its capacity.
	f.Fuzz(func(t *testing.T, scale, x, y, w, h, radius, width float32, capValue uint8, alpha uint8) {
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
		lineCap := LineCap(capValue)

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

// FuzzDrawMask feeds DrawMask inconsistent masks and extreme positions. An
// image.Alpha carries Pix, Stride and Rect independently, so a caller can
// describe more pixels than it holds; and the position is the one argument
// the canvas takes in raw physical pixels, with no float validation in
// front of it. Both have to end in a rejection or a clip, never a panic and
// never a read past the mask.
func FuzzDrawMask(f *testing.F) {
	f.Add(float32(1), int32(0), int32(0), int16(0), int16(0), uint8(4), uint8(4), uint8(4), uint8(128), uint8(255))
	f.Add(float32(2), int32(-3), int32(-3), int16(-50), int16(60), uint8(8), uint8(8), uint8(8), uint8(128), uint8(200))
	f.Add(float32(1), int32(math.MaxInt32), int32(math.MinInt32), int16(0), int16(0), uint8(4), uint8(4), uint8(4), uint8(128), uint8(255))
	f.Add(float32(1.5), int32(2), int32(2), int16(0), int16(0), uint8(6), uint8(6), uint8(2), uint8(0), uint8(255))

	f.Fuzz(func(t *testing.T, scale float32, atX, atY int32, ox, oy int16, w, h, stride, slack, alpha uint8) {
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
		px := make([]uint32, ph*(pw+strideExtra))
		c, err := New(Buffer{Pixels: px, Width: pw, Height: ph, Stride: pw + strideExtra}, lw, lh, scale)
		if err != nil {
			t.Skip()
		}

		const sentinel = 0xCAFEBABE
		fillPadding(c, sentinel)
		origLen, origCap := len(px), cap(px)

		// slack straddles the length the Rect and Stride actually need, so
		// the corpus holds both masks that are long enough and masks that
		// are short by a little.
		mask := &image.Alpha{
			Stride: int(stride),
			Rect:   image.Rect(int(ox), int(oy), int(ox)+int(w), int(oy)+int(h)),
		}
		need := (mask.Rect.Dy()-1)*mask.Stride + mask.Rect.Dx()
		if n := need + int(slack) - 128; n > 0 {
			mask.Pix = make([]uint8, n)
			for i := range mask.Pix {
				mask.Pix[i] = uint8(i * 7)
			}
		}

		c.DrawMask(image.Pt(int(atX), int(atY)), mask, Color{R: 200, G: 100, B: 50, A: alpha})

		if !paddingIntact(c, sentinel) {
			t.Fatal("DrawMask wrote into the row padding")
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
