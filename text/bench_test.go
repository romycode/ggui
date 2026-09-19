package text

import (
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/romycode/ggui/canvas"
)

func benchFace(b *testing.B, size float32) *Face {
	b.Helper()

	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		b.Fatalf("Parse: %v", err)
	}
	f, err := NewFace(parsed, size)
	if err != nil {
		b.Fatalf("NewFace: %v", err)
	}
	b.Cleanup(func() { f.Close() })
	return f
}

func benchCanvas(b *testing.B, scale float32) *canvas.Canvas {
	b.Helper()

	const lw, lh = 600, 80
	pw, ph := int(lw*scale), int(lh*scale)
	cv, err := canvas.New(
		canvas.Buffer{Pixels: make([]uint32, pw*ph), Width: pw, Height: ph, Stride: pw},
		lw, lh, scale,
	)
	if err != nil {
		b.Fatalf("canvas.New: %v", err)
	}
	return cv
}

// BenchmarkDraw separates the first frame, which rasterizes every glyph,
// from the frames after it, which only composite cached masks. The gap
// between the two is what the glyph cache is for.
func BenchmarkDraw(b *testing.B) {
	const label = "Hamburgefonstiv 0123456789"
	clip := canvas.Rect{Width: 600, Height: 80}
	at := canvas.Point{X: 10.5, Y: 40}
	col := canvas.Color{R: 255, G: 255, B: 255, A: 255}

	for _, scale := range []float32{1, 1.5, 2} {
		name := "scale" + map[float32]string{1: "1", 1.5: "1.5", 2: "2"}[scale]

		// Cold: the same face with its cache emptied each iteration, so
		// every glyph is rasterized again. Building the face is left out
		// on purpose — parsing a font is not what the cache addresses,
		// and paying for it here would flatter the comparison.
		b.Run("cold/"+name, func(b *testing.B) {
			b.ReportAllocs()
			cv := benchCanvas(b, scale)
			f := benchFace(b, 16)
			sf, err := f.faceAt(scale)
			if err != nil {
				b.Fatalf("faceAt: %v", err)
			}

			for b.Loop() {
				clear(sf.glyphs)
				f.Draw(cv, at, label, col, clip)
			}
		})

		// Warm: the cache is primed, which is every frame after the first.
		b.Run("warm/"+name, func(b *testing.B) {
			b.ReportAllocs()
			cv := benchCanvas(b, scale)
			f := benchFace(b, 16)
			f.Draw(cv, at, label, col, clip)

			for b.Loop() {
				f.Draw(cv, at, label, col, clip)
			}
		})
	}
}

// BenchmarkMeasure is the other half of a label's per-frame cost: widgets
// call it to center text, and it goes through no cache at all.
func BenchmarkMeasure(b *testing.B) {
	f := benchFace(b, 16)
	b.ReportAllocs()

	for b.Loop() {
		f.Measure("Hamburgefonstiv 0123456789")
	}
}
