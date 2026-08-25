package main

import (
	"testing"

	"github.com/romycode/ggui/canvas"
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
	u := &ui{text: []rune("hello")}

	inX, inY := center(l.button)
	u.pointerPressed(l, inX, inY)
	if !u.armed {
		t.Fatalf("press inside the button did not arm it")
	}

	if fired := u.pointerReleased(l, inX, l.button.Y+l.button.Height+50); fired {
		t.Fatalf("button fired on a release outside its rect")
	}
	if string(u.text) != "hello" {
		t.Fatalf("text = %q, want it untouched", string(u.text))
	}
	if u.armed {
		t.Fatalf("button stayed armed after the release")
	}
}

func TestButtonClearsTheTextWhenPressedAndReleasedInside(t *testing.T) {
	l := computeLayout(600, 300)
	u := &ui{text: []rune("hello")}

	x, y := center(l.button)
	u.pointerPressed(l, x, y)
	if fired := u.pointerReleased(l, x, y); !fired {
		t.Fatalf("button did not fire on a press and release inside")
	}
	if string(u.text) != "" {
		t.Fatalf("text = %q, want it cleared", string(u.text))
	}
}

func TestPressingTheInputFocusesItAndPressingElsewhereDoesNot(t *testing.T) {
	l := computeLayout(600, 300)
	u := &ui{}

	x, y := center(l.input)
	u.pointerPressed(l, x, y)
	if !u.focused {
		t.Fatalf("press inside the input did not focus it")
	}

	u.pointerPressed(l, 5, 5)
	if u.focused {
		t.Fatalf("press outside the input left it focused")
	}
}

func TestBackspaceOnEmptyTextIsANoop(t *testing.T) {
	u := &ui{focused: true}

	if changed := u.backspace(); changed {
		t.Fatalf("backspace on empty text reported a change")
	}
	if len(u.text) != 0 {
		t.Fatalf("text = %q, want it still empty", string(u.text))
	}
}

// Backspace deletes one rune, not one byte: a multi-byte character has to
// disappear in a single keystroke.
func TestBackspaceDeletesOneRuneNotOneByte(t *testing.T) {
	u := &ui{focused: true, text: []rune("añ")}

	if changed := u.backspace(); !changed {
		t.Fatalf("backspace reported no change")
	}
	if got := string(u.text); got != "a" {
		t.Fatalf("text = %q, want %q", got, "a")
	}
}

func TestInsertIsIgnoredWhileTheInputIsNotFocused(t *testing.T) {
	u := &ui{focused: false}

	if changed := u.insert("x"); changed {
		t.Fatalf("insert reported a change while unfocused")
	}
	if len(u.text) != 0 {
		t.Fatalf("text = %q, want it empty", string(u.text))
	}
}

// The composer returns "" for a key that produces no text (arrows, F-keys).
// Appending that would redraw the window for nothing.
func TestInsertOfEmptyTextReportsNoChange(t *testing.T) {
	u := &ui{focused: true}

	if changed := u.insert(""); changed {
		t.Fatalf("insert(\"\") reported a change")
	}
}

// Hover only needs a redraw when it actually flips, otherwise every motion
// event in the window repaints it.
func TestHoverReportsAChangeOnlyOnTransitions(t *testing.T) {
	l := computeLayout(600, 300)
	u := &ui{}

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

// Keysym.Rune maps Return to '\r' and Tab to '\t' through the legacy table,
// so Composer.Feed hands them back as ordinary text. Storing them would draw
// a replacement glyph for a key that should never have reached the input.
func TestInsertDropsControlCharacters(t *testing.T) {
	u := &ui{focused: true}

	if changed := u.insert("\r"); changed {
		t.Fatalf("insert(%q) reported a change", "\r")
	}
	if changed := u.insert("\t"); changed {
		t.Fatalf("insert(%q) reported a change", "\t")
	}
	if changed := u.insert("a\rb"); !changed {
		t.Fatalf("insert(%q) reported no change", "a\rb")
	}
	if got := string(u.text); got != "ab" {
		t.Fatalf("text = %q, want %q", got, "ab")
	}
}
