package window

import (
	"fmt"
	"log"

	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

// setup binds the globals, opens the surface with its xdg role and commits it
// empty, which is what makes the compositor answer with the first configure.
//
// It runs on the goroutine that becomes the Wayland goroutine, before the
// loop starts, and is the only place requests are made outside it. Nothing
// here waits for the application: the way to the first frame knows nothing
// about it.
func (w *Window) setup(cfg Config) error {
	w.conn.OnError(func(objectID, code uint32, msg string) {
		log.Printf("window: protocol error on object %d (code %d): %s", objectID, code, msg)
	})

	registry, err := w.conn.Display().GetRegistry()
	if err != nil {
		return fmt.Errorf("window: get_registry: %w", err)
	}

	var (
		compositor *wlcore.Compositor
		wmBase     *xdgshell.WmBase
		bindErr    error
	)
	registry.SetListener(wlcore.RegistryListener{
		Global: func(name uint32, iface string, version uint32) {
			// err is per-event on purpose: assigning straight into bindErr
			// from every branch would let a later success overwrite an earlier
			// failure, and the first failure is the interesting one.
			var err error
			switch iface {
			case wlcore.CompositorInterface.Name:
				compositor, err = registry.Bind(name, version, wlcore.CompositorInterface)
			case wlcore.ShmInterface.Name:
				w.shm, err = registry.Bind(name, version, wlcore.ShmInterface)
			case xdgshell.WmBaseInterface.Name:
				wmBase, err = registry.Bind(name, version, xdgshell.WmBaseInterface)
			case wlcore.SeatInterface.Name:
				var seat *wlcore.Seat
				if seat, err = registry.Bind(name, version, wlcore.SeatInterface); err == nil {
					err = w.bindSeat(seat)
				}
			}
			if err != nil && bindErr == nil {
				bindErr = err
			}
		},
	})
	// The registry's globals are all in by the time the round trip ends.
	if err := w.conn.Roundtrip(); err != nil {
		return fmt.Errorf("window: roundtrip: %w", err)
	}
	if bindErr != nil {
		return fmt.Errorf("window: bind global: %w", bindErr)
	}

	// The required globals are checked before anything is created: a window
	// that cannot be opened must leave no surface behind. wl_seat is not one
	// of them — a session with no keyboard and no pointer is still a session.
	for _, required := range []struct {
		bound bool
		name  string
	}{
		{compositor != nil, wlcore.CompositorInterface.Name},
		{w.shm != nil, wlcore.ShmInterface.Name},
		{wmBase != nil, xdgshell.WmBaseInterface.Name},
	} {
		if !required.bound {
			return fmt.Errorf("window: the compositor does not offer %s", required.name)
		}
	}

	wmBase.SetListener(xdgshell.WmBaseListener{
		Ping: func(serial uint32) {
			// A close dispatched earlier in the same batch may already have
			// closed the connection: there is nothing left to pong to.
			if w.conn.Err() != nil {
				return
			}
			if err := wmBase.Pong(serial); err != nil {
				log.Printf("window: pong: %v", err)
			}
		},
	})

	if w.surface, err = compositor.CreateSurface(); err != nil {
		return fmt.Errorf("window: create_surface: %w", err)
	}
	xdgSurface, err := wmBase.GetXdgSurface(w.surface)
	if err != nil {
		return fmt.Errorf("window: get_xdg_surface: %w", err)
	}
	if w.toplevel, err = xdgSurface.GetToplevel(); err != nil {
		return fmt.Errorf("window: get_toplevel: %w", err)
	}
	if cfg.Title != "" {
		if err := w.toplevel.SetTitle(cfg.Title); err != nil {
			return fmt.Errorf("window: set_title: %w", err)
		}
	}
	if cfg.AppID != "" {
		if err := w.toplevel.SetAppID(cfg.AppID); err != nil {
			return fmt.Errorf("window: set_app_id: %w", err)
		}
	}

	w.toplevel.SetListener(xdgshell.ToplevelListener{
		Configure: func(width, height int32, _ []byte) {
			// A zero dimension means "you decide", which the UI reads as keep
			// what it has. The size only takes effect with the
			// xdg_surface.configure that ends the sequence, so a compositor
			// may send several of these and the last one is the one that
			// counts.
			w.pendingW, w.pendingH = width, height
		},
		Close: func() {
			// The compositor asked for the window to go. Closing the
			// connection here, on the Wayland goroutine, is what ends the
			// loop and with it Run.
			w.conn.Close()
		},
	})

	xdgSurface.SetListener(xdgshell.SurfaceListener{
		Configure: func(serial uint32) {
			// Nothing may be attached before ack_configure — see wlcore.md.
			// The ack is a request, so it happens here, on the Wayland
			// goroutine, before the UI hears of the new size and can paint a
			// buffer for it.
			if err := xdgSurface.AckConfigure(serial); err != nil {
				log.Printf("window: ack_configure: %v", err)
				return
			}
			w.ui.Push(eventloop.Event{Kind: eventloop.EvConfigure, Width: w.pendingW, Height: w.pendingH})
		},
	})

	// The commit with no buffer is what makes the compositor send the first
	// xdg_surface.configure, and with it open the window.
	if err := w.surface.Commit(); err != nil {
		return fmt.Errorf("window: initial commit: %w", err)
	}
	return nil
}

// bindSeat builds the keyboard and the pointer over seat and wires them to
// the UI. It runs inside the registry's global event because
// wl_seat.capabilities arrives in the same batch as the bind: both devices
// and the seat's own listener have to be live now or the first event is lost.
//
// The callbacks below run on the Wayland goroutine, inside the dispatch.
// None of them touches the UI's state: each turns what it was given into an
// event and pushes it.
func (w *Window) bindSeat(seat *wlcore.Seat) error {
	kbd, err := keyboard.New(w.conn, seat)
	if err != nil {
		return err
	}
	w.kbd = kbd
	kbd.OnKey = func(ev keyboard.Event) {
		w.ui.Push(eventloop.Event{Kind: eventloop.EvKey, Key: ev})
	}
	kbd.OnFocus = func(surface *wlcore.Surface) {
		w.ui.Push(eventloop.Event{Kind: eventloop.EvKeyboardFocus, Surface: surface})
	}
	kbd.OnError = func(err error) { log.Printf("window: keyboard: %v", err) }

	ptr, err := pointer.New(w.conn, seat)
	if err != nil {
		return err
	}
	w.ptr = ptr
	ptr.OnEvent = func(ev pointer.Event) {
		w.ui.Push(eventloop.Event{Kind: eventloop.EvPointer, Pointer: ev})
	}
	ptr.OnFocus = func(surface *wlcore.Surface) {
		w.ui.Push(eventloop.Event{Kind: eventloop.EvPointerFocus, Surface: surface})
	}
	ptr.OnError = func(err error) { log.Printf("window: pointer: %v", err) }

	seat.SetListener(wlcore.SeatListener{
		Capabilities: func(capabilities wlcore.SeatCapability) {
			// The seat's listener stays here because the keyboard and the
			// pointer share it: each device is told the capabilities rather
			// than claiming the seat for itself.
			w.kbd.SetCapabilities(capabilities)
			w.ptr.SetCapabilities(capabilities)
		},
	})
	return nil
}

// closeInput releases the input devices, on the Wayland goroutine, once the
// loop has stopped. With the connection already gone the release cannot be
// sent and says so; there is nothing to do about it and nothing that depends
// on it.
func (w *Window) closeInput() {
	if w.kbd != nil {
		if err := w.kbd.Close(); err != nil && w.conn.Err() == nil {
			log.Printf("window: release keyboard: %v", err)
		}
		w.kbd = nil
	}
	if w.ptr != nil {
		if err := w.ptr.Close(); err != nil && w.conn.Err() == nil {
			log.Printf("window: release pointer: %v", err)
		}
		w.ptr = nil
	}
}
