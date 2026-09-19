// Command widgets opens a Wayland window holding two controls — a text
// input and a button — and wires them to real pointer and keyboard input.
// Click the input and type; click Clear to empty it; press Enter to submit.
//
// It is the example for the eventloop package, and four things are worth
// reading it for:
//
//  1. Two goroutines. The one that runs main owns the Wayland connection:
//     it reads the socket, feeds keyboard and pointer, answers the
//     compositor's pings and makes every request. The UI runs on another and
//     owns the widgets and the canvases. They talk only by messages —
//     keyboard.Keyboard and pointer.Pointer callbacks push events to the UI,
//     and the UI posts closures for the Wayland goroutine to run — so a slow
//     frame never delays a ping and a stalled socket never freezes the UI.
//
//  2. The window opens first. Everything the UI needs before it can show
//     itself, here the system font, loads on a goroutine of its own while the
//     window already shows a loader. Set WIDGETS_SLOW_INIT=3s to see it.
//
//  3. Animation and background work. The loader and the busy indicator run
//     off the compositor's frame callbacks, so an idle window draws nothing.
//     The caret blinks from a timer that asks the UI to repaint twice a
//     second. Enter starts a slow task on its own goroutine, whose result
//     comes back through UI.Do.
//
//  4. Double buffering, with the pool built across the two goroutines: the
//     UI maps the memory and owns each buffer's canvas, the Wayland goroutine
//     creates the wl_buffer, and release events travel back as events.
//
// Widget focus is not surface focus: Wayland gives the surface keyboard focus,
// while which control inside it owns the caret is entirely the client's
// business, and here it is one bool that a pointer press sets. Press and
// release are separate events for a reason too: the button fires only when
// both land inside it, so dragging off a pressed button cancels the click.
//
// The fallback text is drawn with basicfont.Face7x13 from golang.org/x/image,
// blitted straight into the pixels the canvas borrowed — canvas fills shapes
// and has no text API. The face is ASCII-only, so accented characters composed
// with dead keys render as the replacement glyph when no system font is found.
// That is a limit of the example's font, not of the keyboard package.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
	"github.com/romycode/ggui/widget"
)

const (
	defaultWidth  = 640
	defaultHeight = 220
	bytesPerPixel = 4

	// frameCount is the depth of the buffer pool. Two is enough for input-
	// driven redraws: the compositor releases the previous buffer at the
	// next composite, long before a human produces another keystroke.
	frameCount = 2

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

// The keysyms the input handles itself, before anything reaches the
// composer. The keyboard package carries keysym *names* rather than Go
// constants, so editing keys have to be spelled out; the values are the ones
// keyboard/keysyms.gen.go maps to these names.
const (
	symBackSpace = keyboard.Keysym(0xff08)
	symReturn    = keyboard.Keysym(0xff0d)
	symKPEnter   = keyboard.Keysym(0xff8d)
	symEscape    = keyboard.Keysym(0xff1b)
)

// scale is physical pixels per logical unit. This example deliberately
// stays at 1: wp_fractional_scale and wl_surface.set_buffer_scale are what
// example/hidpi and example/scaling are for, and canvas takes the factor as
// a constructor argument, so wiring one in later touches only newFrame.
const scale = 1.0

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// frame is one pooled shm buffer and everything that has to stay alive for
// as long as the compositor may read it. It straddles the two goroutines:
// the UI maps the memory and owns the canvas, the Wayland goroutine creates
// the wl_buffer, and buf stays nil until that has happened.
type frame struct {
	// buf is set on the UI goroutine once the Wayland goroutine has created
	// it. Until then the frame cannot be painted into: there would be nothing
	// to hand the compositor.
	buf *wlcore.Buffer
	// data is the mapping, kept for Munmap at teardown. It must stay mapped
	// for the whole life of cv: canvas.New borrows the pixels and never
	// copies them.
	data []byte
	// cv draws into data, viewed as ARGB8888 words. Built once per frame,
	// not once per repaint.
	cv *canvas.Canvas
	// busy means this frame is attached and the compositor has not sent
	// wl_buffer.release for it yet. Drawing into it now would tear.
	busy bool
	// dead means a resize replaced this frame while its wl_buffer was still
	// being created, so the buffer must be destroyed instead of adopted.
	dead bool
}

// window is split by goroutine, and each field says which side owns it.
// Touching the other side's fields is what the eventloop package exists to
// prevent, so nothing below does.
type window struct {
	// Owned by the Wayland goroutine.
	conn    *wlcore.Conn
	shm     *wlcore.Shm
	surface *wlcore.Surface
	loop    *eventloop.Loop
	kbd     *keyboard.Keyboard
	ptr     *pointer.Pointer
	// pendingW and pendingH are the size xdg_toplevel.configure announced,
	// applied when the xdg_surface.configure that follows it is acked.
	pendingW, pendingH int32

	// Owned by the UI goroutine.
	ev            *eventloop.UI
	width, height int32
	frames        [frameCount]*frame
	// ui is the widget state. It is never nil: it starts on the built-in
	// bitmap font, which costs no I/O, and is replaced by one on the system
	// font once that has loaded. Focus events reach it while the window is
	// still loading, which is why it has to exist from the first frame.
	ui *ui

	// post queues a closure for the Wayland goroutine. It is loop.Post once
	// the loop exists, and does nothing before that, so a window can be built
	// and exercised without a connection.
	post func(func())

	workers sync.WaitGroup
}

// newWindow returns a window on conn, with its UI on font. font is only the
// starting point: run swaps in the system font when it has loaded.
func newWindow(conn *wlcore.Conn, font widget.Font) *window {
	return &window{
		conn:   conn,
		width:  defaultWidth,
		height: defaultHeight,
		ui:     newUI(font),
		ev:     eventloop.NewUI(),
		post:   func(func()) {},
	}
}

func (w *window) startWorker(fn func()) {
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		fn()
	}()
}

func (w *window) waitWorkers() { w.workers.Wait() }

func run() error {
	conn, err := wlcore.Connect()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	conn.OnError(func(objectID, code uint32, msg string) {
		log.Printf("wayland: protocol error on object %d (code %d): %s", objectID, code, msg)
	})

	reg, err := conn.Display().GetRegistry()
	if err != nil {
		return fmt.Errorf("get_registry: %w", err)
	}

	// The window starts on the built-in font, which needs no disk access:
	// nothing on the way to the first frame may wait for the system font.
	w := newWindow(conn, bitmapFont{})

	var (
		compositor *wlcore.Compositor
		wmBase     *xdgshell.WmBase
		seat       *wlcore.Seat
		bindErr    error
	)
	reg.SetListener(wlcore.RegistryListener{
		Global: func(name uint32, iface string, version uint32) {
			// err is per-event on purpose: assigning straight into bindErr
			// from every branch lets a later successful bind overwrite an
			// earlier failure, and the first failure is the interesting one.
			var err error
			switch iface {
			case wlcore.CompositorInterface.Name:
				compositor, err = reg.Bind(name, version, wlcore.CompositorInterface)
			case wlcore.ShmInterface.Name:
				w.shm, err = reg.Bind(name, version, wlcore.ShmInterface)
			case xdgshell.WmBaseInterface.Name:
				wmBase, err = reg.Bind(name, version, xdgshell.WmBaseInterface)
			case wlcore.SeatInterface.Name:
				seat, err = reg.Bind(name, version, wlcore.SeatInterface)
				if seat != nil {
					// wl_seat.capabilities arrives right after bind, in the
					// same batch this Global callback runs in, so both the
					// Keyboard and the listener have to be live now or the
					// first event is lost.
					//
					// The callbacks below run on the Wayland goroutine, inside
					// the dispatch. They do not touch the UI: each one turns
					// what it was given into an event and pushes it.
					kbd, kerr := keyboard.New(conn, seat)
					if kerr != nil {
						err = kerr
						break
					}
					w.kbd = kbd
					w.kbd.OnKey = func(ev keyboard.Event) {
						w.ev.Push(eventloop.Event{Kind: eventloop.EvKey, Key: ev})
					}
					w.kbd.OnFocus = func(surface *wlcore.Surface) {
						w.ev.Push(eventloop.Event{Kind: eventloop.EvKeyboardFocus, Surface: surface})
					}
					w.kbd.OnError = func(e error) { log.Printf("keyboard: %v", e) }

					ptr, perr := pointer.New(conn, seat)
					if perr != nil {
						err = perr
						break
					}
					w.ptr = ptr
					w.ptr.OnEvent = func(ev pointer.Event) {
						w.ev.Push(eventloop.Event{Kind: eventloop.EvPointer, Pointer: ev})
					}
					w.ptr.OnFocus = func(surface *wlcore.Surface) {
						w.ev.Push(eventloop.Event{Kind: eventloop.EvPointerFocus, Surface: surface})
					}
					w.ptr.OnError = func(e error) { log.Printf("pointer: %v", e) }

					seat.SetListener(wlcore.SeatListener{
						Capabilities: func(capabilities wlcore.SeatCapability) {
							// The seat listener stays with the window because
							// keyboard and pointer share it. Each controller is
							// told the capabilities rather than claiming it.
							w.kbd.SetCapabilities(capabilities)
							w.ptr.SetCapabilities(capabilities)
						},
					})
				}
			}
			if err != nil && bindErr == nil {
				bindErr = err
			}
		},
	})
	if err := conn.Roundtrip(); err != nil {
		return fmt.Errorf("roundtrip: %w", err)
	}
	if bindErr != nil {
		return fmt.Errorf("bind global: %w", bindErr)
	}
	if compositor == nil || w.shm == nil || wmBase == nil || seat == nil {
		return errors.New("compositor is missing wl_compositor, wl_shm, xdg_wm_base or wl_seat")
	}
	defer w.kbd.Close()
	defer w.ptr.Close()

	wmBase.SetListener(xdgshell.WmBaseListener{
		Ping: func(serial uint32) {
			// A close event dispatched earlier in the same Dispatch() batch
			// may have already closed the connection — nothing to pong to.
			if conn.Err() != nil {
				return
			}
			if err := wmBase.Pong(serial); err != nil {
				log.Printf("pong: %v", err)
			}
		},
	})

	w.surface, err = compositor.CreateSurface()
	if err != nil {
		return fmt.Errorf("create_surface: %w", err)
	}

	xdgSurface, err := wmBase.GetXdgSurface(w.surface)
	if err != nil {
		return fmt.Errorf("get_xdg_surface: %w", err)
	}

	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return fmt.Errorf("get_toplevel: %w", err)
	}
	if err := toplevel.SetTitle("ggui widgets — click the field and type"); err != nil {
		return fmt.Errorf("set_title: %w", err)
	}
	if err := toplevel.SetAppID("ggui.example.widgets"); err != nil {
		return fmt.Errorf("set_app_id: %w", err)
	}

	toplevel.SetListener(xdgshell.ToplevelListener{
		Configure: func(width, height int32, _ []byte) {
			// A zero width/height means "you decide", which the UI reads
			// as keep what it has. The size only takes effect with the
			// xdg_surface.configure that follows.
			w.pendingW, w.pendingH = width, height
		},
		Close: func() {
			conn.Close()
		},
	})

	xdgSurface.SetListener(xdgshell.SurfaceListener{
		Configure: func(serial uint32) {
			// Nothing may be attached before ack_configure — see wlcore.md.
			// The ack is a request, so it happens here, on the Wayland
			// goroutine, before the UI hears of the new size and can paint
			// a buffer for it.
			if err := xdgSurface.AckConfigure(serial); err != nil {
				log.Printf("ack_configure: %v", err)
				return
			}
			w.ev.Push(eventloop.Event{Kind: eventloop.EvConfigure, Width: w.pendingW, Height: w.pendingH})
		},
	})

	// Initial commit without a buffer: this is what makes the compositor
	// send the first xdg_surface.configure.
	if err := w.surface.Commit(); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}

	log.Printf("widgets: click the text field and type; Enter submits; Ctrl-C to quit")

	w.loop, err = eventloop.New(conn)
	if err != nil {
		return fmt.Errorf("event loop: %w", err)
	}
	// A held key repeats at a moment the compositor will not announce, so the
	// loop wakes for the keyboard's own timer as well as for the socket.
	w.loop.Deadline = w.kbd.NextRepeat
	w.loop.OnTick = func(now time.Time) { w.kbd.Tick(now) }
	w.post = w.loop.Post

	// From here the window is open and the connection is being served. The
	// UI goroutine starts painting the loader at the first configure, and
	// the slow part of startup runs beside it.
	uiDone := make(chan struct{})
	go func() {
		defer close(uiDone)
		w.ev.Run(eventloop.Handler{OnEvent: w.onEvent, Paint: w.paint})
	}()
	w.startWorker(w.blink)
	w.startWorker(w.initialize)

	err = w.loop.Run()
	w.ev.Push(eventloop.Event{Kind: eventloop.EvClosed})
	<-uiDone
	w.waitWorkers()
	if err != nil && !errors.Is(err, wlcore.ErrClosed) {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}

// initialize is the application's slow startup, on a goroutine of its own:
// everything that has to exist before the real UI can be shown, and nothing
// the window needed to open. Here that is the system font, whose discovery
// walks the disk. It finishes by handing the result to the UI goroutine and
// telling it the real UI is ready.
//
// WIDGETS_SLOW_INIT=3s holds it back, to see the loader.
func (w *window) initialize() {
	if d, err := time.ParseDuration(os.Getenv("WIDGETS_SLOW_INIT")); err == nil && d > 0 {
		log.Printf("widgets: WIDGETS_SLOW_INIT=%v, holding the loader", d)
		select {
		case <-time.After(d):
		case <-w.ev.Context().Done():
			return
		}
	}
	ctx := w.ev.Context()
	built := newUI(loadFont(ctx))
	if ctx.Err() != nil {
		return
	}
	w.ev.Do(func() {
		w.ui = built
		w.ev.SetReady()
	})
}

// onEvent handles what the Wayland goroutine sent, on the UI goroutine.
func (w *window) onEvent(ev eventloop.Event) {
	switch ev.Kind {
	case eventloop.EvConfigure:
		w.applyConfigure(ev.Width, ev.Height)
		w.ensureFrames()
	case eventloop.EvBufferRelease:
		for _, f := range w.frames {
			if f != nil && f.buf == ev.Buffer {
				f.busy = false
			}
		}
		w.ev.SetBufferFree(w.freeFrame() != nil)
	case eventloop.EvKey:
		w.typeKey(ev.Key)
	case eventloop.EvPointer:
		w.pointerEvent(ev.Pointer)
	case eventloop.EvKeyboardFocus:
		w.surfaceFocus(ev.Surface)
	case eventloop.EvPointerFocus:
		w.pointerFocus(ev.Surface)
	case eventloop.EvClosed:
		// The connection is gone, so there is nobody to destroy buffers with;
		// the mappings still have to go.
		w.releaseFrames(false)
	}
}

// applyConfigure adopts the size the compositor asked for and reports whether
// it changed. A zero in either dimension means "you decide": keep what we
// have.
func (w *window) applyConfigure(width, height int32) bool {
	if width <= 0 || height <= 0 || (width == w.width && height == w.height) {
		return false
	}
	w.width, w.height = width, height
	return true
}

// paint draws one frame into a free buffer and hands it to the compositor.
// The UI calls it when the frame clock says a frame is due, so it decides
// nothing about timing: which screen to draw is the phase, and whether the
// window wants another frame right away is whether a task is running.
func (w *window) paint(now uint32) (presented, animating bool) {
	f := w.freeFrame()
	if f == nil {
		// Both buffers are with the compositor, or the pool is still being
		// built. Say so, and the UI waits for the release instead of spinning.
		w.ev.SetBufferFree(false)
		return false, false
	}

	phase := w.ev.Phase()
	switch phase {
	case eventloop.PhaseLoading:
		eventloop.PaintLoader(f.cv, float32(w.width), float32(w.height), now)
	case eventloop.PhaseFailed:
		eventloop.PaintFailed(f.cv, float32(w.width), float32(w.height))
	default:
		w.ui.now = now
		draw(f.cv, w.layout(), w.ui)
	}
	if err := f.cv.Err(); err != nil {
		// canvas errors are sticky, so this frame is partly drawn and every
		// later frame in this Canvas would be a no-op. Nothing sensible is
		// left to present.
		log.Printf("canvas: %v", err)
		return false, false
	}

	f.busy = true
	w.present(f)
	return true, phase == eventloop.PhaseReady && w.ui.animating()
}

// present hands f to the compositor. It only queues: attach, damage, the
// frame callback and commit are requests, so they run on the Wayland
// goroutine, and the callback's answer comes back as an event.
func (w *window) present(f *frame) {
	buf, width, height := f.buf, w.width, w.height
	surface := w.surface // set before the UI goroutine started
	w.post(func() {
		if err := surface.Attach(buf, 0, 0); err != nil {
			log.Printf("attach: %v", err)
			return
		}
		// The whole buffer, because the whole buffer was repainted: this
		// frame's previous contents are two frames old, so there is nothing
		// to preserve and nothing to track.
		if err := surface.DamageBuffer(0, 0, width, height); err != nil {
			log.Printf("damage_buffer: %v", err)
			return
		}
		// Asked for before the commit: the callback is answered when this
		// commit is presented, and that answer is what lets the UI paint the
		// next frame.
		cb, err := surface.Frame()
		if err != nil {
			log.Printf("frame: %v", err)
			return
		}
		cb.SetListener(wlcore.CallbackListener{Done: func(t uint32) {
			w.ev.Push(eventloop.Event{Kind: eventloop.EvFrameDone, Time: t})
		}})
		if err := surface.Commit(); err != nil {
			log.Printf("commit: %v", err)
		}
	})
}

// freeFrame returns a frame the compositor is not reading and whose buffer
// exists, or nil.
func (w *window) freeFrame() *frame {
	for _, f := range w.frames {
		if f != nil && f.buf != nil && !f.busy && !f.dead {
			return f
		}
	}
	return nil
}

// ensureFrames (re)builds the pool when the window size changes. Buffers are
// immutable in size, so a resize replaces the whole pool rather than growing
// it.
func (w *window) ensureFrames() {
	if f := w.frames[0]; f != nil && !f.dead && f.cv.PixelWidth() == int(w.width) && f.cv.PixelHeight() == int(w.height) {
		return
	}
	w.releaseFrames(true)

	for i := range w.frames {
		f, err := w.newFrame(w.width, w.height)
		if err != nil {
			log.Printf("frames: %v", err)
			return
		}
		w.frames[i] = f
	}
}

// releaseFrames tears the pool down. Destroying a wl_buffer the compositor
// still holds is allowed — it keeps the contents it already read — and our
// munmap does not disturb it either, since it maps the memfd itself. destroy
// is false when the connection is already gone.
func (w *window) releaseFrames(destroy bool) {
	for i, f := range w.frames {
		if f == nil {
			continue
		}
		f.dead = true
		if buf := f.buf; buf != nil && destroy {
			w.post(func() {
				if err := buf.Destroy(); err != nil {
					log.Printf("buffer destroy: %v", err)
				}
			})
		}
		if err := unix.Munmap(f.data); err != nil {
			log.Printf("munmap: %v", err)
		}
		w.frames[i] = nil
	}
}

// newFrame allocates one pooled buffer: a sealed memfd, a mapping that
// outlives the call, and a Canvas over that mapping. That much is memory and
// belongs to the UI goroutine. The wl_buffer over it is a request, so it is
// created on the Wayland goroutine, and the frame is usable once that has
// reported back.
func (w *window) newFrame(width, height int32) (*frame, error) {
	stride := int(width) * bytesPerPixel
	size := stride * int(height)

	fd, err := unix.MemfdCreate("ggui-widgets", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("memfd_create: %w", err)
	}
	// fd now belongs to the closure posted below, which closes it once
	// wl_shm holds its own reference. Every error path before that closes it
	// here instead.

	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ftruncate: %w", err)
	}

	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("mmap: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		_ = unix.Munmap(data)
		unix.Close(fd)
		return nil, fmt.Errorf("fcntl seal: %w", err)
	}

	// canvas takes []uint32 and mmap returns []byte; Go has no safe
	// conversion between slice element types, so the two are bridged here
	// once per buffer rather than once per frame. The mapping is page
	// aligned, so the uint32 view is aligned too.
	//
	// This reinterprets in the host's byte order, while wl_shm defines
	// argb8888 as little endian regardless of host (wayland.xml, wl_shm
	// format). They agree on every architecture Wayland is used on; a big
	// endian host would need a converting blit here, which is a property of
	// canvas storing 0xAARRGGBB in a machine word, not of this example.
	px := unsafe.Slice((*uint32)(unsafe.Pointer(&data[0])), size/bytesPerPixel)

	cv, err := canvas.New(canvas.Buffer{
		Pixels: px,
		Width:  int(width),
		Height: int(height),
		Stride: int(width),
	}, int(width), int(height), scale)
	if err != nil {
		_ = unix.Munmap(data)
		unix.Close(fd)
		return nil, fmt.Errorf("canvas: %w", err)
	}

	f := &frame{data: data, cv: cv}
	w.post(func() { w.createBuffer(f, fd, size, width, height) })
	return f, nil
}

// createBuffer makes the wl_buffer over f's memory. It runs on the Wayland
// goroutine, and reports back to the UI through Do, which is the only way
// the frame's state may change.
func (w *window) createBuffer(f *frame, fd, size int, width, height int32) {
	// The mapping outlives the descriptor: wl_shm keeps its own reference
	// through create_pool, and mmap keeps ours.
	defer unix.Close(fd)

	pool, err := w.shm.CreatePool(fd, int32(size))
	if err != nil {
		log.Printf("create_pool: %v", err)
		return
	}
	// The pool has served its purpose once the buffer exists; the buffer
	// keeps the storage alive on its own.
	defer pool.Destroy()

	buf, err := pool.CreateBuffer(0, width, height, width*bytesPerPixel, wlcore.ShmFormatArgb8888)
	if err != nil {
		log.Printf("create_buffer: %v", err)
		return
	}
	buf.SetListener(wlcore.BufferListener{
		Release: func() {
			w.ev.Push(eventloop.Event{Kind: eventloop.EvBufferRelease, Buffer: buf})
		},
	})

	w.ev.Do(func() {
		if f.dead {
			// A resize replaced this frame while its buffer was on the way.
			w.post(func() {
				if err := buf.Destroy(); err != nil {
					log.Printf("buffer destroy: %v", err)
				}
			})
			return
		}
		f.buf = buf
		w.ev.SetBufferFree(true)
		w.ev.Invalidate() // whatever was waiting for a buffer can paint now
	})
}

func (w *window) pointerEvent(ev pointer.Event) {
	l := w.layout()
	switch ev.Kind {
	case pointer.Position:
		if w.ui.pointerMoved(l, ev.X, ev.Y) {
			w.ev.Invalidate()
		}
	case pointer.ButtonDown:
		if ev.Button == btnLeft {
			w.ui.pointerPressed(l, ev.X, ev.Y)
			w.ev.Invalidate()
		}
	case pointer.ButtonUp:
		if ev.Button == btnLeft {
			if w.ui.pointerReleased(l, ev.X, ev.Y) {
				log.Printf("cleared")
			}
			w.ev.Invalidate()
		}
	}
}

func (w *window) pointerFocus(surface *wlcore.Surface) {
	if surface == nil && w.ui.button.PointerLeave() {
		w.ev.Invalidate()
	}
}

func (w *window) layout() layout {
	return computeLayout(float32(w.width), float32(w.height))
}

// typeKey turns one key event into an edit. Everything below the keysym —
// the keycode arithmetic, the keymap, the dead keys, the modifiers that
// must not reach the composer — is the keyboard package's; what is left
// here is this window's own policy about which key does what.
//
// A repeat arrives as an ordinary event, so holding backspace erases and
// holding a letter types, with no extra work at this level.
func (w *window) typeKey(ev keyboard.Event) {
	if ev.State == keyboard.Released || !w.ui.focused {
		return
	}

	switch ev.Sym {
	case symBackSpace:
		if w.ui.backspace() {
			w.ev.Invalidate()
		}
		return
	case symReturn, symKPEnter:
		w.submit()
		return
	case symEscape:
		// typeKey already returned unless the input had focus, so this
		// always changes something.
		w.ui.focused = false
		w.ev.Invalidate()
		return
	}

	if w.ui.insert(ev.Text) {
		w.ev.Invalidate()
	}
}

// submit starts the slow task Enter stands for: a request to a server, say.
// It runs on a goroutine of its own, knows nothing of Wayland or the UI, and
// publishes its outcome through Do, which is how any background work reaches
// the widgets. It stops early if the window closes first.
//
// A second Enter while the first is in flight does nothing.
func (w *window) submit() {
	if w.ui.busy {
		return
	}
	text := string(w.ui.text)
	delay := submitDelay // read here, on the UI goroutine
	w.ui.busy = true
	w.ui.status = fmt.Sprintf("submitting %q…", text)
	w.ev.Invalidate()

	ctx := w.ev.Context()
	w.startWorker(func() {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		w.ev.Do(func() {
			w.ui.busy = false
			w.ui.status = fmt.Sprintf("submitted %q", text)
			w.ev.Invalidate()
		})
	})
}

// blink asks the UI to flip the caret twice a second. It is a timer and not
// a frame clock on purpose: a blinking caret needs a repaint every half
// second, not one per frame, and nothing else in the window is animating
// while the user types.
func (w *window) blink() {
	t := time.NewTicker(blinkPeriod)
	defer t.Stop()
	ctx := w.ev.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.ev.Do(w.blinkTick)
		}
	}
}

// blinkTick flips the caret, and repaints only if there is a caret to flip:
// an unfocused window has nothing to blink and should stay quiet.
func (w *window) blinkTick() {
	if !w.ui.focused {
		return
	}
	w.ui.caretOn = !w.ui.caretOn
	w.ev.Invalidate()
}

// surfaceFocus follows the keyboard focus of the whole surface, which is a
// different thing from which control inside it owns the caret. Losing it
// has to drop the caret too: the user is typing somewhere else now.
func (w *window) surfaceFocus(surface *wlcore.Surface) {
	if surface == nil && w.ui.focused {
		w.ui.focused = false
		w.ev.Invalidate()
	}
}
