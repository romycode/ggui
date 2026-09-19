package text

import (
	"math"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/widget"
)

// The compile-time proof that a Face is what the widgets ask for.
var _ widget.Font = (*Face)(nil)

const sentinel = 0xdeadbeef

func newTestFace(t *testing.T, size float32) *Face {
	t.Helper()

	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f, err := NewFace(parsed, size)
	if err != nil {
		t.Fatalf("NewFace: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// newTestCanvas builds a logical w x h canvas at scale over padded storage
// pre-filled with fill, and returns the pixels and their stride.
func newTestCanvas(t *testing.T, w, h int, scale float32, fill uint32) (*canvas.Canvas, []uint32, int) {
	t.Helper()

	pw, ph := int(float32(w)*scale), int(float32(h)*scale)
	stride := pw + 8
	px := make([]uint32, stride*ph)
	for i := range px {
		px[i] = sentinel
	}
	for y := range ph {
		for x := range pw {
			px[y*stride+x] = fill
		}
	}

	cv, err := canvas.New(canvas.Buffer{Pixels: px, Width: pw, Height: ph, Stride: stride}, w, h, scale)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	return cv, px, stride
}

// inkBounds returns the bounding box of every pixel that differs from
// background inside the visible area, and whether there was any.
func inkBounds(px []uint32, stride, pw, ph int, background uint32) (x0, y0, x1, y1 int, ok bool) {
	x0, y0, x1, y1 = pw, ph, -1, -1
	for y := range ph {
		for x := range pw {
			if px[y*stride+x] == background {
				continue
			}
			x0, y0 = min(x0, x), min(y0, y)
			x1, y1 = max(x1, x+1), max(y1, y+1)
		}
	}
	return x0, y0, x1, y1, x1 >= 0
}

var (
	white = canvas.Color{R: 255, G: 255, B: 255, A: 255}
	black = uint32(0xff000000)
)

func TestNewFaceRejectsBadArguments(t *testing.T) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewFace(nil, 16); err == nil {
		t.Error("nil font accepted")
	}
	for _, size := range []float32{0, -1, float32(math.NaN()), float32(math.Inf(1))} {
		if _, err := NewFace(parsed, size); err == nil {
			t.Errorf("size %v accepted", size)
		}
	}
}

func TestMeasure(t *testing.T) {
	f := newTestFace(t, 16)

	if got := f.Measure(""); got != 0 {
		t.Errorf("Measure(\"\") = %v, want 0", got)
	}
	// A proportional font: wide letters are wider than narrow ones, and
	// more letters are wider than fewer.
	if i, w := f.Measure("iii"), f.Measure("WWW"); !(i < w) {
		t.Errorf("Measure(iii) = %v, Measure(WWW) = %v: want iii narrower", i, w)
	}
	if a, b := f.Measure("ab"), f.Measure("abc"); !(a < b) {
		t.Errorf("Measure(ab) = %v, Measure(abc) = %v: want ab narrower", a, b)
	}
	// Control characters take no space and draw nothing.
	if a, b := f.Measure("a\nb\t"), f.Measure("ab"); a != b {
		t.Errorf("Measure with controls = %v, without = %v: want equal", a, b)
	}
}

// Advances are linear in size, which is what lets Measure answer without
// knowing the canvas scale.
func TestMeasureScalesLinearlyWithSize(t *testing.T) {
	small, big := newTestFace(t, 10), newTestFace(t, 20)

	s, b := small.Measure("The quick brown fox"), big.Measure("The quick brown fox")
	if math.Abs(float64(b)-2*float64(s)) > 0.5 {
		t.Errorf("width at 20 = %v, at 10 = %v: want about double", b, s)
	}
}

func TestDrawPaintsTextInsideTheClipAndNothingOutside(t *testing.T) {
	f := newTestFace(t, 16)
	cv, px, stride := newTestCanvas(t, 200, 60, 1, black)

	clip := canvas.Rect{X: 20, Y: 10, Width: 100, Height: 40}
	f.Draw(cv, canvas.Point{X: 10, Y: 30}, "Hello, world — this runs past the clip", white, clip)

	x0, y0, x1, y1, ok := inkBounds(px, stride, 200, 60, black)
	if !ok {
		t.Fatal("nothing was drawn")
	}
	if x0 < 20 || y0 < 10 || x1 > 120 || y1 > 50 {
		t.Errorf("ink spans (%d,%d)-(%d,%d), escaping the clip (20,10)-(120,50)", x0, y0, x1, y1)
	}
	// The text started left of the clip, so its start is cut and the ink
	// reaches the clip's left edge... and it ran on, so it reaches the
	// right one.
	if x1 < 110 {
		t.Errorf("ink ends at x=%d: the long string should have run to the clip's right edge", x1)
	}
	if err := f.Err(); err != nil {
		t.Errorf("Err = %v", err)
	}
}

// Text goes through canvas.DrawMask, so it is damage-tracked like every
// other drawing rather than written behind the canvas's back.
func TestDrawAccumulatesDamage(t *testing.T) {
	f := newTestFace(t, 16)
	cv, px, stride := newTestCanvas(t, 200, 60, 1, black)

	if _, ok := cv.Damage(); ok {
		t.Fatal("the canvas reported damage before anything was drawn")
	}

	f.Draw(cv, canvas.Point{X: 20, Y: 30}, "Hello", white, canvas.Rect{Width: 200, Height: 60})

	dmg, ok := cv.Damage()
	if !ok {
		t.Fatal("drawing text reported no damage")
	}

	// Every inked pixel must be inside the damage, and both bounds are
	// half-open, so the two rectangles can be compared directly. A glyph
	// mask is tight around its own ink, so this is not just containment:
	// the damage is exactly the region the text touched.
	x0, y0, x1, y1, inked := inkBounds(px, stride, 200, 60, black)
	if !inked {
		t.Fatal("nothing was drawn")
	}
	want := canvas.PixelRect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
	if dmg != want {
		t.Errorf("damage = %+v, want the inked region %+v", dmg, want)
	}
}

// Damage must not report pixels the clip stopped from being drawn.
func TestDamageStaysInsideTheClip(t *testing.T) {
	f := newTestFace(t, 16)
	cv, _, _ := newTestCanvas(t, 200, 60, 1, black)

	clip := canvas.Rect{X: 20, Y: 10, Width: 100, Height: 40}
	f.Draw(cv, canvas.Point{X: 10, Y: 30}, "Hello, world — this runs past the clip", white, clip)

	dmg, ok := cv.Damage()
	if !ok {
		t.Fatal("drawing text reported no damage")
	}
	if dmg.X < 20 || dmg.Y < 10 || dmg.X+dmg.Width > 120 || dmg.Y+dmg.Height > 50 {
		t.Errorf("damage %+v escapes the clip (20,10)-(120,50)", dmg)
	}
}

func TestDrawNeverWritesIntoRowPadding(t *testing.T) {
	f := newTestFace(t, 16)
	cv, px, stride := newTestCanvas(t, 120, 40, 1, black)

	// A clip wider than the buffer, and text that runs off its right edge.
	f.Draw(cv, canvas.Point{X: 60, Y: 20}, "overflowing text that leaves the buffer",
		white, canvas.Rect{X: 0, Y: 0, Width: 400, Height: 40})

	for y := range 40 {
		for x := 120; x < stride; x++ {
			if got := px[y*stride+x]; got != sentinel {
				t.Fatalf("padding written at row %d, column %d: %#08x", y, x, got)
			}
		}
	}
}

func TestDrawCentersTheLineBoxVerticallyOnAtY(t *testing.T) {
	f := newTestFace(t, 20)
	cv, px, stride := newTestCanvas(t, 100, 60, 1, black)

	f.Draw(cv, canvas.Point{X: 10, Y: 30}, "Hxg", white, canvas.Rect{Width: 100, Height: 60})

	_, y0, _, y1, ok := inkBounds(px, stride, 100, 60, black)
	if !ok {
		t.Fatal("nothing was drawn")
	}
	// Ink includes a descender and a cap, so its middle is not the line
	// box's, but it has to be close to it and never off by a big fraction
	// of the size.
	if mid := float64(y0+y1) / 2; math.Abs(mid-30) > 20/4 {
		t.Errorf("ink is centered on y=%v, want about 30", mid)
	}
}

// A 16-unit face on a 2x canvas has to draw 32-pixel glyphs — sharp, not a
// scaled-up bitmap.
func TestDrawRasterizesAtTheCanvasScale(t *testing.T) {
	f := newTestFace(t, 16)

	height := func(scale float32) int {
		cv, px, stride := newTestCanvas(t, 100, 60, scale, black)
		f.Draw(cv, canvas.Point{X: 10, Y: 30}, "H", white, canvas.Rect{Width: 100, Height: 60})
		_, y0, _, y1, ok := inkBounds(px, stride, int(100*scale), int(60*scale), black)
		if !ok {
			t.Fatalf("scale %v: nothing was drawn", scale)
		}
		return y1 - y0
	}

	h1, h2 := height(1), height(2)
	if ratio := float64(h2) / float64(h1); math.Abs(ratio-2) > 0.25 {
		t.Errorf("cap height is %d px at 1x and %d px at 2x, ratio %.2f, want about 2", h1, h2, ratio)
	}
	if len(f.scaled) != 2 {
		t.Errorf("%d faces cached, want one per scale seen (2)", len(f.scaled))
	}
}

func TestDrawLeavesTheBackgroundAloneForNothingToDraw(t *testing.T) {
	f := newTestFace(t, 16)
	cv, px, stride := newTestCanvas(t, 100, 40, 1, black)
	full := canvas.Rect{Width: 100, Height: 40}

	f.Draw(cv, canvas.Point{X: 10, Y: 20}, "", white, full)
	f.Draw(cv, canvas.Point{X: 10, Y: 20}, "text", canvas.Color{R: 255, G: 255, B: 255, A: 0}, full)
	f.Draw(cv, canvas.Point{X: 10, Y: 20}, "text", white, canvas.Rect{X: 10, Y: 10})
	f.Draw(cv, canvas.Point{X: 10, Y: 20}, "\n\t", white, full)

	if _, _, _, _, ok := inkBounds(px, stride, 100, 40, black); ok {
		t.Error("something was drawn")
	}
}

func TestDrawFallsBackToTheFontsGlyphForUnknownRunes(t *testing.T) {
	f := newTestFace(t, 20)
	cv, px, stride := newTestCanvas(t, 100, 60, 1, black)

	// U+E000 is a private-use codepoint no font carries.
	f.Draw(cv, canvas.Point{X: 10, Y: 30}, "\ue000", white, canvas.Rect{Width: 100, Height: 60})

	if _, _, _, _, ok := inkBounds(px, stride, 100, 60, black); !ok {
		t.Error("an unknown rune drew nothing, want the font's fallback glyph")
	}
	if err := cv.Err(); err != nil {
		t.Errorf("canvas error: %v", err)
	}
}

func TestScaledFacesAreBounded(t *testing.T) {
	f := newTestFace(t, 16)

	for i := range maxScaledFaces * 3 {
		if _, err := f.faceAt(1 + float32(i)/10); err != nil {
			t.Fatal(err)
		}
		if len(f.scaled) > maxScaledFaces {
			t.Fatalf("%d faces cached, want at most %d", len(f.scaled), maxScaledFaces)
		}
	}
}

func TestNewSystemFaceReportsNotFoundForAnUnknownFamily(t *testing.T) {
	if _, err := NewSystemFace(16, Regular, "No Such Family Anywhere"); err == nil {
		t.Fatal("NewSystemFace succeeded for a family that cannot exist")
	}
}
