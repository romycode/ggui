package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
)

// spyHost is the window as the application sees it, on a real eventloop.UI so
// that Do, Context and shutdown behave exactly as they do under the window
// package, with two things added: it counts the repaints the application asks
// for, and it signals every closure the application queued once the UI has run
// it. Neither needs a compositor, and a test can wait on the second instead of
// sleeping.
type spyHost struct {
	*eventloop.UI
	invalidations atomic.Int32
	// published carries one token per closure the application gave to Do, after
	// the UI goroutine ran it. It is buffered so the UI never blocks on it.
	published chan struct{}
}

func newSpyHost() *spyHost {
	return &spyHost{UI: eventloop.NewUI(), published: make(chan struct{}, 64)}
}

func (h *spyHost) Do(fn func()) {
	h.UI.Do(func() {
		fn()
		h.published <- struct{}{}
	})
}

func (h *spyHost) Invalidate() {
	h.invalidations.Add(1)
	h.UI.Invalidate()
}

// newTestApp returns an application on a window that is not running. It is for
// tests that call the application's methods directly, on the test goroutine,
// which is then the UI goroutine.
func newTestApp(t *testing.T) (*app, *spyHost) {
	t.Helper()
	h := newSpyHost()
	return newApp(h, new(tasks), bitmapFont{}), h
}

// runningApp is an application on a window whose UI goroutine is running, and
// stops it, and the application's goroutines with it, when the test ends.
func runningApp(t *testing.T) (*app, *spyHost) {
	t.Helper()
	a, h := newTestApp(t)
	runUI(t, h, a.tasks)
	return a, h
}

// runUI starts h's UI on its own goroutine, which becomes the UI goroutine.
// At the end of the test it closes it, as the window does when it closes, and
// requires every goroutine started through tk to stop with it.
func runUI(t *testing.T, h *spyHost, tk *tasks) (stopped <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.UI.Run(eventloop.Handler{})
	}()
	t.Cleanup(func() {
		closeWindow(t, h, done)
		waitTasks(t, tk)
	})
	return done
}

// closeWindow ends h's UI the way the connection ending does, and waits for it
// to stop. It is safe to call more than once.
func closeWindow(t *testing.T, h *spyHost, done <-chan struct{}) {
	t.Helper()
	h.UI.Push(eventloop.Event{Kind: eventloop.EvClosed})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI did not stop")
	}
}

// waitTasks requires every goroutine started through tk to have returned
// within a bound, which is what a goroutine that ignores the window's context
// does not do.
func waitTasks(t *testing.T, tk *tasks) {
	t.Helper()
	joined := make(chan struct{})
	go func() {
		tk.wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("a goroutine of the application did not stop with the window")
	}
}

// onUI runs fn on the UI goroutine and waits for it, which is how a test
// touches state the application owns without racing it. It bypasses the spy so
// that it is not counted as something the application published.
func onUI(t *testing.T, h *spyHost, fn func()) {
	t.Helper()
	done := make(chan struct{})
	h.UI.Do(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI goroutine did not run the closure")
	}
}

// waitPublished waits for the application to publish one closure through Do,
// and for the UI to run it.
func waitPublished(t *testing.T, h *spyHost) {
	t.Helper()
	select {
	case <-h.published:
	case <-time.After(2 * time.Second):
		t.Fatal("the application published nothing")
	}
}

func returnKey() keyboard.Event {
	return keyboard.Event{State: keyboard.Pressed, Sym: symReturn}
}

func setSubmitDelay(t *testing.T, d time.Duration) {
	t.Helper()
	old := submitDelay
	submitDelay = d
	t.Cleanup(func() { submitDelay = old })
}

// Enter starts a task that takes a while, and the UI must stay free while it
// does. The task runs on a goroutine of its own and publishes its result
// through Do, which is the pattern an application's own slow work follows.
func TestSubmitRunsInTheBackgroundAndPublishesThroughDo(t *testing.T) {
	setSubmitDelay(t, 30*time.Millisecond)
	a, h := runningApp(t)

	var during string
	var busyDuring bool
	onUI(t, h, func() {
		a.ui.text, a.ui.focused = []rune("hi"), true
		a.typeKey(returnKey())
		busyDuring, during = a.ui.busy, a.ui.status
	})
	if !busyDuring {
		t.Error("the app is not busy after Enter")
	}
	if during == "" {
		t.Error("no status while the task runs")
	}

	// The result is published through Do: this returns when the UI has run it.
	waitPublished(t, h)
	var busy bool
	var after string
	onUI(t, h, func() { busy, after = a.ui.busy, a.ui.status })
	if busy {
		t.Error("the app is still busy after the task published its result")
	}
	if after != `submitted "hi"` {
		t.Errorf("status %q after the task, want %q", after, `submitted "hi"`)
	}
}

// Enter is a request to repaint: the busy state and the status it sets are not
// on screen until the application asks.
func TestSubmitAsksForARepaint(t *testing.T) {
	setSubmitDelay(t, time.Hour)
	a, h := runningApp(t)

	before := h.invalidations.Load()
	onUI(t, h, func() {
		a.ui.focused = true
		a.typeKey(returnKey())
	})
	if h.invalidations.Load() == before {
		t.Error("Enter changed the state and did not ask for a repaint")
	}
}

// A second Enter while the first is in flight must not start a second task
// whose result would overwrite the first's.
func TestSubmitIsIgnoredWhileATaskIsRunning(t *testing.T) {
	setSubmitDelay(t, 40*time.Millisecond)
	a, h := runningApp(t)

	var statusAfterSecond string
	onUI(t, h, func() {
		a.ui.text, a.ui.focused = []rune("first"), true
		a.typeKey(returnKey())
		a.ui.text = []rune("second")
		a.typeKey(returnKey())
		statusAfterSecond = a.ui.status
	})
	// Deterministic: a second task would have started, and said so, at once.
	if want := `submitting "first"…`; statusAfterSecond != want {
		t.Errorf("status %q after a second Enter, want %q: a second task started", statusAfterSecond, want)
	}

	// Every goroutine the app started has returned once wait does, and each
	// queued what it had to say before returning, so after this round trip the
	// UI has run everything any task published. A second task would have
	// published by now, and overwritten the first's result.
	waitTasks(t, a.tasks)
	onUI(t, h, func() {})
	if got := len(h.published); got != 1 {
		t.Errorf("%d results published, want the first task's alone", got)
	}
	var status string
	onUI(t, h, func() { status = a.ui.status })
	if status != `submitted "first"` {
		t.Errorf("status %q, want the first task's result, still", status)
	}
}

// When the window closes, a task still running must neither block the
// shutdown nor deliver into a UI that is gone. The delay is an hour, so the
// only way the task can end is by noticing the window closed.
func TestSubmitDoesNotOutliveTheWindow(t *testing.T) {
	setSubmitDelay(t, time.Hour)
	a, h := newTestApp(t)
	done := runUI(t, h, a.tasks)

	onUI(t, h, func() {
		a.ui.text, a.ui.focused = []rune("hi"), true
		a.typeKey(returnKey())
	})
	closeWindow(t, h, done)
	waitTasks(t, a.tasks)

	if got := len(h.published); got != 0 {
		t.Errorf("the task published %d closures for a window that was gone", got)
	}
}

// The application's goroutines are joined by run once the window has closed, so
// that none is still running when the process is told it may exit; and none
// starts after that.
func TestTasksCanBeJoined(t *testing.T) {
	var tk tasks
	started := make(chan struct{})
	release := make(chan struct{})
	tk.start(func() {
		close(started)
		<-release
	})
	<-started

	joined := make(chan struct{})
	go func() {
		tk.wait()
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("wait returned before the goroutine stopped")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return after the goroutine stopped")
	}
}

func TestTasksRefuseWorkOnceWaitHasBegun(t *testing.T) {
	var tk tasks
	tk.wait()

	ran := make(chan struct{}, 2)
	tk.start(func() { ran <- struct{}{} })
	if tk.do(func() { ran <- struct{}{} }) {
		t.Error("do ran after wait")
	}
	// Nothing was started, so this returns at once, and by then nothing ran.
	tk.wait()
	if len(ran) != 0 {
		t.Error("work ran after wait had begun")
	}
}

// Enter, like any key, does nothing to an input that does not have the caret,
// and a release is never an edit. Without the first, Enter typed anywhere in
// the window would start the task.
func TestKeysActOnlyOnAFocusedInputAndOnPresses(t *testing.T) {
	setSubmitDelay(t, time.Hour)
	a, h := newTestApp(t)
	t.Cleanup(func() { h.UI.Push(eventloop.Event{Kind: eventloop.EvClosed}) })

	a.typeKey(returnKey())
	if a.ui.busy {
		t.Error("Enter started a task on an input that was not focused")
	}

	a.ui.focused = true
	a.typeKey(keyboard.Event{State: keyboard.Released, Sym: symReturn})
	if a.ui.busy {
		t.Error("the release of Enter started a task")
	}
	a.typeKey(keyboard.Event{State: keyboard.Pressed, Text: "x"})
	if got := string(a.ui.text); got != "x" {
		t.Errorf("text %q after typing x, want %q", got, "x")
	}
	a.typeKey(keyboard.Event{State: keyboard.Pressed, Sym: symEscape})
	if a.ui.focused {
		t.Error("Escape left the input focused")
	}
}

// The blink runs off a timer, so its tick has to act only on a focused
// input: an unfocused window has no caret to toggle and no reason to repaint.
func TestBlinkTickTogglesTheCaretOnlyWhileFocused(t *testing.T) {
	a, h := newTestApp(t)

	a.blinkTick()
	if !a.ui.caretOn {
		t.Error("the caret toggled on an unfocused input")
	}
	if n := h.invalidations.Load(); n != 0 {
		t.Errorf("an unfocused tick asked for %d repaints, want none", n)
	}

	a.ui.focused = true
	a.blinkTick()
	if a.ui.caretOn {
		t.Error("the caret did not turn off on the first tick")
	}
	a.blinkTick()
	if !a.ui.caretOn {
		t.Error("the caret did not turn back on on the second tick")
	}
	if n := h.invalidations.Load(); n != 2 {
		t.Errorf("two focused ticks asked for %d repaints, want 2", n)
	}
}

// The blink is a goroutine of its own, and closing the window is what ends it.
func TestBlinkStopsWithTheWindow(t *testing.T) {
	a, h := newTestApp(t)
	done := runUI(t, h, a.tasks)
	a.tasks.start(a.blink)

	closeWindow(t, h, done)
	waitTasks(t, a.tasks)
}

// Losing focus and gaining it again should not leave the caret stuck hidden.
func TestFocusingTheInputShowsTheCaret(t *testing.T) {
	a, _ := newTestApp(t)
	a.ui.caretOn = false
	l := a.layout()
	x, y := center(l.input)

	a.ui.pointerPressed(l, x, y)
	if !a.ui.caretOn {
		t.Error("clicking into the input left the caret hidden")
	}
}

// Keyboard focus belongs to the window, and the caret to the input. When the
// window loses it the caret goes too; gaining it does not put the caret back,
// since which control owns it is the application's own business.
func TestLosingTheKeyboardFocusDropsTheCaret(t *testing.T) {
	a, h := newTestApp(t)
	a.ui.focused = true

	a.keyboardFocus(true)
	if !a.ui.focused {
		t.Error("gaining the window's focus took the input's away")
	}
	a.keyboardFocus(false)
	if a.ui.focused {
		t.Error("the input kept the caret after the window lost the keyboard focus")
	}
	if h.invalidations.Load() != 1 {
		t.Errorf("%d repaints, want one for the change", h.invalidations.Load())
	}

	a.keyboardFocus(false) // already unfocused: nothing to repaint
	if h.invalidations.Load() != 1 {
		t.Error("a focus loss that changed nothing asked for a repaint")
	}
}

// The pointer leaving the window sends no motion, so the button's hover has to
// be dropped by the focus callback.
func TestThePointerLeavingTheWindowClearsTheButtonHover(t *testing.T) {
	a, h := newTestApp(t)
	l := a.layout()
	x, y := center(l.button)
	a.pointerEvent(pointer.Event{Kind: pointer.Position, X: x, Y: y})
	if !a.ui.button.Hovered() {
		t.Fatal("the button is not hovered after the pointer moved onto it")
	}
	before := h.invalidations.Load()

	a.pointerFocus(false)
	if a.ui.button.Hovered() {
		t.Error("the button is still hovered after the pointer left the window")
	}
	if h.invalidations.Load() == before {
		t.Error("the hover was dropped and no repaint was asked for")
	}

	a.pointerFocus(true) // entering says nothing: the position arrives with the first motion
}

// OnResize is how the application learns its size, and both the frame and the
// hit-testing follow it: a click where the moved input now is must focus it.
func TestResizeMovesTheLayoutTheClicksAreTestedAgainst(t *testing.T) {
	a, _ := newTestApp(t)

	a.resize(900, 400)
	if want := computeLayout(900, 400); a.layout() != want {
		t.Errorf("layout %+v after a resize to 900x400, want %+v", a.layout(), want)
	}

	// The input's centre at 900x400 is below the whole window at the default
	// size, so this can only focus it if the click is tested at the new one.
	x, y := center(computeLayout(900, 400).input)
	a.pointerEvent(pointer.Event{Kind: pointer.ButtonDown, Button: btnLeft, X: x, Y: y})
	if !a.ui.focused {
		t.Error("a click on the input at the new size did not focus it")
	}
}

// Only the left button acts.
func TestOtherPointerButtonsAreIgnored(t *testing.T) {
	a, h := newTestApp(t)
	x, y := center(a.layout().input)

	a.pointerEvent(pointer.Event{Kind: pointer.ButtonDown, Button: btnLeft + 1, X: x, Y: y})
	if a.ui.focused || h.invalidations.Load() != 0 {
		t.Error("a button other than the left one was acted on")
	}
}

// paint draws the real UI, and only the real UI: the loader and the failure
// screen belong to the window package. What it draws is the frame draw makes
// of the ui, at the layout of the size OnResize gave.
func TestPaintDrawsTheRealUI(t *testing.T) {
	a, _ := newTestApp(t)
	a.ui.text, a.ui.focused = []rune("hello"), true

	got, gotPx := newTestCanvas(t)
	a.paint(got, 0)
	if err := got.Err(); err != nil {
		t.Fatalf("canvas: %v", err)
	}

	want, wantPx := newTestCanvas(t)
	draw(want, computeLayout(testWidth, testHeight), a.ui)
	if !equalPixels(gotPx, wantPx) {
		t.Error("paint did not draw the ui")
	}

	blank, blankPx := newTestCanvas(t)
	blank.Clear(colorBackground)
	if equalPixels(gotPx, blankPx) {
		t.Error("paint drew nothing but the background")
	}
}

// paint draws at the size the window last reported, not at a size of its own:
// the canvas it is given is that size, and the layout has to be too.
func TestPaintFollowsTheSizeFromOnResize(t *testing.T) {
	a, _ := newTestApp(t)
	a.resize(300, 120)

	cv, px, _ := paddedCanvas(t, 300, 120)
	a.paint(cv, 0)
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas: %v", err)
	}

	want, wantPx, _ := paddedCanvas(t, 300, 120)
	draw(want, computeLayout(300, 120), a.ui)
	if !equalPixels(px, wantPx) {
		t.Error("paint did not draw at the size OnResize reported")
	}
}

// A busy app keeps asking for frames so its spinner moves; an idle one does
// not, so it can go quiet.
func TestPaintAsksForFramesOnlyWhileBusy(t *testing.T) {
	a, _ := newTestApp(t)
	cv, _ := newTestCanvas(t)

	if a.paint(cv, 0) {
		t.Error("an idle app asked for frames")
	}
	a.ui.busy = true
	if !a.paint(cv, 16) {
		t.Error("a busy app did not ask for frames")
	}
}

// The spinner is driven by the clock paint is given, so it moves from one
// frame to the next and does not depend on anything else.
func TestPaintDrivesTheSpinnerFromTheFrameClock(t *testing.T) {
	a, _ := newTestApp(t)
	a.ui.busy = true

	cv, px := newTestCanvas(t)
	a.paint(cv, 0)
	first := snapshot(px)
	a.paint(cv, 240)
	if equalPixels(first, px) {
		t.Error("the spinner did not move between two frames of different time")
	}
	if a.ui.now != 240 {
		t.Errorf("ui.now is %d after painting at 240", a.ui.now)
	}
}

// The content hands the window every callback the application listens to.
func TestContentWiresEveryCallback(t *testing.T) {
	a, _ := newTestApp(t)
	c := a.content()
	if c.Paint == nil || c.OnKey == nil || c.OnPointer == nil ||
		c.OnKeyboardFocus == nil || c.OnPointerFocus == nil || c.OnResize == nil {
		t.Errorf("a callback is missing: %+v", c)
	}
}

// initialize is what the window runs beside its loader. It returns a content
// that can paint and starts the caret's blink, which stops with the window.
func TestInitializeReturnsTheContentAndStartsTheBlink(t *testing.T) {
	t.Setenv("WIDGETS_SLOW_INIT", "")
	h := newSpyHost()
	var tk tasks
	done := runUI(t, h, &tk)

	c, err := initialize(h, &tk)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if c.Paint == nil || c.OnKey == nil || c.OnPointer == nil {
		t.Errorf("the content is incomplete: %+v", c)
	}

	// Paint what it returned, into a real canvas.
	cv, _ := newTestCanvas(t)
	c.OnResize(testWidth, testHeight)
	if c.Paint(cv, 0) {
		t.Error("a fresh app asked for frames")
	}
	if err := cv.Err(); err != nil {
		t.Errorf("canvas: %v", err)
	}

	// Whatever goroutines initialize started, the blink among them, have to stop
	// with the window, or wait never returns.
	closeWindow(t, h, done)
	waitTasks(t, &tk)
}

// WIDGETS_SLOW_INIT holds the loader: initialize does not return before it
// has passed.
func TestSlowInitHoldsInitialize(t *testing.T) {
	const hold = 50 * time.Millisecond
	t.Setenv("WIDGETS_SLOW_INIT", hold.String())
	h := newSpyHost()
	var tk tasks
	runUI(t, h, &tk)

	start := time.Now()
	if _, err := initialize(h, &tk); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if elapsed := time.Since(start); elapsed < hold {
		t.Errorf("initialize returned after %v, want at least the %v it was held for", elapsed, hold)
	}
}

// A window closed while the loader is held must not keep the application
// waiting for the hold to pass, and must not be told it failed: an error would
// be what window.Run returns for an orderly close.
func TestInitializeStopsPromptlyWhenTheWindowCloses(t *testing.T) {
	t.Setenv("WIDGETS_SLOW_INIT", "1h")
	h := newSpyHost()
	var tk tasks
	done := runUI(t, h, &tk)

	type result struct {
		paintable bool
		err       error
	}
	returned := make(chan result, 1)
	go func() {
		c, err := initialize(h, &tk)
		returned <- result{paintable: c.Paint != nil, err: err}
	}()

	closeWindow(t, h, done)
	select {
	case r := <-returned:
		if r.err != nil {
			t.Errorf("initialize failed on a window that closed: %v", r.err)
		}
		if !r.paintable {
			t.Error("initialize returned a content that cannot paint: the window would record it as a failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initialize kept holding the loader after the window closed")
	}
	waitTasks(t, &tk)
}
