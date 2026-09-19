package widget

import (
	"testing"

	"github.com/romycode/ggui/canvas"
)

func newFocusedTestButton() (*Button, *int) {
	b, _, clicks := newTestButton()
	b.SetFocused(true)
	return b, clicks
}

func TestSpaceArmsOnTheDownAndFiresOnTheUp(t *testing.T) {
	b, clicks := newFocusedTestButton()

	if !b.KeyDown(KeySpace) {
		t.Fatal("arming with Space reported no change")
	}
	if !b.Pressed() {
		t.Fatal("a button with Space held is not showing as pressed")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times on the press, want 0", *clicks)
	}

	if !b.KeyUp(KeySpace) {
		t.Fatal("releasing Space reported no change")
	}
	if b.Pressed() {
		t.Fatal("button stayed pressed after Space was released")
	}
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times, want 1", *clicks)
	}

	// A second release with nothing armed must not fire again.
	if b.KeyUp(KeySpace) {
		t.Fatal("releasing Space on an idle button reported a change")
	}
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times after a stray release, want 1", *clicks)
	}
}

// Enter activates outright, so it never changes how the button looks. The
// bool means "a repaint would look different" and nothing else, which is
// exactly why it is false here.
func TestEnterFiresOnTheDownAndReportsNoVisibleChange(t *testing.T) {
	b, clicks := newFocusedTestButton()

	if b.KeyDown(KeyEnter) {
		t.Fatal("Enter reported a visible change")
	}
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times, want 1", *clicks)
	}
	if b.Pressed() {
		t.Fatal("Enter left the button latched")
	}
	if b.KeyUp(KeyEnter) {
		t.Fatal("releasing Enter reported a change")
	}
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times after the release, want 1", *clicks)
	}
}

// Escape is the keyboard's version of dragging off a pressed button.
func TestEscapeAbandonsAnArmedSpace(t *testing.T) {
	b, clicks := newFocusedTestButton()

	b.KeyDown(KeySpace)
	if !b.KeyDown(KeyEscape) {
		t.Fatal("Escape on an armed button reported no change")
	}
	if b.Pressed() {
		t.Fatal("button stayed pressed after Escape")
	}

	b.KeyUp(KeySpace)
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times after Escape, want 0", *clicks)
	}
	if b.KeyDown(KeyEscape) {
		t.Fatal("Escape on an idle button reported a change")
	}
}

func TestKeysAreIgnoredWhileUnfocused(t *testing.T) {
	b, _, clicks := newTestButton()

	for _, k := range []Key{KeyNone, KeySpace, KeyEnter, KeyEscape} {
		if b.KeyDown(k) {
			t.Fatalf("key %d reported a change on an unfocused button", k)
		}
		if b.KeyUp(k) {
			t.Fatalf("releasing key %d reported a change on an unfocused button", k)
		}
	}
	if b.Pressed() {
		t.Fatal("an unfocused button was armed from the keyboard")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times on an unfocused button, want 0", *clicks)
	}
}

// KeyNone is what a caller passes for a key it could not map, so it has to
// be inert even on the focused widget.
func TestUnmappedKeysDoNothingToAFocusedButton(t *testing.T) {
	b, clicks := newFocusedTestButton()

	if b.KeyDown(KeyNone) {
		t.Fatal("KeyNone reported a change")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times for KeyNone, want 0", *clicks)
	}
}

func TestSetFocusedReportsAChangeOnlyOnTransitions(t *testing.T) {
	b, _, _ := newTestButton()

	if !b.SetFocused(true) {
		t.Fatal("focusing an unfocused button reported no change")
	}
	if !b.Focused() {
		t.Fatal("button did not take the focus")
	}
	if b.SetFocused(true) {
		t.Fatal("focusing an already focused button reported a change")
	}
	if !b.SetFocused(false) {
		t.Fatal("unfocusing a focused button reported no change")
	}
	if b.SetFocused(false) {
		t.Fatal("unfocusing an unfocused button reported a change")
	}
}

// The release will go to whatever is focused next, so it can never complete
// here — the same reasoning as PointerLeave.
func TestLosingTheFocusAbandonsAnArmedSpace(t *testing.T) {
	b, clicks := newFocusedTestButton()

	b.KeyDown(KeySpace)
	if !b.SetFocused(false) {
		t.Fatal("unfocusing an armed button reported no change")
	}
	if b.Pressed() {
		t.Fatal("button stayed pressed after losing the focus")
	}

	b.SetFocused(true)
	b.KeyUp(KeySpace)
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times after the focus moved away, want 0", *clicks)
	}
}

func TestDisabledButtonRefusesTheFocusAndIgnoresKeys(t *testing.T) {
	b, _, clicks := newTestButton()
	b.Disabled = true

	if b.SetFocused(true) {
		t.Fatal("focusing a disabled button reported a change")
	}
	if b.Focused() {
		t.Fatal("a disabled button took the focus")
	}
	if b.KeyDown(KeySpace) || b.KeyUp(KeySpace) || b.KeyDown(KeyEnter) {
		t.Fatal("a disabled button reported a change from a key")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times on a disabled button, want 0", *clicks)
	}
}

// Disabling does not move the focus — the caller owns where it goes — but
// the button must stop looking like it is listening, and must stop asking
// for repaints it can no longer justify.
func TestDisablingAFocusedButtonDropsTheRingButNotTheFocus(t *testing.T) {
	b, clicks := newFocusedTestButton()
	b.KeyDown(KeySpace)

	b.Disabled = true
	if !b.Focused() {
		t.Fatal("disabling took the focus away")
	}
	if b.KeyUp(KeySpace) {
		t.Fatal("releasing Space on a newly disabled button reported a change")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times after the button was disabled mid-key, want 0", *clicks)
	}

	x, y := center(b.Bounds)
	if b.PointerMove(x, y) || b.SetFocused(false) {
		t.Fatal("a disabled button asked for a repaint")
	}
}

// Focus is not a fifth state: it sits alongside hover and pressed rather
// than replacing them.
func TestFocusAndPointerStateAreIndependent(t *testing.T) {
	b, clicks := newFocusedTestButton()
	x, y := center(b.Bounds)

	b.PointerMove(x, y)
	b.PointerDown(x, y)
	if !b.Pressed() || !b.Hovered() || !b.Focused() {
		t.Fatalf("focused=%v hovered=%v pressed=%v, want all true", b.Focused(), b.Hovered(), b.Pressed())
	}
	b.PointerUp(x, y)
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times for a click on a focused button, want 1", *clicks)
	}
	if !b.Focused() {
		t.Fatal("clicking a focused button dropped its focus")
	}
}

func TestButtonWithoutAnOnClickHandlerDoesNotPanicOnKeys(t *testing.T) {
	b, _, _ := newTestButton()
	b.OnClick = nil
	b.SetFocused(true)

	b.KeyDown(KeyEnter)
	b.KeyDown(KeySpace)
	b.KeyUp(KeySpace)
}

// changedPixels returns the positions where drawing a focused button
// differs from drawing the same button unfocused.
func changedPixels(t *testing.T, style func(*Button)) (diff []int, w int, bounds canvas.Rect) {
	t.Helper()

	const width, height = 320, 160
	draw := func(focused bool) []uint32 {
		cv, px := newTestCanvas(t, width, height, 0)
		b, _, _ := newTestButton()
		if style != nil {
			style(b)
		}
		b.SetFocused(focused)
		b.Draw(cv)
		if err := cv.Err(); err != nil {
			t.Fatalf("canvas error: %v", err)
		}
		return px
	}

	off, on := draw(false), draw(true)
	for i := range off {
		if off[i] != on[i] {
			diff = append(diff, i)
		}
	}
	return diff, width, testBounds
}

func TestFocusChangesWhatTheButtonRenders(t *testing.T) {
	diff, _, _ := changedPixels(t, nil)
	if len(diff) == 0 {
		t.Fatal("focusing the button changed nothing on screen")
	}
}

// The ring goes inside Bounds. With no layout engine, anything drawn
// outside would land on whatever the caller placed next to the button, and
// the caller has no way to know it had to leave room.
func TestTheFocusRingStaysInsideBounds(t *testing.T) {
	diff, w, r := changedPixels(t, nil)
	if len(diff) == 0 {
		t.Fatal("focusing the button changed nothing on screen")
	}

	for _, i := range diff {
		x, y := float32(i%w), float32(i/w)
		if !contains(r, x, y) {
			t.Fatalf("focus changed pixel (%v, %v), outside bounds %+v", x, y, r)
		}
	}
}

func TestZeroFocusRingWidthDrawsNoRing(t *testing.T) {
	diff, _, _ := changedPixels(t, func(b *Button) { b.Style.FocusRingWidth = 0 })
	if len(diff) != 0 {
		t.Fatalf("a zero-width focus ring changed %d pixels, want 0", len(diff))
	}
}

// A style with no ring makes focus invisible, and the bool these methods
// return promises exactly "a repaint would look different". Reporting a
// change nobody could see would be a lie — and a costly one, since the
// caller repaints on it. The button still takes the focus; only the claim
// about the repaint goes away.
func TestFocusReportsNoChangeWhenTheRingIsInvisible(t *testing.T) {
	cases := []struct {
		name  string
		style func(*ButtonStyle)
	}{
		{"zero width", func(st *ButtonStyle) { st.FocusRingWidth = 0 }},
		{"transparent ring", func(st *ButtonStyle) { st.FocusRing.A = 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newTestButton()
			tc.style(&b.Style)

			if b.SetFocused(true) {
				t.Error("focusing reported a repaint that would render identically")
			}
			if !b.Focused() {
				t.Fatal("the button did not take the focus")
			}
			if b.SetFocused(false) {
				t.Error("unfocusing reported a repaint that would render identically")
			}
		})
	}
}

// A disabled control must not look like it is listening to the keyboard,
// even when the caller left the focus on it.
func TestADisabledButtonDrawsNoFocusRing(t *testing.T) {
	diff, _, _ := changedPixels(t, func(b *Button) { b.Disabled = true })
	if len(diff) != 0 {
		t.Fatalf("a disabled button's focus ring changed %d pixels, want 0", len(diff))
	}
}

// A button too small to hold its own border must collapse the ring rather
// than hand canvas a negative rectangle.
func TestAButtonTooSmallForItsBorderStillDraws(t *testing.T) {
	cv, _ := newTestCanvas(t, 320, 160, 0)
	b, _, _ := newTestButton()
	b.Bounds = canvas.Rect{X: 10, Y: 10, Width: 2, Height: 2}
	b.SetFocused(true)

	b.Draw(cv)

	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error drawing a button smaller than its border: %v", err)
	}
}
