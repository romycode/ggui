package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/wayland/wlcore"
)

// runUI starts the window's eventloop.UI on its own goroutine, which becomes
// the UI goroutine, and stops it when the test ends. A window under test has
// no connection, so nothing here touches Wayland.
func runUI(t *testing.T, w *window) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.ev.Run(eventloop.Handler{OnEvent: w.onEvent, Paint: w.paint})
	}()
	t.Cleanup(func() {
		w.ev.Push(eventloop.Event{Kind: eventloop.EvClosed})
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("the UI did not stop")
		}
	})
}

// onUI runs fn on the window's UI goroutine and waits for it, which is how a
// test touches state the window owns without racing it.
func onUI(t *testing.T, w *window, fn func()) {
	t.Helper()
	done := make(chan struct{})
	w.ev.Do(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI goroutine did not run the closure")
	}
}

func returnKey() keyboard.Event {
	return keyboard.Event{State: keyboard.Pressed, Sym: symReturn}
}

// Enter starts a task that takes a while, and the UI must stay free while it
// does. The task runs on a goroutine of its own and publishes its result
// through Do, which is the pattern an application's own slow work follows.
func TestSubmitRunsInTheBackgroundAndPublishesThroughDo(t *testing.T) {
	old := submitDelay
	submitDelay = 30 * time.Millisecond
	t.Cleanup(func() { submitDelay = old })

	w := newWindow(nil, bitmapFont{})
	runUI(t, w)

	var during, after string
	var busyDuring bool
	onUI(t, w, func() {
		w.ui.text, w.ui.focused = []rune("hi"), true
		w.typeKey(returnKey())
		busyDuring, during = w.ui.busy, w.ui.status
	})
	if !busyDuring {
		t.Error("the window is not busy after Enter")
	}
	if during == "" {
		t.Error("no status while the task runs")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var busy bool
		onUI(t, w, func() { busy, after = w.ui.busy, w.ui.status })
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the task never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if after != `submitted "hi"` {
		t.Errorf("status %q after the task, want %q", after, `submitted "hi"`)
	}
}

// A second Enter while the first is in flight must not start a second task
// whose result would overwrite the first's.
func TestSubmitIsIgnoredWhileATaskIsRunning(t *testing.T) {
	old := submitDelay
	submitDelay = 40 * time.Millisecond
	t.Cleanup(func() { submitDelay = old })

	w := newWindow(nil, bitmapFont{})
	runUI(t, w)

	var statusAfterSecond string
	onUI(t, w, func() {
		w.ui.text, w.ui.focused = []rune("first"), true
		w.typeKey(returnKey())
		w.ui.text = []rune("second")
		w.typeKey(returnKey())
		statusAfterSecond = w.ui.status
	})
	// Deterministic: a second task would have started, and said so, at once.
	if want := `submitting "first"…`; statusAfterSecond != want {
		t.Errorf("status %q after a second Enter, want %q: a second task started", statusAfterSecond, want)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var busy bool
		onUI(t, w, func() { busy = w.ui.busy })
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the task never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Long enough for a second task, had one been started, to have finished
	// and overwritten the first's result.
	time.Sleep(3 * submitDelay)
	var status string
	onUI(t, w, func() { status = w.ui.status })
	if status != `submitted "first"` {
		t.Errorf("status %q, want the first task's result, still", status)
	}
}

// When the window closes, a task still running must neither block the
// shutdown nor deliver into a UI that is gone.
func TestSubmitDoesNotOutliveTheWindow(t *testing.T) {
	old := submitDelay
	submitDelay = 20 * time.Millisecond
	t.Cleanup(func() { submitDelay = old })

	w := newWindow(nil, bitmapFont{})
	var delivered atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.ev.Run(eventloop.Handler{OnEvent: w.onEvent})
	}()

	onUI(t, w, func() {
		w.ui.text, w.ui.focused = []rune("hi"), true
		w.typeKey(returnKey())
	})
	w.ev.Push(eventloop.Event{Kind: eventloop.EvClosed})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI did not stop with a task in flight")
	}

	time.Sleep(2 * submitDelay) // the task's result would land about now
	w.ev.Do(func() { delivered.Store(true) })
	time.Sleep(20 * time.Millisecond)
	if delivered.Load() {
		t.Error("a closure was delivered to a UI that had stopped")
	}
}

// The blink runs off a timer, so its tick has to act only on a focused
// input: an unfocused window has no caret to toggle and no reason to repaint.
func TestBlinkTickTogglesTheCaretOnlyWhileFocused(t *testing.T) {
	w := newWindow(nil, bitmapFont{})

	w.blinkTick()
	if !w.ui.caretOn {
		t.Error("the caret toggled on an unfocused input")
	}

	w.ui.focused = true
	w.blinkTick()
	if w.ui.caretOn {
		t.Error("the caret did not turn off on the first tick")
	}
	w.blinkTick()
	if !w.ui.caretOn {
		t.Error("the caret did not turn back on on the second tick")
	}
}

// Losing focus and gaining it again should not leave the caret stuck hidden.
func TestFocusingTheInputShowsTheCaret(t *testing.T) {
	w := newWindow(nil, bitmapFont{})
	w.ui.caretOn = false
	l := w.layout()
	x, y := center(l.input)

	w.ui.pointerPressed(l, x, y)
	if !w.ui.caretOn {
		t.Error("clicking into the input left the caret hidden")
	}
}

// A zero size in a configure means "you decide": the window keeps what it has
// rather than shrinking to nothing.
func TestApplyConfigureKeepsTheSizeOnZero(t *testing.T) {
	w := newWindow(nil, bitmapFont{})

	if w.applyConfigure(0, 0) {
		t.Error("a zero configure reported a change")
	}
	if w.width != defaultWidth || w.height != defaultHeight {
		t.Errorf("size became %dx%d, want the default", w.width, w.height)
	}
	if !w.applyConfigure(800, 600) {
		t.Error("a real configure did not report a change")
	}
	if w.width != 800 || w.height != 600 {
		t.Errorf("size is %dx%d, want 800x600", w.width, w.height)
	}
	if w.applyConfigure(800, 600) {
		t.Error("the same size reported a change")
	}
}

// Buffers come from the Wayland goroutine, so a frame whose wl_buffer has not
// been created yet must not be handed out, and neither may a busy one.
func TestFreeFrameSkipsFramesWithoutABufferAndBusyOnes(t *testing.T) {
	w := newWindow(nil, bitmapFont{})
	cv, _ := newTestCanvas(t)

	if w.freeFrame() != nil {
		t.Error("an empty pool has a free frame")
	}

	pending := &frame{cv: cv}
	w.frames[0] = pending
	if w.freeFrame() != nil {
		t.Error("a frame with no wl_buffer yet was handed out")
	}

	b1, b2 := &wlcore.Buffer{}, &wlcore.Buffer{}
	busy := &frame{buf: b1, cv: cv, busy: true}
	free := &frame{buf: b2, cv: cv}
	w.frames[0], w.frames[1] = busy, free
	if got := w.freeFrame(); got != free {
		t.Errorf("freeFrame returned %v, want the idle frame", got)
	}

	w.onEvent(eventloop.Event{Kind: eventloop.EvBufferRelease, Buffer: b1})
	if busy.busy {
		t.Error("EvBufferRelease did not free the frame it named")
	}
}

// paint is the whole per-frame decision: which screen, into which buffer, and
// what to tell the clock. The loader, the real UI and the failure screen must
// each draw something different, and each must hand the compositor a frame.
func TestPaintDrawsTheScreenForThePhase(t *testing.T) {
	newPaintable := func(t *testing.T) (*window, *frame, *int) {
		w := newWindow(nil, bitmapFont{})
		cv, _ := newTestCanvas(t)
		f := &frame{buf: &wlcore.Buffer{}, cv: cv}
		w.frames[0] = f
		posted := new(int)
		w.post = func(func()) { *posted++ }
		return w, f, posted
	}

	screens := map[string][]uint32{}
	for _, phase := range []string{"loading", "ready", "failed"} {
		w, f, posted := newPaintable(t)
		switch phase {
		case "ready":
			w.ev.SetReady()
			w.ui.text = []rune("hello")
		case "failed":
			w.ev.Fail(nil)
		}

		presented, _ := w.paint(1000)
		if !presented {
			t.Fatalf("%s: paint did not present", phase)
		}
		if !f.busy {
			t.Errorf("%s: the frame handed to the compositor is not marked busy", phase)
		}
		if *posted != 1 {
			t.Errorf("%s: %d presents posted, want 1", phase, *posted)
		}
		screens[phase] = snapshot(f.cv.Pixels())
	}

	if equalPixels(screens["loading"], screens["ready"]) {
		t.Error("the loader and the real UI look the same")
	}
	if equalPixels(screens["loading"], screens["failed"]) {
		t.Error("the loader and the failure screen look the same")
	}
	if equalPixels(screens["ready"], screens["failed"]) {
		t.Error("the real UI and the failure screen look the same")
	}
}

// With no free buffer there is nothing to paint into. paint must say so and
// touch nothing, so the UI waits for a release instead of spinning.
func TestPaintWithNoFreeBufferPresentsNothing(t *testing.T) {
	w := newWindow(nil, bitmapFont{})
	posted := 0
	w.post = func(func()) { posted++ }

	if presented, _ := w.paint(0); presented {
		t.Error("paint reported a frame with no buffer to paint into")
	}
	if posted != 0 {
		t.Errorf("%d presents posted with no buffer", posted)
	}
}

// A busy window keeps asking for frames so its spinner moves; an idle one
// does not, so it can go quiet.
func TestPaintAsksForFramesOnlyWhileBusy(t *testing.T) {
	w := newWindow(nil, bitmapFont{})
	cv, _ := newTestCanvas(t)
	w.frames[0] = &frame{buf: &wlcore.Buffer{}, cv: cv}
	w.post = func(func()) {}
	w.ev.SetReady()

	if _, animating := w.paint(0); animating {
		t.Error("an idle window asked for frames")
	}

	w.frames[0].busy = false
	w.ui.busy = true
	if _, animating := w.paint(16); !animating {
		t.Error("a busy window did not ask for frames")
	}
}

// The pool is built across two goroutines: the UI maps the memory and owns
// the canvas, and asks the Wayland goroutine, by Post, for the wl_buffer over
// it. A pool at the right size must not be rebuilt, and a resize must replace
// it and tell the Wayland goroutine to destroy the buffers it made.
func TestEnsureFramesBuildsThePoolOnceAndReplacesItOnResize(t *testing.T) {
	w := newWindow(nil, bitmapFont{})
	posts := 0
	w.post = func(func()) { posts++ }
	t.Cleanup(func() { w.releaseFrames(false) })

	w.width, w.height = 100, 80
	w.ensureFrames()
	if posts != frameCount {
		t.Fatalf("%d buffer creations posted, want one per frame (%d)", posts, frameCount)
	}
	first := w.frames
	for i, f := range first {
		if f == nil || f.buf != nil {
			t.Fatalf("frame %d: want a mapped frame with no wl_buffer yet, got %+v", i, f)
		}
		if f.cv.PixelWidth() != 100 || f.cv.PixelHeight() != 80 {
			t.Errorf("frame %d is %dx%d, want 100x80", i, f.cv.PixelWidth(), f.cv.PixelHeight())
		}
	}

	w.ensureFrames() // same size
	if posts != frameCount || w.frames != first {
		t.Error("an unchanged size rebuilt the pool")
	}

	// The Wayland goroutine has reported one buffer back by the time of the
	// resize, so that one has to be destroyed, and the other is still in
	// flight and is marked dead so it is destroyed on arrival.
	first[0].buf = &wlcore.Buffer{}
	w.width, w.height = 120, 90
	w.ensureFrames()

	if !first[0].dead || !first[1].dead {
		t.Error("the replaced frames were not marked dead")
	}
	// 1 destroy for the frame that had a buffer + frameCount new creations.
	if want := frameCount + 1 + frameCount; posts != want {
		t.Errorf("%d posts in total, want %d", posts, want)
	}
	for i, f := range w.frames {
		if f == first[i] || f.cv.PixelWidth() != 120 || f.cv.PixelHeight() != 90 {
			t.Errorf("frame %d was not replaced by a 120x90 one", i)
		}
	}
}
