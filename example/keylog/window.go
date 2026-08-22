// Command keylog opens a Wayland window and logs every key it receives to
// the console: the keycode in both encodings, the resolved keysym, the text
// the key produced after dead-key composition, and the modifier state split
// into effective and consumed.
//
// The window is not decorative. Wayland only delivers wl_keyboard.enter to a
// surface the compositor has given focus to, so there has to be a mapped
// surface to focus — click the window and start typing.
//
// This wires wl_keyboard to the keyboard package by hand because the
// Keyboard lifecycle type described in docs/keyboard.md does not exist yet.
// Key repeat is deliberately left out: the repeat timer belongs in that
// pending layer, not in an example.
//
// The interesting column is `consumed`. If the key's type spent Shift
// choosing the level, that Shift is not part of a shortcut, and the match a
// real app wants is Effective &^ Consumed — otherwise Shift+2 never matches
// "at" and Ctrl+Shift+X behaves differently across layouts.
package main

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

const (
	defaultWidth  = 480
	defaultHeight = 240
	bytesPerPixel = 4
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// window holds the keyboard state the listener needs across events: the
// compiled keymap, the modifier/group state it feeds, and the composer that
// turns dead-key sequences into text.
type window struct {
	width, height int32

	capabilities wlcore.SeatCapability
	keyboard     *wlcore.Keyboard

	keymap   *keyboard.Keymap
	state    *keyboard.State
	composer keyboard.Composer
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

	w := &window{width: defaultWidth, height: defaultHeight}

	var (
		compositor *wlcore.Compositor
		shm        *wlcore.Shm
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
				shm, err = reg.Bind(name, version, wlcore.ShmInterface)
			case xdgshell.WmBaseInterface.Name:
				wmBase, err = reg.Bind(name, version, xdgshell.WmBaseInterface)
			case wlcore.SeatInterface.Name:
				seat, err = reg.Bind(name, version, wlcore.SeatInterface)
				if seat != nil {
					// wl_seat.capabilities arrives right after bind, in the
					// same batch this Global callback runs in, so the
					// listener has to be live now or the first event is lost.
					seat.SetListener(wlcore.SeatListener{
						Capabilities: func(capabilities wlcore.SeatCapability) {
							w.capabilities = capabilities
							w.syncKeyboard(seat)
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
	if compositor == nil || shm == nil || wmBase == nil || seat == nil {
		return errors.New("compositor is missing wl_compositor, wl_shm, xdg_wm_base or wl_seat")
	}

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

	surface, err := compositor.CreateSurface()
	if err != nil {
		return fmt.Errorf("create_surface: %w", err)
	}

	xdgSurface, err := wmBase.GetXdgSurface(surface)
	if err != nil {
		return fmt.Errorf("get_xdg_surface: %w", err)
	}

	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return fmt.Errorf("get_toplevel: %w", err)
	}
	if err := toplevel.SetTitle("ggui keylog — focus me and type"); err != nil {
		return fmt.Errorf("set_title: %w", err)
	}
	if err := toplevel.SetAppID("ggui.example.keylog"); err != nil {
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
			if err := w.redraw(shm, surface); err != nil {
				log.Printf("redraw: %v", err)
			}
		},
	})

	// Initial commit without a buffer: this is what makes the compositor
	// send the first xdg_surface.configure.
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}

	log.Printf("keylog: focus the window and type; Ctrl-C to quit")

	if err := conn.Run(); err != nil && !errors.Is(err, wlcore.ErrClosed) {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}

// syncKeyboard follows the seat's keyboard capability in both directions.
// The capability can disappear while running — unplug a USB keyboard — and
// the object has to be released and the state dropped when it does.
func (w *window) syncKeyboard(seat *wlcore.Seat) {
	has := w.capabilities.Has(wlcore.SeatCapabilityKeyboard)

	switch {
	case has && w.keyboard == nil:
		kbd, err := seat.GetKeyboard()
		if err != nil {
			log.Printf("get_keyboard: %v", err)
			return
		}
		w.keyboard = kbd
		w.arm(kbd)
	case !has && w.keyboard != nil:
		if err := w.keyboard.Release(); err != nil {
			log.Printf("keyboard release: %v", err)
		}
		w.keyboard = nil
		w.keymap, w.state = nil, nil
		w.composer.Reset()
	}
}

func (w *window) arm(kbd *wlcore.Keyboard) {
	kbd.SetListener(wlcore.KeyboardListener{
		Keymap: func(format wlcore.KeyboardKeymapFormat, fd int, size uint32) {
			w.loadKeymap(format, fd, size)
		},

		Enter: func(_ uint32, _ *wlcore.Surface, keys []byte) {
			// keys holds the keycodes already physically down — typically the
			// shortcut that raised the window. They are seeded, not replayed:
			// emitting press events for them would type characters the user
			// never pressed while focused here.
			log.Printf("focus  gained  (%d key(s) already down)", len(keys)/4)
		},

		Leave: func(uint32, *wlcore.Surface) {
			// A half-typed accent must not survive a focus change, or the
			// next window's first letter silently absorbs it.
			w.composer.Reset()
			log.Printf("focus  lost")
		},

		Modifiers: func(_ uint32, depressed, latched, locked, group uint32) {
			if w.state == nil {
				return
			}
			w.state.UpdateMask(depressed, latched, locked, group)
		},

		Key: func(_ uint32, _ uint32, key uint32, keyState wlcore.KeyboardKeyState) {
			w.logKey(key, keyState)
		},

		RepeatInfo: func(rate, delay int32) {
			// Repeat is the client's job, and this example does not implement
			// it — log the parameters so it is visible that they arrived.
			log.Printf("repeat_info  rate=%d/s delay=%dms (not implemented here)", rate, delay)
		},
	})
}

// loadKeymap maps the keymap fd and compiles it. The fd is closed on every
// path, including the ones that reject it: a compositor that re-sends the
// keymap on each layout change would otherwise exhaust our descriptors over
// a long session.
func (w *window) loadKeymap(format wlcore.KeyboardKeymapFormat, fd int, size uint32) {
	defer unix.Close(fd)

	if format != wlcore.KeyboardKeymapFormatXkbV1 {
		log.Printf("keymap: unsupported format %d, ignoring", format)
		return
	}
	if size == 0 {
		log.Printf("keymap: empty")
		return
	}

	// MAP_PRIVATE is required from wl_keyboard version 7 on; MAP_SHARED may
	// fail outright.
	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		log.Printf("keymap mmap: %v", err)
		return
	}
	defer func() {
		if err := unix.Munmap(data); err != nil {
			log.Printf("keymap munmap: %v", err)
		}
	}()

	// size counts the trailing NUL, which is not part of the keymap text.
	km, err := keyboard.Compile(string(data[:size-1]))
	if err != nil {
		log.Printf("keymap compile: %v", err)
		return
	}

	w.keymap = km
	w.state = km.NewState()
	w.composer.Reset()
	log.Printf("keymap loaded (%d bytes)", size-1)
}

// logKey prints one line per key event. Everything it needs is already in
// the keymap and state; nothing here is Wayland-specific except the +8.
func (w *window) logKey(evdev uint32, keyState wlcore.KeyboardKeyState) {
	if w.state == nil {
		log.Printf("key %-8s evdev=%-3d (no keymap yet)", keyStateName(keyState), evdev)
		return
	}

	// wl_keyboard.key carries evdev keycodes; XKB keycodes are evdev+8.
	xkb := evdev + 8
	sym := w.state.Sym(xkb)

	// Text only makes sense for a press, and modifier keysyms must never
	// reach the composer: feeding Shift_L to it would cancel a pending
	// accent, so typing an accented capital would drop the accent.
	text := ""
	if keyState != wlcore.KeyboardKeyStateReleased && !isModifierSym(sym) {
		text = w.composer.Feed(sym)
	}

	effective := w.state.Effective()
	consumed := w.state.Consumed(xkb)

	log.Printf("key %-8s evdev=%-3d xkb=%-3d sym=%-24s rune=%-12s text=%-8q mods=%-16s consumed=%s",
		keyStateName(keyState), evdev, xkb, symLabel(sym), symRune(sym),
		text, modNames(effective), modNames(consumed))
}

func keyStateName(s wlcore.KeyboardKeyState) string {
	switch s {
	case wlcore.KeyboardKeyStatePressed:
		return "press"
	case wlcore.KeyboardKeyStateReleased:
		return "release"
	case wlcore.KeyboardKeyStateRepeated:
		return "repeat"
	}
	return "?"
}

func symLabel(k keyboard.Keysym) string {
	return fmt.Sprintf("%s(%#06x)", k.Name(), uint32(k))
}

// symRune renders the keysym's character for the log, or a placeholder when
// it has none (arrows, F-keys, modifiers). %q escapes the control codes, so
// Return and Tab stay on one line.
func symRune(k keyboard.Keysym) string {
	r := k.Rune()
	if r < 0 {
		return "-"
	}
	return fmt.Sprintf("%q", r)
}

// isModifierSym reports whether the keysym is one of the modifier keys in
// 0xFFE1-0xFFEE (Shift_L through Hyper_R).
func isModifierSym(k keyboard.Keysym) bool {
	return k >= 0xffe1 && k <= 0xffee
}

// modNames renders a real-modifier mask the way the docs name the bits.
// The trailing names are the conventional xkeyboard-config meanings, not a
// guarantee: Mod4 is usually Super but nothing forces it.
func modNames(mask uint32) string {
	if mask == 0 {
		return "-"
	}
	names := []struct {
		bit  uint32
		name string
	}{
		{keyboard.ModShift, "Shift"},
		{keyboard.ModLock, "Lock"},
		{keyboard.ModCtrl, "Ctrl"},
		{keyboard.ModMod1, "Mod1"},
		{keyboard.ModMod2, "Mod2"},
		{keyboard.ModMod3, "Mod3"},
		{keyboard.ModMod4, "Mod4"},
		{keyboard.ModMod5, "Mod5"},
	}
	var out []string
	for _, n := range names {
		if mask&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	return strings.Join(out, "|")
}

func (w *window) redraw(shm *wlcore.Shm, surface *wlcore.Surface) error {
	buf, err := blankBuffer(shm, w.width, w.height)
	if err != nil {
		return err
	}
	if err := surface.Attach(buf, 0, 0); err != nil {
		return err
	}
	if err := surface.DamageBuffer(0, 0, w.width, w.height); err != nil {
		return err
	}
	return surface.Commit()
}

func blankBuffer(shm *wlcore.Shm, width, height int32) (*wlcore.Buffer, error) {
	stride := width * bytesPerPixel
	size := int64(stride) * int64(height)

	fd, err := unix.MemfdCreate("ggui-keylog", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("memfd_create: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Ftruncate(fd, size); err != nil {
		return nil, fmt.Errorf("ftruncate: %w", err)
	}

	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	// xrgb8888, little-endian: byte order per pixel is B, G, R, x.
	for i := 0; i < len(data); i += bytesPerPixel {
		data[i+0] = 0x2c
		data[i+1] = 0x28
		data[i+2] = 0x24
		data[i+3] = 0x00
	}
	if err := unix.Munmap(data); err != nil {
		return nil, fmt.Errorf("munmap: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		return nil, fmt.Errorf("fcntl seal: %w", err)
	}

	pool, err := shm.CreatePool(fd, int32(size))
	if err != nil {
		return nil, fmt.Errorf("create_pool: %w", err)
	}
	defer pool.Destroy()

	return pool.CreateBuffer(0, width, height, stride, wlcore.ShmFormatXrgb8888)
}
