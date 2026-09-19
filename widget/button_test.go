package widget

import (
	"testing"

	"github.com/romycode/ggui/canvas"
)

// fakeFont records what a widget asks of a Font and paints nothing, so the
// tests exercise the button and not a rasterizer.
type fakeFont struct {
	advance float32

	draws int
	at    canvas.Point
	text  string
	col   canvas.Color
	clip  canvas.Rect
}

func (f *fakeFont) Measure(s string) float32 { return f.advance * float32(len(s)) }

func (f *fakeFont) Draw(_ *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect) {
	f.draws++
	f.at, f.text, f.col, f.clip = at, s, col, clip
}

var testBounds = canvas.Rect{X: 100, Y: 50, Width: 120, Height: 40}

func newTestButton() (*Button, *fakeFont, *int) {
	font := &fakeFont{advance: 10}
	clicks := 0
	b := NewButton("OK", font)
	b.Bounds = testBounds
	b.OnClick = func() { clicks++ }
	return b, font, &clicks
}

func center(r canvas.Rect) (float32, float32) { return r.X + r.Width/2, r.Y + r.Height/2 }

func newTestCanvas(t *testing.T, w, h, pad int) (*canvas.Canvas, []uint32) {
	t.Helper()

	stride := w + pad
	px := make([]uint32, stride*h)
	cv, err := canvas.New(canvas.Buffer{Pixels: px, Width: w, Height: h, Stride: stride}, w, h, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	return cv, px
}

// The far edges belong to the next widget, so two adjacent rects can never
// both claim the same pixel.
func TestContainsIsHalfOpenOnTheFarEdges(t *testing.T) {
	r := canvas.Rect{X: 10, Y: 20, Width: 100, Height: 40}

	cases := []struct {
		name string
		x, y float32
		want bool
	}{
		{"top-left corner", 10, 20, true},
		{"inside", 50, 30, true},
		{"just left", 9.99, 30, false},
		{"just above", 50, 19.99, false},
		{"right edge", 110, 30, false},
		{"bottom edge", 50, 60, false},
		{"last pixel inside", 109.99, 59.99, true},
	}
	for _, c := range cases {
		if got := contains(r, c.x, c.y); got != c.want {
			t.Errorf("contains(%s at %v,%v) = %v, want %v", c.name, c.x, c.y, got, c.want)
		}
	}
}

func TestPressAndReleaseInsideFiresOnce(t *testing.T) {
	b, _, clicks := newTestButton()
	x, y := center(b.Bounds)

	if !b.PointerDown(x, y) {
		t.Fatal("press on the button reported no visible change")
	}
	if !b.Pressed() {
		t.Fatal("press on the button did not press it")
	}
	if !b.PointerUp(x, y) {
		t.Fatal("release reported no visible change")
	}
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times, want 1", *clicks)
	}
	if b.Pressed() {
		t.Fatal("button stayed pressed after the release")
	}
}

// Dragging off a pressed button and letting go is how a user cancels a
// click, so it must not fire.
func TestReleaseOutsideDoesNotFire(t *testing.T) {
	b, _, clicks := newTestButton()
	x, y := center(b.Bounds)

	b.PointerDown(x, y)
	b.PointerMove(x, b.Bounds.Y+b.Bounds.Height+50)
	b.PointerUp(x, b.Bounds.Y+b.Bounds.Height+50)

	if *clicks != 0 {
		t.Fatalf("OnClick called %d times after a release outside, want 0", *clicks)
	}
	if b.Pressed() || b.Hovered() {
		t.Fatalf("button is pressed=%v hovered=%v after the pointer left, want neither", b.Pressed(), b.Hovered())
	}
}

// Drag off and back on: the click is still live, and the button shows it.
func TestDraggingBackOntoAPressedButtonRearmsItsAppearance(t *testing.T) {
	b, _, clicks := newTestButton()
	x, y := center(b.Bounds)

	b.PointerDown(x, y)
	if !b.PointerMove(5, 5) {
		t.Fatal("dragging off a pressed button reported no visible change")
	}
	if b.Pressed() {
		t.Fatal("button still looks pressed with the pointer off it")
	}
	if !b.PointerMove(x, y) {
		t.Fatal("dragging back on reported no visible change")
	}
	if !b.Pressed() {
		t.Fatal("button does not look pressed after the pointer came back")
	}

	b.PointerUp(x, y)
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times, want 1", *clicks)
	}
}

// A press that starts elsewhere and is released over the button is not a
// click on it: the press is what arms it.
func TestReleaseOverTheButtonWithoutAPressOnItDoesNotFire(t *testing.T) {
	b, _, clicks := newTestButton()
	x, y := center(b.Bounds)

	b.PointerDown(5, 5)
	b.PointerMove(x, y)
	if b.PointerUp(x, y) {
		t.Fatal("release of a press that never armed the button reported a change")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times, want 0", *clicks)
	}
}

// Motion arrives on every pixel; only a transition is visible, and only a
// transition should cost a repaint.
func TestPointerMoveReportsAChangeOnlyOnTransitions(t *testing.T) {
	b, _, _ := newTestButton()
	x, y := center(b.Bounds)

	if !b.PointerMove(x, y) {
		t.Fatal("moving onto the button reported no change")
	}
	if b.PointerMove(x+1, y+1) {
		t.Fatal("moving within the button reported a change")
	}
	if !b.PointerMove(5, 5) {
		t.Fatal("moving off the button reported no change")
	}
	if b.PointerMove(6, 6) {
		t.Fatal("moving outside the button reported a change")
	}
}

// The pointer left the surface: the release will go elsewhere, so the press
// has to be abandoned or the button is stuck armed.
func TestPointerLeaveAbandonsAPress(t *testing.T) {
	b, _, clicks := newTestButton()
	x, y := center(b.Bounds)

	b.PointerDown(x, y)
	if !b.PointerLeave() {
		t.Fatal("leaving a pressed button reported no change")
	}
	if b.Pressed() || b.Hovered() {
		t.Fatal("button kept its state after the pointer left")
	}
	if b.PointerLeave() {
		t.Fatal("leaving an idle button reported a change")
	}

	// A later release back over the button must not count as a click.
	b.PointerMove(x, y)
	b.PointerUp(x, y)
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times after an abandoned press, want 0", *clicks)
	}
}

func TestDisabledButtonIgnoresPressesAndNeverAsksForARepaint(t *testing.T) {
	b, _, clicks := newTestButton()
	b.Disabled = true
	x, y := center(b.Bounds)

	if b.PointerMove(x, y) {
		t.Fatal("hovering a disabled button reported a change")
	}
	if b.PointerDown(x, y) {
		t.Fatal("pressing a disabled button reported a change")
	}
	if b.PointerUp(x, y) {
		t.Fatal("releasing a disabled button reported a change")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times on a disabled button, want 0", *clicks)
	}
}

// Disabling while a click is in flight must not let the release fire it.
func TestDisablingMidPressCancelsTheClick(t *testing.T) {
	b, _, clicks := newTestButton()
	x, y := center(b.Bounds)

	b.PointerDown(x, y)
	b.Disabled = true
	b.PointerUp(x, y)

	if *clicks != 0 {
		t.Fatalf("OnClick called %d times after the button was disabled mid-press, want 0", *clicks)
	}
}

func TestButtonWithoutAnOnClickHandlerDoesNotPanic(t *testing.T) {
	b, _, _ := newTestButton()
	b.OnClick = nil
	x, y := center(b.Bounds)

	b.PointerDown(x, y)
	b.PointerUp(x, y)
}

func TestDrawCentersTheLabelAndClipsItToTheButton(t *testing.T) {
	cv, _ := newTestCanvas(t, 320, 160, 0)
	b, font, _ := newTestButton()

	b.Draw(cv)

	if font.draws != 1 {
		t.Fatalf("Font.Draw called %d times, want 1", font.draws)
	}
	if font.text != "OK" {
		t.Fatalf("label drawn = %q, want %q", font.text, "OK")
	}
	// "OK" is 2 runes at 10 units each: 20 wide, centered in 120.
	if want := (canvas.Point{X: 100 + (120-20)/2, Y: 50 + 40/2}); font.at != want {
		t.Fatalf("label anchored at %+v, want %+v", font.at, want)
	}
	if font.clip != b.Bounds {
		t.Fatalf("label clipped to %+v, want the button's bounds %+v", font.clip, b.Bounds)
	}
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
}

// Each state has to be visible, or the button gives no feedback before the
// click. The label color is checked through the Font, the fill through
// pixels.
func TestEachStateDrawsDifferently(t *testing.T) {
	cv, px := newTestCanvas(t, 320, 160, 0)
	b, font, _ := newTestButton()
	x, y := center(b.Bounds)
	probe := int(y)*320 + int(x)

	b.Draw(cv)
	rest, restLabel := px[probe], font.col

	b.PointerMove(x, y)
	b.Draw(cv)
	hover := px[probe]

	b.PointerDown(x, y)
	b.Draw(cv)
	pressed, pressedLabel := px[probe], font.col

	b.PointerUp(x, y)
	b.Disabled = true
	b.Draw(cv)
	disabled, disabledLabel := px[probe], font.col

	fills := map[string]uint32{"rest": rest, "hover": hover, "pressed": pressed, "disabled": disabled}
	seen := map[uint32]string{}
	for name, v := range fills {
		if other, dup := seen[v]; dup {
			t.Errorf("%s and %s render the same fill %#08x", name, other, v)
		}
		seen[v] = name
	}

	labels := map[string]canvas.Color{"rest": restLabel, "pressed": pressedLabel, "disabled": disabledLabel}
	if labels["rest"] == labels["pressed"] || labels["rest"] == labels["disabled"] || labels["pressed"] == labels["disabled"] {
		t.Errorf("label colors do not distinguish rest/pressed/disabled: %+v", labels)
	}
}

func TestDrawWithoutAFontOrLabelStillDrawsTheButton(t *testing.T) {
	cv, px := newTestCanvas(t, 320, 160, 0)
	x, y := center(testBounds)

	b := NewButton("OK", nil)
	b.Bounds = testBounds
	b.Draw(cv)
	if px[int(y)*320+int(x)] == 0 {
		t.Fatal("a button with no font drew nothing")
	}

	b2, font, _ := newTestButton()
	b2.Label = ""
	b2.Draw(cv)
	if font.draws != 0 {
		t.Fatal("an empty label still called Font.Draw")
	}
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
}

func TestZeroBorderDrawsNoOutline(t *testing.T) {
	cv, _ := newTestCanvas(t, 320, 160, 0)
	b, _, _ := newTestButton()
	b.Style.Border = 0

	b.Draw(cv)

	// StrokeRoundedRect rejects a zero width as an error; the button must
	// not ask for one.
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error with Border = 0: %v", err)
	}
}

// Nothing written by the button may land in row padding.
func TestDrawStaysInsideTheVisibleRegion(t *testing.T) {
	const w, h, pad = 320, 160, 8
	cv, px := newTestCanvas(t, w, h, pad)
	for i := range px {
		px[i] = 0xdeadbeef
	}
	b, _, _ := newTestButton()
	// Straddle the right edge so the button is clipped by the buffer.
	b.Bounds = canvas.Rect{X: w - 40, Y: 20, Width: 120, Height: 40}

	b.Draw(cv)

	for y := range h {
		for x := w; x < w+pad; x++ {
			if got := px[y*(w+pad)+x]; got != 0xdeadbeef {
				t.Fatalf("padding written at row %d, column %d: %#08x", y, x, got)
			}
		}
	}
}

// canvas draw paths are asserted allocation-free, and a widget that draws
// through them must not be what breaks that.
func TestDrawDoesNotAllocate(t *testing.T) {
	cv, _ := newTestCanvas(t, 320, 160, 0)
	b, _, _ := newTestButton()
	x, y := center(b.Bounds)
	b.PointerMove(x, y)
	// Focused as well as hovered, so the ring is on the measured path.
	b.SetFocused(true)

	if allocs := testing.AllocsPerRun(100, func() { b.Draw(cv) }); allocs != 0 {
		t.Fatalf("Button.Draw allocates %v times per call, want 0", allocs)
	}
}
