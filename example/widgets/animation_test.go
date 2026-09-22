package main

import (
	"testing"

	"github.com/romycode/ggui/canvas"
)

// paddedCanvas is newTestCanvas at any size, with the row padding filled with
// the sentinel so a test can prove nothing wrote into it.
func paddedCanvas(t *testing.T, width, height int) (*canvas.Canvas, []uint32, int) {
	t.Helper()
	stride := width + testPad
	px := make([]uint32, stride*height)
	for i := range px {
		px[i] = paddingSentinel
	}
	cv, err := canvas.New(canvas.Buffer{Pixels: px, Width: width, Height: height, Stride: stride}, width, height, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	return cv, px, stride
}

func snapshot(px []uint32) []uint32 { return append([]uint32(nil), px...) }

// A new input has its caret showing: the blink turns it off and on from
// there, and a caret that started hidden would look like a broken focus.
func TestNewUIStartsWithAVisibleCaret(t *testing.T) {
	if u := newUI(bitmapFont{}); !u.caretOn {
		t.Fatal("a new ui starts with the caret hidden")
	}
}

// The blink is the caret's whole animation, so the two states have to look
// different, and hiding it must not make a focused input look unfocused.
func TestCaretBlinkChangesWhatTheInputRenders(t *testing.T) {
	cv, px := newTestCanvas(t)
	l := computeLayout(testWidth, testHeight)

	shown := newUI(bitmapFont{})
	shown.focus.Focus(shown.field)
	draw(cv, l, shown)
	withCaret := snapshot(px)

	hidden := newUI(bitmapFont{})
	hidden.focus.Focus(hidden.field)
	hidden.setCaretVisible(false)
	draw(cv, l, hidden)
	withoutCaret := snapshot(px)

	unfocused := newUI(bitmapFont{})
	draw(cv, l, unfocused)
	plain := snapshot(px)

	if equalPixels(withCaret, withoutCaret) {
		t.Fatal("hiding the caret changed nothing on screen")
	}
	if equalPixels(withoutCaret, plain) {
		t.Error("a focused input with the caret hidden looks unfocused: the outline should still show focus")
	}
}

func TestBusyIndicatorRendersAndAnimates(t *testing.T) {
	cv, px := newTestCanvas(t)
	l := computeLayout(testWidth, testHeight)

	idle := newUI(bitmapFont{})
	draw(cv, l, idle)
	quiet := snapshot(px)

	busy := newUI(bitmapFont{})
	busy.busy = true
	draw(cv, l, busy)
	first := snapshot(px)

	busy.now = 240
	draw(cv, l, busy)
	later := snapshot(px)

	if equalPixels(quiet, first) {
		t.Fatal("a busy ui looks the same as an idle one")
	}
	if equalPixels(first, later) {
		t.Error("the busy indicator does not move: it is not animated")
	}
}

func TestStatusLineRenders(t *testing.T) {
	cv, px := newTestCanvas(t)
	l := computeLayout(testWidth, testHeight)

	draw(cv, l, newUI(bitmapFont{}))
	blank := snapshot(px)

	u := newUI(bitmapFont{})
	u.status = "submitted"
	draw(cv, l, u)

	if equalPixels(blank, px) {
		t.Fatal("the status line drew nothing")
	}
}

// Only a task in flight needs frames on every callback. An idle window has
// to be allowed to go quiet, which is the FrameClock's whole saving.
func TestAnimatingIsTrueOnlyWhileBusy(t *testing.T) {
	u := newUI(bitmapFont{})
	if u.animating() {
		t.Error("an idle ui asks for frames")
	}
	u.busy = true
	if !u.animating() {
		t.Error("a busy ui does not ask for frames, so its spinner would freeze")
	}
}

// The status line sits below the controls and the spinner beside it; on a
// small window both run off the canvas. That must clip, not trip the sticky
// error that would blank every later draw in the frame, and not reach the
// row padding the compositor never reads.
func TestStatusAndSpinnerNeverBreakTheFrame(t *testing.T) {
	sizes := [][2]int{{640, 220}, {640, 60}, {640, 10}, {300, 220}, {130, 220}, {40, 220}, {40, 30}, {1, 1}}
	for _, size := range sizes {
		w, h := size[0], size[1]
		cv, px, stride := paddedCanvas(t, w, h)

		u := newUI(bitmapFont{})
		u.field.SetText("hello")
		u.focus.Focus(u.field)
		u.busy = true
		u.status = "a status line that is much longer than any of these windows can hold"
		u.now = 500
		draw(cv, computeLayout(float32(w), float32(h)), u)

		if err := cv.Err(); err != nil {
			t.Errorf("%dx%d: canvas error: %v", w, h, err)
		}
		for y := 0; y < h; y++ {
			for x := w; x < stride; x++ {
				if got := px[y*stride+x]; got != paddingSentinel {
					t.Fatalf("%dx%d: padding written at row %d, column %d: %#08x", w, h, y, x, got)
				}
			}
		}
	}
}
