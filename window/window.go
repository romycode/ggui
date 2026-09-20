package window

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

// The logical size of a window whose Config leaves it at zero.
const (
	defaultWidth  = 640
	defaultHeight = 480
)

// Config describes the window [Run] opens. The zero value is a 640x480
// window with no title and no application id.
type Config struct {
	// Title is what the compositor shows in the window's title bar and in
	// its list of windows. Empty leaves it unset.
	Title string
	// AppID identifies the application to the desktop environment, which is
	// what matches a window with its .desktop file. It is a reverse-DNS
	// name, such as "ggui.example.widgets". Empty leaves it unset.
	AppID string
	// Width and Height are the initial logical size. Zero means 640x480.
	// The compositor may configure another size straight away.
	Width, Height int
}

// size returns the logical size a config asks for, with the defaults applied.
func (c Config) size() (width, height int32) {
	width, height = int32(c.Width), int32(c.Height)
	if width <= 0 {
		width = defaultWidth
	}
	if height <= 0 {
		height = defaultHeight
	}
	return width, height
}

// Content is what the application hands over when its UI is ready, and is
// everything the window layer knows about it. Every callback runs on the UI
// goroutine, one at a time, so they may touch the application's state without
// a lock and must not block: the window stops answering the compositor while
// one runs.
//
// The nil callbacks are ignored, as with any listener in this repository.
// Only Paint is required.
type Content struct {
	// Paint draws one whole frame into cv, in logical units: cv.Width() and
	// cv.Height() are the window's logical size. now is the compositor's
	// millisecond clock as of the last frame callback, and is what an
	// animation must be driven by.
	//
	// It returns whether the application wants a frame on every callback
	// from here on. An animation turns it on while it runs and off when it
	// settles; a UI that only reacts to input returns false and calls
	// [Window.Invalidate] when something changes.
	Paint func(cv *canvas.Canvas, now uint32) (animating bool)

	// OnKey receives key presses, releases and repeats, with the keysym and
	// the text already resolved.
	OnKey func(keyboard.Event)
	// OnPointer receives pointer motion, buttons, clicks and drags, in
	// logical units.
	OnPointer func(pointer.Event)
	// OnKeyboardFocus reports whether the window has keyboard focus.
	OnKeyboardFocus func(focused bool)
	// OnPointerFocus reports whether the pointer is over the window. The
	// position is not reported with it: it arrives with the first motion.
	OnPointerFocus func(focused bool)
	// OnResize is called before the first Paint at each logical size,
	// including the first one.
	OnResize func(width, height int)
}

// Window is one open window. It is created by [Run] and handed to the init
// function; an application never builds one.
//
// Its methods say which goroutine they belong to. [Window.Do] and
// [Window.Context] are safe from any goroutine, and are how work done
// elsewhere reaches the UI; the rest are for the UI goroutine, which is where
// every [Content] callback runs.
type Window struct {
	// --- Wayland goroutine ---

	conn     *wlcore.Conn
	loop     *eventloop.Loop
	shm      *wlcore.Shm
	surface  *wlcore.Surface
	toplevel *xdgshell.Toplevel
	kbd      *keyboard.Keyboard
	ptr      *pointer.Pointer
	// pendingW and pendingH are the size xdg_toplevel.configure announced.
	// It only takes effect with the xdg_surface.configure that ends the
	// sequence, so the two events are kept apart.
	pendingW, pendingH int32

	// --- UI goroutine ---

	ui   *eventloop.UI
	pool *pool
	// width and height are the current logical size.
	width, height int32
	// content is what init returned. It is empty until the UI is ready, and
	// no callback of it is called before that.
	content Content

	// --- any goroutine ---

	// post queues a closure for the Wayland goroutine. It is Loop.Post once
	// the loop exists and does nothing before that.
	post func(func())
	// workers counts the goroutines this package starts: the UI and the one
	// init runs on. Run waits for the UI, never for init, which may be parked
	// in application code; the group is what makes "nothing leaked" checkable.
	workers sync.WaitGroup

	// mu guards initErr alone. init runs on its own goroutine and Run may
	// read what it left at any moment after the loop ends.
	mu      sync.Mutex
	initErr error
}

// newWindow returns a window on conn at cfg's logical size. Nothing is opened
// and no goroutine started until run.
func newWindow(conn *wlcore.Conn, cfg Config) *Window {
	w := &Window{conn: conn, ui: eventloop.NewUI(), post: func(func()) {}}
	w.width, w.height = cfg.size()
	return w
}

// Do queues fn to run on the UI goroutine, after everything already queued.
// It is how a background task publishes its result: the task does its slow
// work on its own goroutine and calls Do with a closure that stores the
// outcome and calls [Window.Invalidate].
//
// It is safe from any goroutine and never blocks. A closure queued after the
// window has closed is dropped, so a task that is a moment slower to notice
// neither blocks nor piles up work nobody will run; one that wants to stop
// earlier watches [Window.Context].
func (w *Window) Do(fn func()) { w.ui.Do(fn) }

// Context is cancelled when the window closes, and is what a background task
// watches to stop with it. It exists before the window is on screen, so init
// may hand it to whatever it starts.
func (w *Window) Context() context.Context { return w.ui.Context() }

// Invalidate asks for a repaint. The next frame the compositor allows draws
// one, which is what an application calls when its state changed and it is
// not animating. UI goroutine only.
func (w *Window) Invalidate() { w.ui.Invalidate() }

// Size returns the window's logical size, the same units [Content.Paint]
// draws in. UI goroutine only.
func (w *Window) Size() (width, height int) { return int(w.width), int(w.height) }

// Run opens a window described by cfg and blocks until it closes. It returns
// nil for an orderly close, whether the compositor asked for it or the
// application did, and the failure otherwise.
//
// init runs on a goroutine of its own while the window is already on screen
// showing a loader, and returns the [Content] that is installed when it is
// done. Everything slow an application needs before it can show itself — a
// font, a configuration file, a first request — belongs there and not before
// Run.
func Run(cfg Config, init func(w *Window) (Content, error)) error {
	conn, err := wlcore.Connect()
	if err != nil {
		return fmt.Errorf("window: connect: %w", err)
	}
	return run(conn, cfg, init)
}

// run is Run over a connection somebody else made, which is what lets a test
// drive it against a fake compositor. It takes ownership of conn and closes
// it on the way out.
//
// The goroutine that calls it becomes the Wayland goroutine: setup makes its
// requests here, before the loop starts, and from then on everything that
// touches the connection is either dispatched by the loop or posted to it.
func run(conn *wlcore.Conn, cfg Config, init func(w *Window) (Content, error)) error {
	if init == nil {
		conn.Close()
		return errors.New("window: Run needs an init function")
	}

	w := newWindow(conn, cfg)
	defer conn.Close()
	defer w.closeInput()

	if err := w.setup(cfg); err != nil {
		return err
	}

	loop, err := eventloop.New(conn)
	if err != nil {
		return fmt.Errorf("window: event loop: %w", err)
	}
	w.loop, w.post = loop, loop.Post
	if w.kbd != nil {
		// A held key repeats at a moment the compositor never announces, so
		// the loop wakes for the keyboard's own timer as well as the socket.
		loop.Deadline = w.kbd.NextRepeat
		loop.OnTick = func(now time.Time) { w.kbd.Tick(now) }
	}
	w.pool = newPool(w.shm, loop.Post, w.ui.Do, w.ui.Push)
	// A buffer arriving frees the frame clock and nothing more. Whatever was
	// waiting for one still paints: the clock keeps the wish to paint while
	// it is starved, and a configure, a release and SetReady each invalidate
	// on their own. Asking for a repaint here as well would cost a frame
	// nobody asked for every time the second buffer of a pool is adopted a
	// pass later than the first.
	w.pool.adopted = func() { w.ui.SetBufferFree(true) }

	// From here the window is open and the connection is being served: the UI
	// paints the loader from the first configure, and the application's own
	// startup runs beside it.
	uiDone := make(chan struct{})
	w.start(func() {
		defer close(uiDone)
		w.ui.Run(eventloop.Handler{OnEvent: w.onEvent, Paint: w.paint})
	})
	w.start(func() { w.runInit(init) })

	runErr := loop.Run()
	// The loop has stopped, so nothing will reach the UI again; EvClosed is
	// what tells it so, and it is always the last event it sees. init is not
	// waited for: it may be parked in application code, and what it returns
	// after this is dropped.
	w.ui.Push(eventloop.Event{Kind: eventloop.EvClosed})
	<-uiDone

	if runErr != nil && !errors.Is(runErr, wlcore.ErrClosed) {
		return fmt.Errorf("window: %w", runErr)
	}
	return nil
}

// start runs fn on a goroutine of this package's own.
func (w *Window) start(fn func()) {
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		fn()
	}()
}

// runInit is the application's startup, on a goroutine of its own. What it
// returns is installed on the UI goroutine, which is the only place the
// content may be touched, and a failure puts the window in its failed phase
// instead. Either way the window is already open.
func (w *Window) runInit(init func(*Window) (Content, error)) {
	content, err := init(w)
	if err != nil {
		w.setInitErr(err)
		w.ui.Do(func() { w.ui.Fail(err) })
		return
	}
	w.ui.Do(func() { w.install(content) })
}

// install adopts what init returned and ends the loading phase, on the UI
// goroutine. A content that cannot be painted is a failure and not a window
// stuck on a spinner.
func (w *Window) install(content Content) {
	if content.Paint == nil {
		err := errors.New("window: the init function returned a Content without Paint")
		w.setInitErr(err)
		w.ui.Fail(err)
		return
	}
	w.content = content
	w.ui.SetReady()
}

// setInitErr records why the application could not start. It is written on
// the init goroutine and read once the loop and the UI have both ended.
func (w *Window) setInitErr(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.initErr == nil {
		w.initErr = err
	}
}

// initError returns what init failed with, or nil.
func (w *Window) initError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.initErr
}

// onEvent handles what the Wayland goroutine sent, on the UI goroutine.
func (w *Window) onEvent(ev eventloop.Event) {
	switch ev.Kind {
	case eventloop.EvConfigure:
		w.configure(ev.Width, ev.Height)
	case eventloop.EvBufferRelease:
		w.pool.released(ev.Buffer)
		w.ui.SetBufferFree(w.pool.free() != nil)
	case eventloop.EvClosed:
		// The connection is gone, so there is nobody to destroy buffers
		// with; the mappings still have to go.
		w.pool.close(false)
	}
}

// configure adopts the size the compositor asked for and makes the pool match
// it. A zero dimension means "you decide": keep the one the window has.
func (w *Window) configure(width, height int32) {
	if width > 0 {
		w.width = width
	}
	if height > 0 {
		w.height = height
	}
	if err := w.pool.ensure(w.width, w.height); err != nil {
		log.Printf("window: buffers: %v", err)
	}
}

// paint draws one frame into a free buffer and hands it to the compositor. It
// decides nothing about timing — the UI calls it when the frame clock allows
// — and nothing about what is drawn either: the phase picks the screen, and
// only a ready UI is the application's.
func (w *Window) paint(now uint32) (presented, animating bool) {
	f := w.pool.free()
	if f == nil {
		// Every buffer is with the compositor, or the pool is still being
		// built. Say so, and the UI waits for a release instead of spinning.
		w.ui.SetBufferFree(false)
		return false, false
	}

	width, height := float32(w.width), float32(w.height)
	switch w.ui.Phase() {
	case eventloop.PhaseLoading:
		eventloop.PaintLoader(f.cv, width, height, now)
	case eventloop.PhaseFailed:
		eventloop.PaintFailed(f.cv, width, height)
	default:
		animating = w.content.Paint(f.cv, now)
	}
	if err := f.cv.Err(); err != nil {
		// canvas errors are sticky, so this frame is half drawn and every
		// later one into the same canvas would be a no-op. There is nothing
		// sensible left to present.
		log.Printf("window: canvas: %v", err)
		return false, false
	}

	f.busy = true
	w.present(f)
	return true, animating
}

// present hands f to the compositor. It only queues: attach, damage, the
// frame callback and the commit are requests, so they run on the Wayland
// goroutine, and the callback's answer comes back as an event.
func (w *Window) present(f *frame) {
	buf, surface := f.buf, w.surface // both set before the UI goroutine started
	width, height := f.cv.PixelWidth(), f.cv.PixelHeight()
	w.post(func() {
		if err := surface.Attach(buf, 0, 0); err != nil {
			log.Printf("window: attach: %v", err)
			return
		}
		// The whole buffer, because the whole buffer was repainted: what this
		// frame held is two frames old, so there is nothing to preserve and
		// nothing to track.
		if err := surface.DamageBuffer(0, 0, int32(width), int32(height)); err != nil {
			log.Printf("window: damage_buffer: %v", err)
			return
		}
		// Asked for before the commit: the callback is answered when this
		// commit is presented, and that answer is what lets the UI paint
		// again.
		cb, err := surface.Frame()
		if err != nil {
			log.Printf("window: frame: %v", err)
			return
		}
		cb.SetListener(wlcore.CallbackListener{Done: func(t uint32) {
			w.ui.Push(eventloop.Event{Kind: eventloop.EvFrameDone, Time: t})
		}})
		if err := surface.Commit(); err != nil {
			log.Printf("window: commit: %v", err)
		}
	})
}
