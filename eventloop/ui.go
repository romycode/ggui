package eventloop

import (
	"context"
	"errors"
	"sync/atomic"
)

// Handler is what the application gives [UI.Run]. Both callbacks run on the
// UI goroutine, so they may touch widgets and canvas freely and must not
// touch the Wayland connection.
type Handler struct {
	// OnEvent receives every event from the Wayland goroutine, in order.
	// EvClosed is delivered too, last, so the handler can clean up. Nil
	// ignores them.
	OnEvent func(Event)

	// Paint paints one frame and hands it to the compositor. now is the
	// compositor's millisecond clock as of the last frame callback, which
	// is what an animation should be driven by.
	//
	// It reports two things. presented says whether a frame was actually
	// sent; when there was no free buffer to paint into it is false, and
	// the UI waits for [UI.SetBufferFree] instead of calling Paint again.
	// animating says whether the UI wants a frame on every callback from
	// here on, and is how an animation keeps itself going and how a static
	// UI lets the window go quiet. Nil never paints.
	Paint func(now uint32) (presented, animating bool)
}

// UI is the UI goroutine's half of the split: it receives what the Wayland
// goroutine pushes, runs what other goroutines hand it with [UI.Do], and
// decides when to paint with a [FrameClock].
//
// The goroutine that calls [UI.Run] becomes the UI goroutine. Everything the
// application does to its widgets and canvas happens there, in OnEvent, in
// Paint or in a closure given to Do; that is what lets none of it need a
// lock. Push and Do are the ways in from other goroutines. Invalidate and
// SetBufferFree are for the UI goroutine only, and are meant to be called
// from inside those same callbacks.
type UI struct {
	inbox *Inbox
	tasks *mailbox

	clock FrameClock
	// now is the compositor timestamp of the last frame callback.
	now uint32
	// configured is set by the first EvConfigure. Nothing can be presented
	// before the compositor has told the surface its size.
	configured bool

	ctx    context.Context
	cancel context.CancelFunc
	// closed is set when Run ends. Push and Do drop what arrives after it,
	// so a Wayland goroutine or a background task that is a moment slower
	// to notice neither blocks nor piles up work nobody will read.
	closed  atomic.Bool
	started atomic.Bool
}

// NewUI returns a UI. Nothing runs until [UI.Run].
func NewUI() *UI {
	ctx, cancel := context.WithCancel(context.Background())
	return &UI{
		inbox:  NewInbox(),
		tasks:  newMailbox(nil),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Push queues an event for the UI. It is what the callbacks of keyboard and
// pointer call, from the Wayland goroutine, and is safe from any goroutine
// and never blocks. Events pushed after [UI.Run] has returned are dropped.
func (u *UI) Push(ev Event) {
	if u.closed.Load() {
		return
	}
	u.inbox.Push(ev)
}

// Do queues fn to run on the UI goroutine, after everything already queued,
// and wakes it. It is how a background task publishes its result: the task
// does its slow work on its own goroutine and calls Do with a closure that
// stores the outcome and calls [UI.Invalidate].
//
// It is safe from any goroutine and never blocks. Closures given to Do after
// [UI.Run] has returned are dropped; a task that wants to stop earlier
// selects on [UI.Context].
func (u *UI) Do(fn func()) {
	if fn == nil || u.closed.Load() {
		return
	}
	u.tasks.put(fn)
}

// Context is cancelled when the UI ends, and is what a background task
// watches to stop when the window goes away. It exists from [NewUI], so a
// task may be started before Run.
func (u *UI) Context() context.Context { return u.ctx }

// Invalidate asks for a repaint. UI goroutine only.
func (u *UI) Invalidate() { u.clock.Invalidate() }

// SetBufferFree tells the UI whether there is a buffer to paint into.
// Paint calls it with false when it finds none; the handler calls it with
// true when a buffer is released. UI goroutine only.
func (u *UI) SetBufferFree(free bool) { u.clock.SetBufferFree(free) }

// Run is the UI goroutine's loop. It blocks until the connection ends —
// EvClosed — and returns nil then. It may be called only once.
//
// One pass does, in order: delivers the events that arrived, runs the
// closures given to Do, and paints if the [FrameClock] allows. Events come
// first because a key press or a frame callback may be exactly what makes
// the next paint worth doing.
func (u *UI) Run(h Handler) error {
	if !u.started.CompareAndSwap(false, true) {
		return errors.New("eventloop: UI.Run called more than once")
	}
	defer func() {
		u.closed.Store(true)
		u.cancel()
		// Nobody will read what is left; drop it so it is not held.
		u.inbox.Drain(nil)
		u.tasks.take(nil)
	}()

	var events []Event
	var tasks []func()
	for {
		select {
		case <-u.inbox.Signal():
		case <-u.tasks.signal():
		}

		events = u.inbox.Drain(events[:0])
		for i, ev := range events {
			switch ev.Kind {
			case EvConfigure:
				u.configured = true
				u.clock.Invalidate() // a new size always needs a new frame
			case EvFrameDone:
				u.now = ev.Time
				u.clock.FrameDone()
			}
			if h.OnEvent != nil {
				h.OnEvent(ev)
			}
			if ev.Kind == EvClosed {
				// Whatever was queued behind it is for a UI that is gone.
				clear(events[i:])
				return nil
			}
		}
		clear(events)

		tasks = u.tasks.take(tasks[:0])
		for _, fn := range tasks {
			fn()
		}
		clear(tasks) // do not keep what the closures captured

		if h.Paint != nil && u.configured && u.clock.ShouldPaint() {
			presented, animating := h.Paint(u.now)
			if presented {
				u.clock.Painted()
			}
			u.clock.SetAnimating(animating)
		}
	}
}
