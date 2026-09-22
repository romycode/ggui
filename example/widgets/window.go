// Command widgets opens a Wayland window holding two controls — a text
// input and a button — and wires them to real pointer and keyboard input.
// Click the input and type; click Clear to empty it; press Enter to submit.
//
// It is the example for the window package, which owns everything about
// Wayland — the connection, the surface, the buffers, the frame clock, the
// shutdown — and leaves the application four things worth reading it for:
//
//  1. Nothing but the application. main is one call to window.Run, and what
//     the application hands over is a window.Content: callbacks that paint
//     the window and receive its keys, pointer, focus and size. They all run
//     on one goroutine and speak in logical units, so the widget state (ui)
//     has no locks and the code never sees a surface, a buffer or a scale.
//
//  2. The window opens first. Everything the application needs before it can
//     show itself, here the system font, loads in the init function, which
//     runs beside a loader the window draws by itself. Set
//     WIDGETS_SLOW_INIT=3s to see it.
//
//  3. Animation and background work. Paint returns whether the window wants
//     another frame at once: it does while a task runs, so the busy indicator
//     moves off the compositor's frame callbacks, and an idle window draws
//     nothing. The caret blinks from a timer that asks the window to repaint
//     twice a second. Enter starts a slow task on its own goroutine, whose
//     result comes back through Window.Do. Every goroutine stops on
//     Window.Context, which closing the window cancels.
//
//  4. Focus is not one thing. The window reports whether the keyboard focus
//     is on it, and which control inside it owns the caret is entirely the
//     application's business: here a widget.Chain that distributes it
//     between the field and the button, and that Tab moves. Press and
//     release are separate events for a reason too: the button fires only
//     when both land inside it, so dragging off a pressed button cancels the
//     click; a click on the field is the three calls docs/widget.md
//     describes.
//
// The fallback text is drawn with basicfont.Face7x13 from golang.org/x/image,
// blitted straight into the pixels the canvas borrowed — canvas fills shapes
// and has no text API. The face is ASCII-only, so accented characters composed
// with dead keys render as the replacement glyph when no system font is found.
// That is a limit of the example's font, not of the keyboard package.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/widget"
	"github.com/romycode/ggui/window"
)

const (
	// defaultWidth and defaultHeight are the logical size the window asks for.
	// The compositor may configure another one, and the application hears of
	// it through OnResize.
	defaultWidth  = 640
	defaultHeight = 220

	// btnLeft is BTN_LEFT from linux/input-event-codes.h. wl_pointer.button
	// carries evdev button codes, which the Wayland protocol does not
	// enumerate.
	btnLeft = 0x110

	// blinkPeriod is how long the caret stays in each state.
	blinkPeriod = 530 * time.Millisecond
)

// submitDelay is how long the simulated submit takes. It is a variable so a
// test can shorten it.
var submitDelay = 1500 * time.Millisecond

// The keysyms this window gives a meaning to. The keyboard package carries
// keysym *names* rather than Go constants, so they are spelled out here.
const (
	symBackSpace = keyboard.Keysym(0xff08)
	symDelete    = keyboard.Keysym(0xffff)
	symLeft      = keyboard.Keysym(0xff51)
	symRight     = keyboard.Keysym(0xff53)
	symHome      = keyboard.Keysym(0xff50)
	symEnd       = keyboard.Keysym(0xff57)
	symReturn    = keyboard.Keysym(0xff0d)
	symKPEnter   = keyboard.Keysym(0xff8d)
	symEscape    = keyboard.Keysym(0xff1b)
	symTab       = keyboard.Keysym(0xff09)
	symLeftTab   = keyboard.Keysym(0xfe20) // ISO_Left_Tab, what Shift-Tab is on some keymaps
	symSpace     = keyboard.Keysym(0x0020)
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run opens the window and returns when it has closed and the application's
// own goroutines have stopped.
func run() error {
	log.Printf("widgets: click the text field and type; Enter submits; Ctrl-C to quit")

	var t tasks
	err := window.Run(window.Config{
		Title:  "ggui widgets — click the field and type",
		AppID:  "ggui.example.widgets",
		Width:  defaultWidth,
		Height: defaultHeight,
	}, func(w *window.Window) (window.Content, error) {
		return initialize(w, &t)
	})
	// Closing the window cancelled its context, which every goroutine started
	// through t watches, so this is prompt. What it adds is that nothing of the
	// application is still running when the process is told it may exit.
	t.wait()
	return err
}

// host is what the application asks of its window: the three methods it calls
// from anywhere but Paint. *window.Window is one, and so is *eventloop.UI,
// which is what lets a test drive the application without a compositor.
type host interface {
	// Do queues fn for the UI goroutine. Safe from any goroutine.
	Do(fn func())
	// Context is cancelled when the window closes. Safe from any goroutine.
	Context() context.Context
	// Invalidate asks for a repaint. UI goroutine only.
	Invalidate()
}

// tasks counts the goroutines the application starts. All of them stop when
// the window's context is cancelled; what tasks adds is that run can wait for
// them, and that none starts once it has begun to.
type tasks struct {
	mu      sync.Mutex
	waiting bool
	wg      sync.WaitGroup
}

// add registers one goroutine and reports whether it may run: after wait has
// begun the window is gone and there is nothing left for it to serve.
func (t *tasks) add() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.waiting {
		return false
	}
	t.wg.Add(1)
	return true
}

// start runs fn on a goroutine of its own, unless wait has begun.
func (t *tasks) start(fn func()) {
	if !t.add() {
		return
	}
	go func() {
		defer t.wg.Done()
		fn()
	}()
}

// do runs fn on the calling goroutine, counted like the others, and reports
// whether it ran. It is for work that is not started here, such as init.
func (t *tasks) do(fn func()) bool {
	if !t.add() {
		return false
	}
	defer t.wg.Done()
	fn()
	return true
}

// wait blocks until every registered goroutine has returned, and refuses any
// registered after it.
func (t *tasks) wait() {
	t.mu.Lock()
	t.waiting = true
	t.mu.Unlock()
	t.wg.Wait()
}

// initialize is the application's slow startup: everything that has to exist
// before the real UI can be shown, and nothing the window needed to open. Here
// that is the system font, whose discovery walks the disk. It runs on a
// goroutine window.Run makes for it, beside the loader, and returns the
// window.Content that takes the loader's place.
//
// It never fails. When the window closes first it stops at once and returns a
// content nobody will see: an error would be recorded as the reason the window
// did not run, which is not what closing it is.
func initialize(h host, t *tasks) (window.Content, error) {
	ctx := h.Context()
	var font widget.Font = bitmapFont{}
	t.do(func() { font = startupFont(ctx) })

	a := newApp(h, t, font)
	t.start(a.blink)
	return a.content(), nil
}

// startupFont is the slow part of initialize: the system font, after however
// long WIDGETS_SLOW_INIT holds it back, to see the loader. It gives up on ctx.
func startupFont(ctx context.Context) widget.Font {
	if d, err := time.ParseDuration(os.Getenv("WIDGETS_SLOW_INIT")); err == nil && d > 0 {
		log.Printf("widgets: WIDGETS_SLOW_INIT=%v, holding the loader", d)
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return bitmapFont{}
		}
	}
	return loadFont(ctx)
}

// app is the application: the widget state and the window it lives in. It is
// only ever touched on the UI goroutine, which is where every Content callback
// runs and where whatever a background goroutine publishes through Do runs.
type app struct {
	host  host
	tasks *tasks
	// ui is the widget state.
	ui *ui
	// width and height are the window's logical size, as OnResize last said.
	width, height int
}

// newApp returns an application drawing with font, in a window of the size
// it asks for until told otherwise.
func newApp(h host, t *tasks, font widget.Font) *app {
	a := &app{host: h, tasks: t, ui: newUI(font), width: defaultWidth, height: defaultHeight}
	a.ui.field.OnSubmit = a.submit
	return a
}

// content is what the window layer is given: the application's side of every
// callback it can make.
func (a *app) content() window.Content {
	return window.Content{
		Paint:           a.paint,
		OnKey:           a.typeKey,
		OnPointer:       a.pointerEvent,
		OnKeyboardFocus: a.keyboardFocus,
		OnPointerFocus:  a.pointerFocus,
		OnResize:        a.resize,
	}
}

// resize records the window's size. The window calls it before the first
// paint at each size, so paint and pointer hit-testing always agree on the
// layout.
func (a *app) resize(width, height int) { a.width, a.height = width, height }

func (a *app) layout() layout {
	return computeLayout(float32(a.width), float32(a.height))
}

// paint draws one frame. The window decides when: this decides nothing about
// timing except whether it wants the next frame at once, which it does while a
// task runs and its spinner moves.
func (a *app) paint(cv *canvas.Canvas, now uint32) (animating bool) {
	a.ui.now = now
	draw(cv, a.layout(), a.ui)
	return a.ui.animating()
}

func (a *app) pointerEvent(ev pointer.Event) {
	l := a.layout()
	switch ev.Kind {
	case pointer.Position:
		if a.ui.pointerMoved(l, ev.X, ev.Y) {
			a.host.Invalidate()
		}
	case pointer.ButtonDown:
		if ev.Button == btnLeft {
			if a.ui.pointerPressed(l, ev.X, ev.Y) {
				a.host.Invalidate()
			}
		}
	case pointer.ButtonUp:
		if ev.Button == btnLeft {
			if a.ui.pointerReleased(l, ev.X, ev.Y) {
				log.Printf("cleared")
			}
			a.host.Invalidate()
		}
	}
}

// pointerFocus follows whether the pointer is over the window. Leaving it has
// to drop the button's hover, since no motion will say so.
func (a *app) pointerFocus(focused bool) {
	if !focused && a.ui.button.PointerLeave() {
		a.host.Invalidate()
	}
}

// keyboardFocus follows the keyboard focus of the whole window, which is a
// different thing from which control inside it owns the caret. Losing it
// has to drop the caret too: the user is typing somewhere else now.
func (a *app) keyboardFocus(focused bool) {
	if !focused && a.ui.blur() {
		a.host.Invalidate()
	}
}

// typeKey turns one key event into an edit, a focus move or an activation.
// Everything below the keysym — the keycode arithmetic, the keymap, the
// dead keys, the modifiers that must not reach the composer — is the
// keyboard package's; what is left here is this window's own policy about
// which key does what, and the translation into the keys a widget
// understands. The widget package has no keysyms on purpose.
//
// A repeat arrives as an ordinary event, so holding Backspace erases and
// holding a letter types, with no extra work at this level.
func (a *app) typeKey(ev keyboard.Event) {
	k := a.widgetKey(ev)

	if ev.State == keyboard.Released {
		if a.ui.keyUp(k) {
			a.host.Invalidate()
		}
		return
	}
	if ev.Sym == symEscape {
		// Escape drops the caret in this window, rather than reaching the
		// widgets, where it would only cancel a Space held on the button.
		if a.ui.blur() {
			a.host.Invalidate()
		}
		return
	}
	if k != widget.KeyNone {
		if a.ui.keyDown(k) {
			a.host.Invalidate()
		}
		return
	}
	if a.ui.insert(ev.Text) {
		a.host.Invalidate()
	}
}

// widgetKey translates a keysym into the key the widgets are driven by, or
// KeyNone for one they have no name for — whose text, if it has any, goes
// to the field instead.
//
// Space is the one that depends on what is focused: in the text field it
// is a character like any other, and on the button it is the activation
// that arms on the press and fires on the release.
func (a *app) widgetKey(ev keyboard.Event) widget.Key {
	switch ev.Sym {
	case symLeft:
		return widget.KeyLeft
	case symRight:
		return widget.KeyRight
	case symHome:
		return widget.KeyHome
	case symEnd:
		return widget.KeyEnd
	case symBackSpace:
		return widget.KeyBackspace
	case symDelete:
		return widget.KeyDelete
	case symReturn, symKPEnter:
		return widget.KeyEnter
	case symLeftTab:
		return widget.KeyBacktab
	case symTab:
		// Which of the two a press is cannot be worked out by the widget
		// package: it has no modifier state, and a shifted Tab arrives as
		// either keysym depending on the keymap.
		if ev.Mods.Effective&keyboard.ModShift != 0 {
			return widget.KeyBacktab
		}
		return widget.KeyTab
	case symSpace:
		if a.ui.editing() {
			return widget.KeyNone
		}
		return widget.KeySpace
	}
	return widget.KeyNone
}

// submit starts the slow task Enter stands for: a request to a server,
// say. It is wired to the field's OnSubmit, runs on a goroutine of its
// own, knows nothing of Wayland or the UI, and publishes its outcome
// through Do, which is how any background work reaches the widgets. It
// stops early if the window closes first.
//
// A second Enter while the first is in flight does nothing.
func (a *app) submit(text string) {
	if a.ui.busy {
		return
	}
	delay := submitDelay // read here, on the UI goroutine
	a.ui.busy = true
	a.ui.status = fmt.Sprintf("submitting %q…", text)
	a.host.Invalidate()

	ctx := a.host.Context()
	a.tasks.start(func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		a.host.Do(func() {
			a.ui.busy = false
			a.ui.status = fmt.Sprintf("submitted %q", text)
			a.host.Invalidate()
		})
	})
}

// blink asks the UI to flip the caret twice a second. It is a timer and not
// the frame clock on purpose: a blinking caret needs a repaint every half
// second, not one per frame, and nothing else in the window is animating
// while the user types.
func (a *app) blink() {
	t := time.NewTicker(blinkPeriod)
	defer t.Stop()
	ctx := a.host.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.host.Do(a.blinkTick)
		}
	}
}

// blinkTick flips the caret, and repaints only if there is a caret to
// flip: an unfocused window has nothing to blink and should stay quiet.
func (a *app) blinkTick() {
	if !a.ui.editing() {
		return
	}
	if a.ui.setCaretVisible(!a.ui.caretOn) {
		a.host.Invalidate()
	}
}
