// Command widgets opens a Wayland window holding two controls — a text
// input and a button — and wires them to real pointer and keyboard input.
// Click the input and type; click Clear to empty it.
//
// It is the first example that draws with the canvas package instead of
// poking bytes into the shared-memory buffer by hand, and the first that
// uses a pointer and a keyboard at once. Three things are worth reading it
// for:
//
//  1. Double buffering. The window keeps two shm buffers and hands the
//     compositor whichever one is free, tracking wl_buffer.release to know
//     which that is. Every other example in this tree allocates a fresh
//     memfd per frame, which is fine when a frame is a resize and wrong
//     when a frame is a keystroke. A pool also lets each buffer's mapping
//     — and therefore its Canvas — live as long as the buffer does, which
//     is exactly what canvas.New asks of a caller.
//
//  2. Widget focus is not surface focus. Wayland gives the surface
//     keyboard focus — keyboard.Keyboard reports that through OnFocus —
//     while which control inside it owns the caret is entirely the client's
//     business, and here it is one bool that a pointer press sets.
//
//  3. Press and release are separate events for a reason. The button fires
//     only when both land inside it, so dragging off a pressed button
//     cancels the click the way every real toolkit does.
//
// Text is drawn with basicfont.Face7x13 from golang.org/x/image, blitted
// straight into the pixels the canvas borrowed — canvas fills shapes and
// has no text API. The face is ASCII-only, so accented characters composed
// with dead keys render as the replacement glyph. That is a limit of the
// example's font, not of the keyboard package underneath it, which composes
// them correctly.
package main

import (
	"errors"
	"fmt"
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
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
)

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
// as long as the compositor may read it.
type frame struct {
	buf *wlcore.Buffer
	// data is the mapping, kept for Munmap at teardown. It must stay mapped
	// for the whole life of cv: canvas.New borrows the pixels and never
	// copies them.
	data []byte
	// cv draws into data, viewed as ARGB8888 words. Built once per frame,
	// not once per redraw.
	cv *canvas.Canvas
	// busy means this frame is attached and the compositor has not sent
	// wl_buffer.release for it yet. Drawing into it now would tear.
	busy bool
}

type window struct {
	conn    *wlcore.Conn
	shm     *wlcore.Shm
	surface *wlcore.Surface

	width, height int32

	frames [frameCount]*frame
	// dirty records a redraw that could not happen because every frame was
	// busy. The next wl_buffer.release runs it.
	dirty bool

	ui ui

	capabilities wlcore.SeatCapability

	// kbd owns the keymap, the modifier state, the dead-key composer and
	// the repeat timer. What is left here is policy: which key does what
	// to the input.
	kbd *keyboard.Keyboard

	pointer *wlcore.Pointer
	// ptrX, ptrY are the last surface-local pointer position.
	// wl_pointer.button carries no coordinates — it is defined against the
	// location the last motion or enter event reported.
	ptrX, ptrY float32
}

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

	w := &window{conn: conn, width: defaultWidth, height: defaultHeight}

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
					kbd, kerr := keyboard.New(conn, seat)
					if kerr != nil {
						err = kerr
						break
					}
					w.kbd = kbd
					w.kbd.OnKey = w.typeKey
					w.kbd.OnFocus = w.surfaceFocus
					w.kbd.OnError = func(e error) { log.Printf("keyboard: %v", e) }

					seat.SetListener(wlcore.SeatListener{
						Capabilities: func(capabilities wlcore.SeatCapability) {
							w.capabilities = capabilities
							// The seat listener stays with the window
							// because this example wants the pointer from
							// the same seat; keyboard.Keyboard is told the
							// capabilities rather than claiming them.
							w.kbd.SetCapabilities(capabilities)
							w.syncPointer(seat)
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
			// A zero width/height means "you decide" — keep whatever we have.
			if width > 0 && height > 0 {
				w.width, w.height = width, height
			}
		},
		Close: func() {
			conn.Close()
		},
	})

	xdgSurface.SetListener(xdgshell.SurfaceListener{
		Configure: func(serial uint32) {
			// Nothing may be attached before ack_configure — see wlcore.md.
			if err := xdgSurface.AckConfigure(serial); err != nil {
				log.Printf("ack_configure: %v", err)
				return
			}
			w.redraw()
		},
	})

	// Initial commit without a buffer: this is what makes the compositor
	// send the first xdg_surface.configure.
	if err := w.surface.Commit(); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}

	log.Printf("widgets: click the text field and type; Ctrl-C to quit")

	err = w.loop()
	w.releaseFrames()
	if err != nil && !errors.Is(err, wlcore.ErrClosed) {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}

// loop pumps until the connection dies. It is conn.Run() with one addition:
// a held key is due to repeat at a moment the compositor will not tell us
// about, so the loop wakes on the earlier of "a message arrived" and "the
// next repeat is due".
//
// NextRepeat returns the zero time when no key is held, and that is exactly
// the zero DispatchUntil reads as "no deadline" — so the idle case blocks
// on the socket just as Run did, with no branch here to say so.
func (w *window) loop() error {
	for {
		if err := w.conn.DispatchUntil(w.kbd.NextRepeat()); err != nil {
			return err
		}
		w.kbd.Tick(time.Now())
	}
}

// redraw paints and presents one frame, or defers if the pool is exhausted.
func (w *window) redraw() {
	if w.conn.Err() != nil {
		return
	}
	if err := w.ensureFrames(); err != nil {
		log.Printf("frames: %v", err)
		return
	}

	f := w.freeFrame()
	if f == nil {
		// Both buffers are still with the compositor. Dropping the frame
		// would lose the keystroke that caused it, so remember that a
		// repaint is owed and let wl_buffer.release run it.
		w.dirty = true
		return
	}
	w.dirty = false

	draw(f.cv, computeLayout(float32(w.width), float32(w.height)), &w.ui)
	if err := f.cv.Err(); err != nil {
		// canvas errors are sticky, so this frame is partly drawn and every
		// later frame in this Canvas would be a no-op. Nothing sensible is
		// left to present.
		log.Printf("canvas: %v", err)
		return
	}

	if err := w.surface.Attach(f.buf, 0, 0); err != nil {
		log.Printf("attach: %v", err)
		return
	}
	// The whole buffer, because the whole buffer was repainted: this frame's
	// previous contents are two frames old, so there is nothing to preserve
	// and nothing to track. Canvas.Damage is what an example that repainted
	// only what changed would send instead — text included, since glyphs go
	// through canvas.DrawMask and are damage-tracked like any other drawing.
	if err := w.surface.DamageBuffer(0, 0, w.width, w.height); err != nil {
		log.Printf("damage_buffer: %v", err)
		return
	}
	if err := w.surface.Commit(); err != nil {
		log.Printf("commit: %v", err)
		return
	}
	f.busy = true
}

// freeFrame returns a frame the compositor is not reading, or nil.
func (w *window) freeFrame() *frame {
	for _, f := range w.frames {
		if f != nil && !f.busy {
			return f
		}
	}
	return nil
}

// ensureFrames (re)builds the pool when the window size changes. Buffers are
// immutable in size, so a resize replaces the whole pool rather than growing
// it.
func (w *window) ensureFrames() error {
	if f := w.frames[0]; f != nil && f.cv.PixelWidth() == int(w.width) && f.cv.PixelHeight() == int(w.height) {
		return nil
	}
	w.releaseFrames()

	for i := range w.frames {
		f, err := w.newFrame(w.width, w.height)
		if err != nil {
			return err
		}
		w.frames[i] = f
	}
	return nil
}

// releaseFrames tears the pool down. Destroying a wl_buffer the compositor
// still holds is allowed — it keeps the contents it already read — and our
// munmap does not disturb it either, since it maps the memfd itself.
func (w *window) releaseFrames() {
	for i, f := range w.frames {
		if f == nil {
			continue
		}
		if err := f.buf.Destroy(); err != nil {
			log.Printf("buffer destroy: %v", err)
		}
		if err := unix.Munmap(f.data); err != nil {
			log.Printf("munmap: %v", err)
		}
		w.frames[i] = nil
	}
}

// newFrame allocates one pooled buffer: a sealed memfd, a mapping that
// outlives the call, and a Canvas over that mapping.
func (w *window) newFrame(width, height int32) (*frame, error) {
	stride := int(width) * bytesPerPixel
	size := stride * int(height)

	fd, err := unix.MemfdCreate("ggui-widgets", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("memfd_create: %w", err)
	}
	// The mapping outlives the descriptor: wl_shm keeps its own reference
	// through create_pool, and mmap keeps ours.
	defer unix.Close(fd)

	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		return nil, fmt.Errorf("ftruncate: %w", err)
	}

	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		_ = unix.Munmap(data)
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
		return nil, fmt.Errorf("canvas: %w", err)
	}

	pool, err := w.shm.CreatePool(fd, int32(size))
	if err != nil {
		_ = unix.Munmap(data)
		return nil, fmt.Errorf("create_pool: %w", err)
	}
	// The pool has served its purpose once the buffer exists; the buffer
	// keeps the storage alive on its own.
	defer pool.Destroy()

	buf, err := pool.CreateBuffer(0, width, height, int32(stride), wlcore.ShmFormatArgb8888)
	if err != nil {
		_ = unix.Munmap(data)
		return nil, fmt.Errorf("create_buffer: %w", err)
	}

	f := &frame{buf: buf, data: data, cv: cv}
	buf.SetListener(wlcore.BufferListener{
		Release: func() {
			f.busy = false
			// A repaint that found the pool exhausted is owed one now.
			if w.dirty {
				w.redraw()
			}
		},
	})
	return f, nil
}

// syncPointer follows the seat's pointer capability in both directions: it
// can appear and disappear at runtime, and the object has to be released
// when it goes.
func (w *window) syncPointer(seat *wlcore.Seat) {
	has := w.capabilities.Has(wlcore.SeatCapabilityPointer)

	switch {
	case has && w.pointer == nil:
		pointer, err := seat.GetPointer()
		if err != nil {
			log.Printf("get_pointer: %v", err)
			return
		}
		w.pointer = pointer
		w.armPointer(pointer)
	case !has && w.pointer != nil:
		if err := w.pointer.Release(); err != nil {
			log.Printf("pointer release: %v", err)
		}
		w.pointer = nil
		// Hover and the armed state describe a pointer that no longer
		// exists; leaving them set would freeze the button mid-press.
		if w.ui.button.PointerLeave() {
			w.redraw()
		}
	}
}

func (w *window) armPointer(pointer *wlcore.Pointer) {
	pointer.SetListener(wlcore.PointerListener{
		Enter: func(_ uint32, _ *wlcore.Surface, surfaceX, surfaceY wlcore.Fixed) {
			w.pointerAt(surfaceX, surfaceY)
			if w.ui.pointerMoved(w.layout(), w.ptrX, w.ptrY) {
				w.redraw()
			}
		},

		Leave: func(uint32, *wlcore.Surface) {
			// The pointer is gone from this surface, so a press it started
			// here can never be completed here.
			if w.ui.button.PointerLeave() {
				w.redraw()
			}
		},

		Motion: func(_ uint32, surfaceX, surfaceY wlcore.Fixed) {
			w.pointerAt(surfaceX, surfaceY)
			if w.ui.pointerMoved(w.layout(), w.ptrX, w.ptrY) {
				w.redraw()
			}
		},

		Button: func(_ uint32, _ uint32, button uint32, state wlcore.PointerButtonState) {
			if button != btnLeft {
				return
			}
			// wl_pointer.button carries no coordinates: it is defined
			// against the position the last motion or enter event gave.
			l := w.layout()
			if state == wlcore.PointerButtonStatePressed {
				w.ui.pointerPressed(l, w.ptrX, w.ptrY)
			} else if w.ui.pointerReleased(l, w.ptrX, w.ptrY) {
				log.Printf("cleared")
			}
			// Both edges change something visible — the focus ring, the
			// button's fill, or the text — and a click is rare enough that
			// working out which is not worth the branch.
			w.redraw()
		},
	})
}

// pointerAt converts a surface-local position into the logical units the
// layout is expressed in. They coincide while scale is 1; the conversion is
// written out so that changing scale does not silently break hit testing.
func (w *window) pointerAt(x, y wlcore.Fixed) {
	w.ptrX = float32(x.Float64())
	w.ptrY = float32(y.Float64())
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
			w.redraw()
		}
		return
	case symReturn, symKPEnter:
		log.Printf("submitted %q", string(w.ui.text))
		return
	case symEscape:
		// typeKey already returned unless the input had focus, so this
		// always changes something.
		w.ui.focused = false
		w.redraw()
		return
	}

	if w.ui.insert(ev.Text) {
		w.redraw()
	}
}

// surfaceFocus follows the keyboard focus of the whole surface, which is a
// different thing from which control inside it owns the caret. Losing it
// has to drop the caret too: the user is typing somewhere else now.
func (w *window) surfaceFocus(surface *wlcore.Surface) {
	if surface == nil && w.ui.focused {
		w.ui.focused = false
		w.redraw()
	}
}
