package main

import (
	"testing"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/widget"
)

func TestLayoutAnchorsButtonAgainstTheRightPadding(t *testing.T) {
	l := computeLayout(600, 300)

	if got, want := l.button.X+l.button.Width, float32(600-padding); got != want {
		t.Fatalf("button right edge = %v, want %v", got, want)
	}
	if got, want := l.input.X, float32(padding); got != want {
		t.Fatalf("input left edge = %v, want %v", got, want)
	}
}

func TestLayoutKeepsTheGapBetweenInputAndButton(t *testing.T) {
	l := computeLayout(600, 300)

	if got, want := l.button.X-(l.input.X+l.input.Width), float32(gap); got != want {
		t.Fatalf("gap between input and button = %v, want %v", got, want)
	}
}

func TestLayoutCentersControlsVertically(t *testing.T) {
	l := computeLayout(600, 300)

	above := l.input.Y
	below := 300 - (l.input.Y + l.input.Height)
	if above != below {
		t.Fatalf("controls are not centered: %v above, %v below", above, below)
	}
	if l.button.Y != l.input.Y || l.button.Height != l.input.Height {
		t.Fatalf("button and input are not on the same row: %+v vs %+v", l.button, l.input)
	}
}

// A window narrower than the button plus its padding must not produce a
// negative input width: canvas rejects those, and the sticky error would
// silently kill every later draw call in the frame.
func TestLayoutClampsInputWidthOnANarrowWindow(t *testing.T) {
	l := computeLayout(40, 300)

	if l.input.Width < 0 {
		t.Fatalf("input width = %v, want it clamped to zero or more", l.input.Width)
	}
}

// Hit testing is half-open: the far edge belongs to the next widget, so two
// adjacent rects can never both claim the same pixel.
func TestHitIsHalfOpenOnTheFarEdges(t *testing.T) {
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
		if got := hit(r, c.x, c.y); got != c.want {
			t.Errorf("hit(%s at %v,%v) = %v, want %v", c.name, c.x, c.y, got, c.want)
		}
	}
}

// Pressing the button and releasing somewhere else must not activate it.
// This is the one piece of real button semantics the example exists to show.
func TestButtonDoesNotFireWhenTheReleaseLandsOutside(t *testing.T) {
	l := computeLayout(600, 300)
	u := newUI(bitmapFont{})
	u.field.SetText("hello")

	inX, inY := center(l.button)
	u.pointerPressed(l, inX, inY)
	if !u.button.Pressed() {
		t.Fatalf("press inside the button did not press it")
	}

	outY := l.button.Y + l.button.Height + 50
	u.pointerMoved(l, inX, outY)
	if u.button.Pressed() {
		t.Fatalf("button still looked pressed after moving outside")
	}

	if fired := u.pointerReleased(l, inX, outY); fired {
		t.Fatalf("button fired on a release outside its rect")
	}
	if u.text() != "hello" {
		t.Fatalf("text = %q, want it untouched", u.text())
	}
	if u.button.Pressed() {
		t.Fatalf("button stayed pressed after the release")
	}
}

func TestNewAppInitializesItsUI(t *testing.T) {
	a, _ := newTestApp(t)

	if a.ui.font == nil {
		t.Fatal("new app has no UI font")
	}
	if a.ui.button == nil {
		t.Fatal("new app has no button")
	}
	if a.ui.field == nil {
		t.Fatal("new app has no field")
	}
	if a.ui.focus == nil {
		t.Fatal("new app has no focus chain")
	}
}

func TestNewAppButtonClearsTheStoredUI(t *testing.T) {
	a, _ := newTestApp(t)
	a.ui.field.SetText("hello")
	l := computeLayout(defaultWidth, defaultHeight)
	x, y := center(l.button)

	a.ui.pointerPressed(l, x, y)
	if fired := a.ui.pointerReleased(l, x, y); !fired {
		t.Fatal("button callback did not report activation on the app UI")
	}
	if a.ui.text() != "" {
		t.Fatalf("text = %q after Clear, want empty", a.ui.text())
	}
}

func TestButtonClearsTheTextWhenPressedAndReleasedInside(t *testing.T) {
	l := computeLayout(600, 300)
	u := newUI(bitmapFont{})
	u.field.SetText("hello")

	x, y := center(l.button)
	u.pointerPressed(l, x, y)
	if fired := u.pointerReleased(l, x, y); !fired {
		t.Fatalf("button did not fire on a press and release inside")
	}
	if u.text() != "" {
		t.Fatalf("text = %q, want it cleared", u.text())
	}
}

func TestPressingTheInputFocusesItAndPressingElsewhereDoesNot(t *testing.T) {
	l := computeLayout(600, 300)
	u := newUI(bitmapFont{})

	x, y := center(l.input)
	u.pointerPressed(l, x, y)
	if !u.editing() {
		t.Fatalf("press inside the input did not focus it")
	}

	u.pointerPressed(l, 5, 5)
	if u.editing() {
		t.Fatalf("press outside the input left it focused")
	}
}

// The field is widget.TextField now: what this checks is the wiring, not
// the editing rules, which widget tests on its own.
func TestTypingGoesToTheFocusedFieldOnly(t *testing.T) {
	l := computeLayout(600, 300)
	u := newUI(bitmapFont{})

	if u.insert("x") || u.text() != "" {
		t.Fatal("an unfocused field took text")
	}

	x, y := center(l.input)
	u.pointerPressed(l, x, y)
	if !u.editing() {
		t.Fatal("a press inside the field did not focus it")
	}
	if !u.insert("añ") || u.text() != "añ" {
		t.Fatalf("text = %q after typing, want %q", u.text(), "añ")
	}
	if !u.keyDown(widget.KeyBackspace) || u.text() != "a" {
		t.Fatalf("text = %q after Backspace, want %q", u.text(), "a")
	}
	if u.insert("\r") {
		t.Fatal("a control character reached the field")
	}

	u.pointerPressed(l, 5, 5)
	if u.editing() {
		t.Fatal("a press outside the field left it focused")
	}
}

// Tab is what the chain is for, and it now has two widgets to walk.
func TestTabMovesTheFocusBetweenTheFieldAndTheButton(t *testing.T) {
	u := newUI(bitmapFont{})
	u.keyDown(widget.KeyTab)
	if !u.editing() {
		t.Fatal("Tab did not land on the field first")
	}
	u.keyDown(widget.KeyTab)
	if u.editing() || !u.button.Focused() {
		t.Fatal("a second Tab did not move on to the button")
	}
}

// Hover only needs a redraw when it actually flips, otherwise every motion
// event in the window repaints it.
func TestHoverReportsAChangeOnlyOnTransitions(t *testing.T) {
	l := computeLayout(600, 300)
	u := newUI(bitmapFont{})

	x, y := center(l.button)
	if changed := u.pointerMoved(l, x, y); !changed {
		t.Fatalf("moving onto the button reported no change")
	}
	if changed := u.pointerMoved(l, x+1, y+1); changed {
		t.Fatalf("moving within the button reported a change")
	}
	if changed := u.pointerMoved(l, 5, 5); !changed {
		t.Fatalf("moving off the button reported no change")
	}
}

func center(r canvas.Rect) (float32, float32) {
	return r.X + r.Width/2, r.Y + r.Height/2
}
