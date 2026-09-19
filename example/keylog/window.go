// Command keylog opens a Wayland window and logs every key it receives to
// the console: the keycode in both encodings, the resolved keysym, the text
// the key produced after dead-key composition, and the modifier state split
// into effective and consumed.
//
// The window is not decorative. Wayland only delivers wl_keyboard.enter to a
// surface the compositor has given focus to, so there has to be a mapped
// surface to focus — click the window and start typing.
//
// Everything below the keysym belongs to keyboard.Keyboard: the seat
// lifecycle, the keymap fd, the modifier state, the dead-key composer and
// the repeat timer. This file is the logging and nothing else — which is
// the point of the layer existing.
//
// The loop is not conn.Run(). A held key is due to repeat at a moment the
// compositor will not announce, so it waits on the earlier of "a message
// arrived" and "the next repeat is due"; see Conn.DispatchUntil.
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
	"os"
	"strings"
	"time"

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

// window is what is left of this example once keyboard.Keyboard owns the
// keymap, the modifier state, the composer, the focus and the repeat timer.
// What remains here is the logging, which is the only part that was ever
// specific to a key logger.
type window struct {
	width, height int32

	kbd *keyboard.Keyboard
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
					// same batch this Global callback runs in, so both the
					// Keyboard and the listener have to be live now or the
					// first event is lost.
					kbd, kerr := keyboard.New(conn, seat)
					if kerr != nil {
						err = kerr
						break
					}
					w.kbd = kbd
					w.kbd.OnKey = logKey
					w.kbd.OnError = func(e error) { log.Printf("keyboard: %v", e) }
					w.kbd.OnKeymap = dumpKeymap
					// Logged from the first focus rather than on arrival:
					// repeat_info lands before anything else, and a line
					// printed then scrolls past before the window is up.
					w.kbd.OnFocus = func(s *wlcore.Surface) {
						logFocus(s)
						if s != nil {
							w.logRepeatInfo()
						}
					}

					seat.SetListener(wlcore.SeatListener{
						Capabilities: func(capabilities wlcore.SeatCapability) {
							// The seat listener stays here rather than
							// inside the Keyboard: a seat also carries the
							// pointer and touch capabilities, and whoever
							// wants those needs this same listener.
							w.kbd.SetCapabilities(capabilities)
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

	// Not conn.Run(): a key repeat is due at a time the compositor is not
	// going to tell us about, so the loop wakes on the earlier of "a
	// message arrived" and "the next repeat is due". NextRepeat returns
	// the zero time when no key is held, which DispatchUntil reads as no
	// deadline at all — so the idle case blocks exactly like Run did.
	for {
		if err := conn.DispatchUntil(w.kbd.NextRepeat()); err != nil {
			if errors.Is(err, wlcore.ErrClosed) {
				return nil
			}
			return fmt.Errorf("dispatch: %w", err)
		}
		w.kbd.Tick(time.Now())
	}
}

// dumpKeymap writes the keymap the compositor actually sent to the path in
// KEYLOG_DUMP_KEYMAP, if it is set. The oracle suite only ever compiles
// synthetic RMLVO layouts, so a keymap that behaves differently in a real
// session cannot be reproduced from the tests alone — it has to be captured
// here.
func dumpKeymap(src string) {
	path := os.Getenv("KEYLOG_DUMP_KEYMAP")
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		log.Printf("keymap dump: %v", err)
		return
	}
	log.Printf("keymap dumped to %s (%d bytes)", path, len(src))
}

// logFocus reports the focus changing. Which surface got it does not
// matter here: this example has exactly one.
func logFocus(surface *wlcore.Surface) {
	if surface == nil {
		log.Printf("focus  lost")
		return
	}
	log.Printf("focus  gained")
}

// logKey prints one line per key event. Everything it needs has already
// been resolved by the keyboard layer: the keycodes, the keysym, the
// composed text and which modifiers the key spent choosing its level.
// logRepeatInfo prints what the compositor asked for, once it has. The
// delay is a desktop setting, not something this code picks, and it is the
// first thing to look at when the first repeat feels slow to arrive.
func (w *window) logRepeatInfo() {
	rate, delay := w.kbd.RepeatInfo()
	if rate == 0 {
		log.Printf("repeat_info  disabled by the compositor")
		return
	}
	log.Printf("repeat_info  rate=%d/s (%v apart) delay=%v", rate, time.Second/time.Duration(rate), delay)
}

func logKey(ev keyboard.Event) {
	log.Printf("key %-8s evdev=%-3d xkb=%-3d sym=%-24s rune=%-12s text=%-8q mods=%-16s consumed=%s",
		ev.State, ev.Evdev, ev.Keycode, symLabel(ev.Sym), symRune(ev.Sym),
		ev.Text, modNames(ev.Mods.Effective), modNames(ev.Mods.Consumed))
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
