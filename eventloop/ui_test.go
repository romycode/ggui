package eventloop

import (
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

// A static UI costs nothing: once it has painted and stopped animating, a
// frame callback alone must not make it paint again.
func TestUIStopsPaintingWhenItStopsAnimating(t *testing.T) {
	ui := NewUI()
	paints := make(chan uint32, 8)
	startUI(t, ui, Handler{Paint: func(now uint32) (bool, bool) {
		paints <- now
		return true, false
	}})

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
