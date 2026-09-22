package main

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/text"
	"github.com/romycode/ggui/widget"
)

const (
	testWidth  = 640
	testHeight = 220
	// testPad is extra stride beyond the visible width, so the tests can
	// prove no draw call reaches into row padding.
	testPad = 8
)

// newTestCanvas builds a canvas over padded storage and returns both, plus
// the sentinel the padding was filled with.
func newTestCanvas(t *testing.T) (*canvas.Canvas, []uint32) {
	t.Helper()

	stride := testWidth + testPad
	px := make([]uint32, stride*testHeight)
	for i := range px {
		px[i] = paddingSentinel
	}

	cv, err := canvas.New(canvas.Buffer{
		Pixels: px,
		Width:  testWidth,
		Height: testHeight,
		Stride: stride,
	}, testWidth, testHeight, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	return cv, px
}

const paddingSentinel = 0xdeadbeef

// A frame has to render without tripping canvas's sticky error: the first
// bad argument turns every later call in that frame into a no-op, so a
// single mistake in draw would blank the window rather than misdraw it.
func TestDrawAFrameRecordsNoCanvasError(t *testing.T) {
	cv, _ := newTestCanvas(t)
	u := newUI(bitmapFont{})
	u.field.SetText("hello world")
	u.focus.Focus(u.field)

	draw(cv, computeLayout(testWidth, testHeight), u)

	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error after a frame: %v", err)
	}
}

// Glyph blitting writes into the borrowed pixels directly, bypassing the
// canvas clipping that protects row padding. This is the test that says it
// stays inside the visible region anyway.
func TestDrawNeverWritesIntoRowPadding(t *testing.T) {
	cv, px := newTestCanvas(t)
	stride := testWidth + testPad

	// Text long enough to overflow the input and run at the button.
	u := newUI(bitmapFont{})
	u.field.SetText("the quick brown fox jumps over the lazy dog 0123456789")
	u.focus.Focus(u.field)
	draw(cv, computeLayout(testWidth, testHeight), u)

	for y := range testHeight {
		for x := testWidth; x < stride; x++ {
			if got := px[y*stride+x]; got != paddingSentinel {
				t.Fatalf("padding written at row %d, column %d: %#08x", y, x, got)
			}
		}
	}
}

// Text that does not fit the input must be clipped at the control's edge
// rather than spilling across the gap and over the button.
func TestLongTextIsClippedToTheInput(t *testing.T) {
	cv, px := newTestCanvas(t)
	stride := testWidth + testPad
	l := computeLayout(testWidth, testHeight)

	// A frame with an empty, unfocused input is the baseline: the button is
	// drawn in both, so any difference over the button comes from the text.
	draw(cv, l, newUI(bitmapFont{}))
	baseline := make([]uint32, len(px))
	copy(baseline, px)

	u := newUI(bitmapFont{})
	u.field.SetText("the quick brown fox jumps over the lazy dog 0123456789")
	u.focus.Focus(u.field)
	draw(cv, l, u)

	x0, x1 := int(l.button.X), int(l.button.X+l.button.Width)
	y0, y1 := int(l.button.Y), int(l.button.Y+l.button.Height)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			i := y*stride + x
			if px[i] != baseline[i] {
				t.Fatalf("text reached the button at %d,%d: %#08x, want %#08x", x, y, px[i], baseline[i])
			}
		}
	}
}

// The caret is the only thing that distinguishes a focused empty input from
// an unfocused one that is showing its placeholder, so it has to render.
func TestFocusChangesWhatTheInputRenders(t *testing.T) {
	cv, px := newTestCanvas(t)
	l := computeLayout(testWidth, testHeight)

	draw(cv, l, newUI(bitmapFont{}))
	unfocused := make([]uint32, len(px))
	copy(unfocused, px)

	focused := newUI(bitmapFont{})
	focused.focus.Focus(focused.field)
	draw(cv, l, focused)

	if equalPixels(px, unfocused) {
		t.Fatal("focusing the input changed nothing on screen")
	}
}

// Hover has to be visible or the button gives no feedback before the click.
func TestHoverChangesWhatTheButtonRenders(t *testing.T) {
	cv, px := newTestCanvas(t)
	l := computeLayout(testWidth, testHeight)

	draw(cv, l, newUI(bitmapFont{}))
	plain := make([]uint32, len(px))
	copy(plain, px)

	hovered := newUI(bitmapFont{})
	x, y := center(l.button)
	hovered.pointerMoved(l, x, y)
	draw(cv, l, hovered)

	if equalPixels(px, plain) {
		t.Fatal("hovering the button changed nothing on screen")
	}
}

// Every rune the face can be asked for must resolve to a row, including the
// ones it does not carry — those fall back to the replacement glyph rather
// than reading past the mask.
func TestGlyphRowResolvesPrintableAsciiAndFallsBackOtherwise(t *testing.T) {
	for r := rune(0x20); r < 0x7f; r++ {
		if _, ok := glyphRow(r); !ok {
			t.Fatalf("no glyph row for %q", r)
		}
	}
	// Accented characters are exactly what Composer produces and exactly
	// what Face7x13 lacks.
	if _, ok := glyphRow('é'); !ok {
		t.Fatal("no fallback glyph row for 'é'")
	}
}

// A window too narrow to hold the button still has to produce a frame:
// computeLayout clamps the input, and a clamped zero-width rect is a
// documented canvas no-op rather than an error.
func TestDrawSurvivesAWindowNarrowerThanTheButton(t *testing.T) {
	stride := 40
	px := make([]uint32, stride*testHeight)
	cv, err := canvas.New(canvas.Buffer{
		Pixels: px, Width: stride, Height: testHeight, Stride: stride,
	}, stride, testHeight, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}

	u := newUI(bitmapFont{})
	u.field.SetText("hello")
	u.focus.Focus(u.field)
	draw(cv, computeLayout(float32(stride), testHeight), u)

	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error on a narrow window: %v", err)
	}
}

// The same frame with a real outline font, the case the system font path
// takes: nothing may leave its control, reach the row padding, or trip the
// canvas's sticky error.
func TestDrawWithATrueTypeFontStaysInsideItsControls(t *testing.T) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatalf("opentype.Parse: %v", err)
	}
	face, err := text.NewFace(parsed, fontSize)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	cv, px := newTestCanvas(t)
	stride := testWidth + testPad
	l := computeLayout(testWidth, testHeight)

	draw(cv, l, newUI(face))
	baseline := make([]uint32, len(px))
	copy(baseline, px)

	u := newUI(face)
	u.field.SetText("the quick brown fox jumps over the lazy dog 0123456789 àéîõü")
	u.focus.Focus(u.field)
	draw(cv, l, u)

	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
	if err := face.Err(); err != nil {
		t.Fatalf("face error: %v", err)
	}
	for y := range testHeight {
		for x := testWidth; x < stride; x++ {
			if got := px[y*stride+x]; got != paddingSentinel {
				t.Fatalf("padding written at row %d, column %d: %#08x", y, x, got)
			}
		}
	}

	// The long line overflows the input; the button must be untouched.
	for y := int(l.button.Y); y < int(l.button.Y+l.button.Height); y++ {
		for x := int(l.button.X); x < int(l.button.X+l.button.Width); x++ {
			if i := y*stride + x; px[i] != baseline[i] {
				t.Fatalf("text reached the button at %d,%d", x, y)
			}
		}
	}
}

// BenchmarkTextFieldWithASystemFont is the one measurement taken against a
// real outline font: widget cannot import text, so this belongs here. It
// is informative — what it says is that the field's cost does not follow
// the length of the text — and it is skipped on a machine with no font we
// can read.
func BenchmarkTextFieldWithASystemFont(b *testing.B) {
	face, err := text.NewSystemFace(fontSize, text.Regular)
	if err != nil {
		b.Skipf("no system font: %v", err)
	}
	defer face.Close()

	px := make([]uint32, testWidth*testHeight)
	cv, err := canvas.New(canvas.Buffer{
		Pixels: px, Width: testWidth, Height: testHeight, Stride: testWidth,
	}, testWidth, testHeight, 1)
	if err != nil {
		b.Fatalf("canvas.New: %v", err)
	}
	l := computeLayout(testWidth, testHeight)

	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("draw/n=%d", n), func(b *testing.B) {
			u := newUI(face)
			u.place(l)
			u.focus.Focus(u.field)
			u.field.SetText(strings.Repeat("x", n))
			b.ReportAllocs()
			for b.Loop() {
				u.field.Draw(cv)
			}
		})
		b.Run(fmt.Sprintf("type/n=%d", n), func(b *testing.B) {
			u := newUI(face)
			u.place(l)
			u.focus.Focus(u.field)
			u.field.SetText(strings.Repeat("x", n))
			b.ReportAllocs()
			for b.Loop() {
				u.field.Insert("y")
				u.field.KeyDown(widget.KeyBackspace)
			}
		})
	}
}

func equalPixels(a, b []uint32) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
