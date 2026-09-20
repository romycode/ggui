package window

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"

	"github.com/romycode/ggui/eventloop"
)

// SetTitle changes the title the compositor shows for the window. It is safe
// from any goroutine, the UI's included, and never blocks: the request is
// queued for the goroutine that owns the connection. Once the window has
// closed it does nothing.
func (w *Window) SetTitle(title string) {
	w.post(func() {
		// A close dispatched earlier in the same pass may already have taken
		// the connection down: there is nobody to tell.
		if w.conn.Err() != nil {
			return
		}
		if err := w.toplevel.SetTitle(title); err != nil {
			log.Printf("window: set_title: %v", err)
		}
	})
}

// Close ends the window, and with it [Run], which returns nil unless
// something failed before. It is safe from any goroutine, the UI's included,
// and from a window that is already closing or closed, so an application may
// call it wherever it decides to quit, even before the event loop has started.
//
// It is the equivalent of the compositor's own close request, and goes through
// Loop.Close, which closes the connection from the calling goroutine and wakes
// the loop. It does not queue a request for the Wayland goroutine, and that is
// the point: a compositor that stops reading leaves that goroutine blocked in a
// write, and only closing the socket from another goroutine can break it. A
// closure queued behind the stuck write would never run, and the application
// could not close a window that has hung.
func (w *Window) Close() {
	if w.loop != nil {
		w.loop.Close()
	}
}

// own runs fn on a goroutine that Run owns and waits for. The goroutine is
// counted in w.owned for as long as it lives, and the returned channel is
// closed once it has stopped and been uncounted: the order matters, because
// what Run sees when it is released and what a test reads afterwards have to
// be the same.
func (w *Window) own(fn func()) <-chan struct{} {
	done := make(chan struct{})
	w.owned.Add(1)
	go func() {
		defer close(done)
		defer w.owned.Add(-1)
		fn()
	}()
	return done
}

// runInit is the application's startup, on a goroutine of its own that Run
// does not own: it may be parked in application code for as long as the
// application likes, and Run does not wait for it. What it returns is
// installed on the UI goroutine, which is the only place the content may be
// touched, and a failure puts the window in its failed phase instead. Either
// way the window is already open. After the window closes, what init returns
// is dropped with everything else given to the UI.
//
// A panic in init, or init ending its goroutine without returning, is a
// failure like any other: the window stays open showing it and Run returns an
// error, instead of the process dying under a window the user is looking at.
func (w *Window) runInit(init func(*Window) (Content, error)) {
	defer close(w.initDone)

	returned := false
	defer func() {
		// recover has to be called by the deferred function itself. It is nil
		// both when init returned and when it left through runtime.Goexit.
		r := recover()
		if returned {
			return
		}
		var err error
		switch v := r.(type) {
		case nil:
			err = errors.New("window: init ended its goroutine without returning")
		case error:
			log.Printf("window: init panicked: %v\n%s", v, debug.Stack())
			err = fmt.Errorf("window: init panicked: %w", v)
		default:
			log.Printf("window: init panicked: %v\n%s", v, debug.Stack())
			err = fmt.Errorf("window: init panicked: %v", v)
		}
		w.initFailed(err)
	}()

	content, err := init(w)
	returned = true
	if err != nil {
		w.initFailed(err)
		return
	}
	w.ui.Do(func() { w.install(content) })
}

// initFailed records why the application could not start, and has the window
// show it. The window stays open: closing it is the user's decision.
func (w *Window) initFailed(err error) {
	w.setFailure(err)
	w.ui.Do(func() { w.ui.Fail(err) })
}

// setFailure records why the window did not run: the first reason wins, since
// what follows a failure is usually a consequence of it. It may be called from
// any goroutine.
func (w *Window) setFailure(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed == nil {
		w.failed = err
	}
}

// failure returns what setFailure recorded, or nil. It is what Run returns in
// preference to how the connection ended.
func (w *Window) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failed
}

// resizeFailed handles a size the pool could not be built for, on the UI
// goroutine. The pool is intact at the size it had, and what to do depends on
// what the window can still do:
//
//   - With no buffer to paint into at all — the very first size failed — there
//     is nothing to show a failure on, and the window would stay open and blank
//     for good, so it closes and Run returns the error.
//   - While the application is loading it is a failed start, like init failing:
//     the failure screen is shown at the old size, the window stays open, and
//     Run returns the error when it closes.
//   - Once the application is running, a size that cannot be honored is
//     refused, logged, and the window carries on at the size it had; the
//     application is not told of a size that never took effect. The compositor
//     copes with a buffer of another size, and an application that is working
//     is not taken down by one absurd configure.
func (w *Window) resizeFailed(err error) {
	log.Print(err)
	switch {
	case !w.pool.usable():
		w.setFailure(err)
		w.Close()
	case w.ui.Phase() == eventloop.PhaseLoading:
		w.setFailure(err)
		w.ui.Fail(err)
	}
}

// bufferFailed handles the Wayland goroutine's report that a buffer could not
// be made, on the UI goroutine. A pool with a frame that will never have a
// buffer cannot be painted into at full depth, and under a compositor that
// holds the last buffer until the next commit even one buffer stalls the
// window for good, so the answer depends on whether anything is on screen
// yet:
//
//   - While the application is loading, or has failed, and another frame
//     remains, the failure screen is shown on it, exactly as if init had
//     failed, and the window stays open.
//   - Otherwise — the application is running, or no frame is left to show
//     anything on — the window closes itself and Run returns the error. A
//     window that stays open and stops updating is the one outcome not
//     acceptable.
func (w *Window) bufferFailed(err error) {
	// The pool has logged what went wrong on the Wayland goroutine, where it
	// did; this is what Run will say.
	err = fmt.Errorf("window: buffers: %w", err)
	if w.ui.Phase() != eventloop.PhaseReady && w.pool.usable() {
		w.setFailure(err)
		w.ui.Fail(err)
		return
	}
	w.setFailure(err)
	w.Close()
}
