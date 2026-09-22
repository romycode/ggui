package eventloop

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/romycode/ggui/keyboard"
)

// startUI runs a UI on its own goroutine, which becomes the UI goroutine,
// and stops it when the test ends.
func startUI(t *testing.T, ui *UI, h Handler) <-chan error {
	t.Helper()
	// Closed after the error is sent, so a test that reads the error itself
	// does not leave the cleanup below waiting for a second value.
	done := make(chan error, 1)
	go func() {
		done <- ui.Run(h)
		close(done)
	}()
	t.Cleanup(func() {
		ui.Push(Event{Kind: EvClosed})
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not return after EvClosed")
		}
	})
	return done
}

// ready moves the UI out of the loading phase, which is where every UI
// starts, and returns once it has happened. Do is asynchronous, and a test
// that pushed a key straight after it would race the closure: the key is
// handled before the closure runs and, rightly, dropped. Tests about
// steady-state behavior begin here; the loading phase has its own tests
// below.
func ready(t *testing.T, ui *UI) {
	t.Helper()
	done := make(chan struct{})
	ui.Do(func() {
		ui.SetReady()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI never became ready")
	}
}

func configure(ui *UI) { ui.Push(Event{Kind: EvConfigure, Width: 640, Height: 480}) }

func expectNoPaint(t *testing.T, paints <-chan uint32, why string) {
	t.Helper()
	select {
	case now := <-paints:
		t.Fatalf("painted (now=%d) but %s", now, why)
	case <-time.After(60 * time.Millisecond):
	}
}

func expectPaint(t *testing.T, paints <-chan uint32, why string) uint32 {
	t.Helper()
	select {
	case now := <-paints:
		return now
	case <-time.After(2 * time.Second):
		t.Fatalf("never painted: %s", why)
		return 0
	}
}

// Do is how an async task hands its result to the UI. From however many
// goroutines it is called, the closure must run on the one UI goroutine,
// because that is what owns the widgets.
func TestUIDoRunsEveryClosureOnTheUIGoroutine(t *testing.T) {
	const workers, each = 32, 50
	ui := NewUI()

	var uiG atomic.Int64
	started := make(chan struct{})
	startUI(t, ui, Handler{OnEvent: func(ev Event) {
		if ev.Kind == EvConfigure {
			uiG.Store(goroutineID())
			close(started)
		}
	}})
	configure(ui)
	<-started

	var ran atomic.Int64
	var wrongGoroutine atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				ui.Do(func() {
					if goroutineID() != uiG.Load() {
						wrongGoroutine.Store(true)
					}
					ran.Add(1)
				})
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for ran.Load() < workers*each {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d closures ran", ran.Load(), workers*each)
		}
		time.Sleep(time.Millisecond)
	}
	if wrongGoroutine.Load() {
		t.Error("a Do closure ran off the UI goroutine")
	}
}

// Events reach OnEvent in the order the Wayland goroutine pushed them, with
// their payload.
func TestUIDeliversEventsInOrder(t *testing.T) {
	ui := NewUI()
	got := make(chan Event, 8)
	startUI(t, ui, Handler{OnEvent: func(ev Event) { got <- ev }})
	ready(t, ui)

	ui.Push(Event{Kind: EvConfigure, Width: 640, Height: 480})
	ui.Push(Event{Kind: EvKeyboardFocus})
	ui.Push(Event{Kind: EvKey, Key: keyboard.Event{State: keyboard.Pressed, Text: "a"}})

	want := []EventKind{EvConfigure, EvKeyboardFocus, EvKey}
	for i, kind := range want {
		select {
		case ev := <-got:
			if ev.Kind != kind {
				t.Fatalf("event %d is %v, want %v", i, ev.Kind, kind)
			}
			if kind == EvConfigure && (ev.Width != 640 || ev.Height != 480) {
				t.Errorf("configure arrived as %dx%d, want 640x480", ev.Width, ev.Height)
			}
			if kind == EvKey && ev.Key.Text != "a" {
				t.Errorf("key text %q, want %q", ev.Key.Text, "a")
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d (%v) never arrived", i, kind)
		}
	}
}

// Nothing can be presented before the compositor's first configure, so the
// UI must not paint before it however much has been invalidated.
func TestUIDoesNotPaintBeforeTheFirstConfigure(t *testing.T) {
	ui := NewUI()
	paints := make(chan uint32, 8)
	startUI(t, ui, Handler{Paint: func(now uint32) (bool, bool) {
		paints <- now
		return true, false
	}})

	ui.Do(ui.Invalidate)
	expectNoPaint(t, paints, "the compositor has not configured the surface yet")

	configure(ui)
	expectPaint(t, paints, "the surface was configured and something was invalidated")
}

// The animation loop: one paint, then nothing until the compositor says it
// is ready for the next, and then one more per callback. The clock it runs on
// is the compositor's own timestamp.
func TestUIPaintsOncePerFrameCallbackWhileAnimating(t *testing.T) {
	ui := NewUI()
	paints := make(chan uint32, 8)
	startUI(t, ui, Handler{Paint: func(now uint32) (bool, bool) {
		paints <- now
		return true, true
	}})
	ready(t, ui)

	ui.Do(ui.Invalidate)
	configure(ui)
	expectPaint(t, paints, "the first frame")
	expectNoPaint(t, paints, "the frame callback for the first one has not come back")

	ui.Push(Event{Kind: EvFrameDone, Time: 16})
	if now := expectPaint(t, paints, "the first frame callback"); now != 16 {
		t.Errorf("painted with now=%d, want the callback's 16", now)
	}
	expectNoPaint(t, paints, "no second callback yet")

	ui.Push(Event{Kind: EvFrameDone, Time: 33})
	if now := expectPaint(t, paints, "the second frame callback"); now != 33 {
		t.Errorf("painted with now=%d, want the callback's 33", now)
	}
}

// A scale change needs a new frame just as a resize does, with nothing else
// asking for one: the buffer the pool built at the old scale is stale the
// moment the compositor reports a new one.
func TestUIPaintsAgainAfterAScaleChange(t *testing.T) {
	ui := NewUI()
	paints := make(chan uint32, 8)
	startUI(t, ui, Handler{Paint: func(now uint32) (bool, bool) {
		paints <- now
		return true, false
	}})
	ready(t, ui)

	ui.Do(ui.Invalidate)
	configure(ui)
	expectPaint(t, paints, "the first frame")

	// The frame in flight has to answer before another can start; a scale
	// change alone does not skip that.
	ui.Push(Event{Kind: EvFrameDone, Time: 16})
	expectNoPaint(t, paints, "the frame answered but nothing was invalidated yet")

	ui.Push(Event{Kind: EvScale, Scale: 2})
	expectPaint(t, paints, "the scale change invalidated the clock")
}

// A static UI costs nothing: once it has painted and stopped animating, a
// frame callback alone must not make it paint again.
func TestUIStopsPaintingWhenItStopsAnimating(t *testing.T) {
	ui := NewUI()
	paints := make(chan uint32, 8)
	startUI(t, ui, Handler{Paint: func(now uint32) (bool, bool) {
		paints <- now
		return true, false
	}})
	ready(t, ui)

	ui.Do(ui.Invalidate)
	configure(ui)
	expectPaint(t, paints, "the first frame")

	ui.Push(Event{Kind: EvFrameDone, Time: 16})
	expectNoPaint(t, paints, "nothing was invalidated and nothing is animating")

	ui.Do(ui.Invalidate)
	expectPaint(t, paints, "the UI was invalidated again")
}

// With every buffer held by the compositor, Paint reports it did not
// present. The UI must then wait — not spin calling Paint — and try again
// once a buffer is released.
func TestUIRetriesWhenABufferFrees(t *testing.T) {
	ui := NewUI()
	paints := make(chan uint32, 8)
	var attempts atomic.Int64
	startUI(t, ui, Handler{
		OnEvent: func(ev Event) {
			if ev.Kind == EvBufferRelease {
				ui.SetBufferFree(true)
			}
		},
		Paint: func(now uint32) (bool, bool) {
			paints <- now
			if attempts.Add(1) == 1 {
				ui.SetBufferFree(false) // nothing free: cannot present
				return false, false
			}
			return true, false
		},
	})
	ready(t, ui)

	ui.Do(ui.Invalidate)
	configure(ui)
	expectPaint(t, paints, "the first attempt, which finds no buffer")
	expectNoPaint(t, paints, "no buffer has been released: it must wait, not spin")

	ui.Push(Event{Kind: EvBufferRelease})
	expectPaint(t, paints, "a buffer was released")
	if n := attempts.Load(); n != 2 {
		t.Errorf("Paint was called %d times, want 2", n)
	}
}

// EvClosed ends Run cleanly, cancels the context the async tasks hang off,
// and makes later Do calls harmless.
func TestUIClosedEndsRunAndCancelsTheContext(t *testing.T) {
	ui := NewUI()
	closed := make(chan struct{})
	done := startUI(t, ui, Handler{OnEvent: func(ev Event) {
		if ev.Kind == EvClosed {
			close(closed)
		}
	}})

	select {
	case <-ui.Context().Done():
		t.Fatal("the context was already cancelled")
	default:
	}

	ui.Push(Event{Kind: EvClosed})
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after EvClosed, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after EvClosed")
	}
	select {
	case <-closed:
	default:
		t.Error("OnEvent never saw EvClosed: the handler cannot clean up")
	}
	select {
	case <-ui.Context().Done():
	default:
		t.Error("the context was not cancelled")
	}

	finished := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			ui.Do(func() { t.Error("a closure ran after Run returned") })
			ui.Push(Event{Kind: EvKey})
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Do or Push blocked after Run returned")
	}
}

// What was queued behind EvClosed is not for a UI that is gone.
func TestUIDoesNotDeliverEventsAfterClosed(t *testing.T) {
	ui := NewUI()
	var after atomic.Bool
	seenClosed := false
	done := startUI(t, ui, Handler{OnEvent: func(ev Event) {
		if seenClosed {
			after.Store(true)
		}
		if ev.Kind == EvClosed {
			seenClosed = true
		}
	}})

	ui.Push(Event{Kind: EvClosed})
	ui.Push(Event{Kind: EvKey})
	ui.Push(Event{Kind: EvKey})
	<-done

	if after.Load() {
		t.Error("an event was delivered after EvClosed")
	}
}

func TestUIRunTwiceIsAnError(t *testing.T) {
	ui := NewUI()
	startUI(t, ui, Handler{})
	time.Sleep(20 * time.Millisecond)

	if err := ui.Run(Handler{}); err == nil {
		t.Fatal("a second Run returned nil")
	}
}

// Context is what a task selects on to stop when the window goes away. It
// must be usable before Run starts, since a task can be launched first.
func TestUIContextExistsBeforeRun(t *testing.T) {
	ui := NewUI()
	if ui.Context() == nil {
		t.Fatal("Context is nil before Run")
	}
	select {
	case <-ui.Context().Done():
		t.Fatal("the context is cancelled before anything ended")
	default:
	}
}

// The application's UI is not there when the window opens. Until it says so
// the UI is Loading, and what the user does is not for it: a key pressed
// into a loader has no widget to reach.
func TestUIDropsInputUntilReady(t *testing.T) {
	ui := NewUI()
	got := make(chan Event, 16)
	startUI(t, ui, Handler{OnEvent: func(ev Event) { got <- ev }})

	ui.Push(Event{Kind: EvConfigure, Width: 100, Height: 100})
	ui.Push(Event{Kind: EvKey, Key: keyboard.Event{State: keyboard.Pressed, Text: "lost"}})
	ui.Push(Event{Kind: EvPointer})
	ui.Push(Event{Kind: EvKeyboardFocus})
	ui.Push(Event{Kind: EvPointerFocus})
	ui.Push(Event{Kind: EvBufferRelease})

	// Everything except the input arrives: the application needs the size,
	// the focus and the buffers to be ready to show its UI.
	for _, want := range []EventKind{EvConfigure, EvKeyboardFocus, EvPointerFocus, EvBufferRelease} {
		select {
		case ev := <-got:
			if ev.Kind != want {
				t.Fatalf("got %v, want %v: input reached the handler before Ready", ev.Kind, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%v never arrived", want)
		}
	}

	ready(t, ui)
	ui.Push(Event{Kind: EvKey, Key: keyboard.Event{State: keyboard.Pressed, Text: "kept"}})
	ui.Push(Event{Kind: EvPointer})

	for _, want := range []EventKind{EvKey, EvPointer} {
		select {
		case ev := <-got:
			if ev.Kind != want {
				t.Fatalf("got %v, want %v", ev.Kind, want)
			}
			if ev.Kind == EvKey && ev.Key.Text != "kept" {
				t.Errorf("key %q arrived: input dropped while loading came back later", ev.Key.Text)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%v never arrived after Ready", want)
		}
	}
}

// Closing the window has to work whatever the phase: a program stuck
// loading must still be closable.
func TestUIClosedIsDeliveredWhileLoadingAndFailed(t *testing.T) {
	for _, name := range []string{"loading", "failed"} {
		t.Run(name, func(t *testing.T) {
			ui := NewUI()
			closed := make(chan struct{})
			done := startUI(t, ui, Handler{OnEvent: func(ev Event) {
				if ev.Kind == EvClosed {
					close(closed)
				}
			}})
			if name == "failed" {
				ui.Do(func() { ui.Fail(errors.New("boom")) })
			}
			ui.Push(Event{Kind: EvClosed})

			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("EvClosed never reached the handler")
			}
			<-done
		})
	}
}

func TestUIPhaseTransitions(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		do      func(*UI)
		phase   Phase
		wantErr error
	}{
		{"starts loading", func(*UI) {}, PhaseLoading, nil},
		{"ready", func(u *UI) { u.SetReady() }, PhaseReady, nil},
		{"failed", func(u *UI) { u.Fail(boom) }, PhaseFailed, boom},
		{"a late failure does not undo ready", func(u *UI) { u.SetReady(); u.Fail(boom) }, PhaseReady, nil},
		{"a late ready does not undo failure", func(u *UI) { u.Fail(boom); u.SetReady() }, PhaseFailed, boom},
		{"the first failure is kept", func(u *UI) { u.Fail(boom); u.Fail(errors.New("later")) }, PhaseFailed, boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := NewUI()
			type state struct {
				phase Phase
				err   error
			}
			got := make(chan state, 1)
			startUI(t, ui, Handler{})
			ui.Do(func() {
				tt.do(ui)
				got <- state{ui.Phase(), ui.Err()}
			})
			select {
			case s := <-got:
				if s.phase != tt.phase {
					t.Errorf("phase %v, want %v", s.phase, tt.phase)
				}
				if s.err != tt.wantErr {
					t.Errorf("err %v, want %v", s.err, tt.wantErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the closure never ran")
			}
		})
	}
}

// A failure with no error still has to say that it failed, not look like
// success.
func TestUIFailWithNilStillFails(t *testing.T) {
	ui := NewUI()
	got := make(chan error, 1)
	startUI(t, ui, Handler{})
	ui.Do(func() {
		ui.Fail(nil)
		got <- ui.Err()
	})
	select {
	case err := <-got:
		if err == nil {
			t.Fatal("Fail(nil) left Err nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the closure never ran")
	}
}

// The loader spins on every frame callback with no help from the
// application: Paint says it is not animating, and the UI keeps asking for
// frames anyway because a loader that stops moving looks hung.
func TestUILoadingKeepsPaintingWhilePaintSaysItIsStatic(t *testing.T) {
	ui := NewUI()
	paints := make(chan Phase, 8)
	startUI(t, ui, Handler{Paint: func(uint32) (bool, bool) {
		paints <- ui.Phase()
		return true, false
	}})
	ui.Push(Event{Kind: EvConfigure, Width: 10, Height: 10})

	for i := 0; i < 3; i++ {
		select {
		case p := <-paints:
			if p != PhaseLoading {
				t.Fatalf("paint %d in phase %v, want Loading", i, p)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("paint %d never came: the loader stopped animating", i)
		}
		ui.Push(Event{Kind: EvFrameDone, Time: uint32(16 * (i + 1))})
	}
}

// Once the real UI is ready the forced animation stops, and the very next
// frame is the real UI, not one more loader frame.
func TestUIReadyRepaintsWithTheRealUIAndStopsForcingFrames(t *testing.T) {
	ui := NewUI()
	paints := make(chan Phase, 8)
	startUI(t, ui, Handler{Paint: func(uint32) (bool, bool) {
		paints <- ui.Phase()
		return true, false
	}})
	ui.Push(Event{Kind: EvConfigure, Width: 10, Height: 10})
	if p := <-paints; p != PhaseLoading {
		t.Fatalf("first frame in phase %v, want Loading", p)
	}

	// The application finishes while the loader's frame is still in flight.
	ui.Do(ui.SetReady)
	ui.Push(Event{Kind: EvFrameDone, Time: 16})
	select {
	case p := <-paints:
		if p != PhaseReady {
			t.Fatalf("next frame in phase %v, want Ready: one more loader frame was shown", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetReady did not cause a repaint")
	}

	ui.Push(Event{Kind: EvFrameDone, Time: 32})
	select {
	case p := <-paints:
		t.Fatalf("painted again in phase %v with a static UI", p)
	case <-time.After(60 * time.Millisecond):
	}
}

// A failed start is shown once and then left alone: the failure screen does
// not animate, so an unattended window does not spin forever.
func TestUIFailedPaintsTheFailureAndGoesQuiet(t *testing.T) {
	ui := NewUI()
	paints := make(chan Phase, 8)
	startUI(t, ui, Handler{Paint: func(uint32) (bool, bool) {
		paints <- ui.Phase()
		return true, false
	}})
	ui.Push(Event{Kind: EvConfigure, Width: 10, Height: 10})
	<-paints // the loader

	ui.Do(func() { ui.Fail(errors.New("boom")) })
	ui.Push(Event{Kind: EvFrameDone, Time: 16})
	select {
	case p := <-paints:
		if p != PhaseFailed {
			t.Fatalf("frame after Fail in phase %v, want Failed", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fail did not cause a repaint")
	}

	ui.Push(Event{Kind: EvFrameDone, Time: 32})
	select {
	case p := <-paints:
		t.Fatalf("the failure screen kept animating (phase %v)", p)
	case <-time.After(60 * time.Millisecond):
	}
}

func TestPhaseNames(t *testing.T) {
	for phase, want := range map[Phase]string{
		PhaseLoading: "loading", PhaseReady: "ready", PhaseFailed: "failed", Phase(99): "unknown",
	} {
		if got := phase.String(); got != want {
			t.Errorf("Phase(%d).String() = %q, want %q", phase, got, want)
		}
	}
}
