package keyboard

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/wlcore"
)

// Keyboard turns a seat's wl_keyboard into [Event]s: it owns the keymap,
// the modifier state, the dead-key composer, the surface focus and the
// repeat timer, so an application does none of that itself.
//
// # Callbacks, not a channel
//
// Events arrive through OnKey, called from inside the dispatch that
// produced them. That is not a stylistic choice: the whole wlcore runtime
// is lock-free on the promise that one goroutine touches Conn, and a
// handler that wants to answer a key by sending a request has to run on
// that goroutine. A channel read by another one would hand the caller
// events it cannot act on.
//
// # The seat is not ours
//
// A Keyboard does not take wl_seat's listener. A seat also carries the
// pointer and touch capabilities, and whoever wants those needs the same
// listener; a Keyboard that claimed it would lock them out. The caller
// keeps its own seat listener and passes the capabilities on with
// [Keyboard.SetCapabilities], which is what attaches and releases the
// underlying wl_keyboard as the device comes and goes.
//
// # Repeat
//
// Repeat is the client's job. A Keyboard tracks when the next one is due
// and reports it from [Keyboard.NextRepeat]; the caller passes that to
// [wlcore.Conn.DispatchUntil] and calls [Keyboard.Tick] when it returns.
// Nothing is repeated by a timer goroutine, for the same reason there is
// no channel.
//
// Not safe for concurrent use, like everything else here.
type Keyboard struct {
	conn *wlcore.Conn
	seat *wlcore.Seat
	wl   *wlcore.Keyboard

	keymap   *Keymap
	state    *State
	composer Composer
	focus    *wlcore.Surface

	// OnKey receives every press, release and repeat. Nil ignores them.
	OnKey func(Event)
	// OnFocus is called when the keyboard focus enters a surface or
	// leaves it, in which case the surface is nil. Nil ignores it.
	OnFocus func(surface *wlcore.Surface)
	// OnError reports a keymap that could not be loaded and any failure
	// releasing the device. These are not fatal — the connection is fine,
	// there is simply no keymap — so they are reported rather than
	// returned. Nil discards them.
	OnError func(err error)
	// OnKeymap receives the keymap's source text, before it is compiled
	// and while the mapping is still alive. It exists so a client can dump
	// what the compositor actually sent: the tests only ever compile
	// synthetic layouts, so a keymap that misbehaves in a real session
	// cannot be reproduced from them and has to be captured here. Nil
	// ignores it.
	OnKeymap func(src string)

	// rate is repeats per second, 0 meaning the compositor asked for no
	// repeat at all; delay is the wait before the first one.
	rate  int32
	delay time.Duration

	// held is the evdev keycode currently repeating, 0 for none, with the
	// serial and time of the press that started it: a repeat invents
	// neither, since no new event arrived to carry them.
	held       uint32
	heldSerial uint32
	heldTime   uint32
	// next is when the next repeat is due, zero when nothing repeats.
	next time.Time
}

// New returns a Keyboard for the given seat. Nothing is attached yet: the
// caller drives that from its own wl_seat listener with
// [Keyboard.SetCapabilities], which is also what handles the device being
// unplugged mid-session.
func New(conn *wlcore.Conn, seat *wlcore.Seat) (*Keyboard, error) {
	if conn == nil || seat == nil {
		return nil, errors.New("keyboard: nil conn or seat")
	}
	return &Keyboard{conn: conn, seat: seat}, nil
}

// SetCapabilities follows the seat's capabilities in both directions: it
// takes the wl_keyboard when the seat gains one and releases it when the
// seat loses one, which happens for real when a USB keyboard is unplugged.
//
// Call it from wl_seat.capabilities, and again whenever it fires: the
// capability can come back.
func (k *Keyboard) SetCapabilities(caps wlcore.SeatCapability) {
	has := caps.Has(wlcore.SeatCapabilityKeyboard)
	switch {
	case has && k.wl == nil:
		wl, err := k.seat.GetKeyboard()
		if err != nil {
			k.fail(fmt.Errorf("keyboard: get_keyboard: %w", err))
			return
		}
		k.wl = wl
		k.listen(wl)
	case !has && k.wl != nil:
		k.detach()
	}
}

// Focus returns the surface holding the keyboard focus, or nil.
func (k *Keyboard) Focus() *wlcore.Surface { return k.focus }

// Keymap returns the compiled keymap, or nil before one has arrived. A
// caller needs it only to ask questions this package does not answer for
// it, such as whether a keycode is a modifier.
func (k *Keyboard) Keymap() *Keymap { return k.keymap }

// Close releases the wl_keyboard. The Keyboard must not be used
// afterwards.
func (k *Keyboard) Close() error {
	if k.wl == nil {
		return nil
	}
	err := k.wl.Release()
	k.wl = nil
	k.reset()
	return err
}

// detach drops the device and everything derived from it. The keymap goes
// too: the next one may be for a different layout entirely.
func (k *Keyboard) detach() {
	if err := k.wl.Release(); err != nil {
		k.fail(fmt.Errorf("keyboard: release: %w", err))
	}
	k.wl = nil
	k.keymap, k.state = nil, nil
	k.reset()
}

// reset drops the per-focus state: what is held, what a dead key is
// waiting for, and any repeat in flight.
func (k *Keyboard) reset() {
	k.composer.Reset()
	k.stopRepeat()
	k.focus = nil
}

func (k *Keyboard) fail(err error) {
	if k.OnError != nil {
		k.OnError(err)
	}
}

func (k *Keyboard) listen(wl *wlcore.Keyboard) {
	wl.SetListener(wlcore.KeyboardListener{
		Keymap: func(format wlcore.KeyboardKeymapFormat, fd int, size uint32) {
			k.loadKeymap(format, fd, size)
		},
		Enter: func(_ uint32, surface *wlcore.Surface, _ []byte) {
			k.enter(surface)
		},
		Leave: func(uint32, *wlcore.Surface) {
			k.leave()
		},
		Modifiers: func(_ uint32, depressed, latched, locked, group uint32) {
			if k.state != nil {
				k.state.UpdateMask(depressed, latched, locked, group)
			}
		},
		Key: func(serial, time, key uint32, state wlcore.KeyboardKeyState) {
			k.key(serial, time, key, state)
		},
		RepeatInfo: func(rate, delay int32) {
			k.setRepeatInfo(rate, delay)
		},
	})
}

// loadKeymap maps the keymap fd and compiles it. The fd is closed on every
// path, including the ones that reject it: a compositor that re-sends the
// keymap on each layout change would otherwise exhaust our descriptors
// over a long session.
func (k *Keyboard) loadKeymap(format wlcore.KeyboardKeymapFormat, fd int, size uint32) {
	defer unix.Close(fd)

	if format != wlcore.KeyboardKeymapFormatXkbV1 {
		k.fail(fmt.Errorf("keyboard: unsupported keymap format %d", format))
		return
	}
	if size == 0 {
		k.fail(errors.New("keyboard: empty keymap"))
		return
	}

	// MAP_PRIVATE is required from wl_keyboard version 7 on; MAP_SHARED
	// may fail outright.
	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		k.fail(fmt.Errorf("keyboard: keymap mmap: %w", err))
		return
	}
	defer func() {
		if err := unix.Munmap(data); err != nil {
			k.fail(fmt.Errorf("keyboard: keymap munmap: %w", err))
		}
	}()

	// size counts the trailing NUL, which is not part of the keymap text.
	src := string(data[:size-1])
	if k.OnKeymap != nil {
		k.OnKeymap(src)
	}

	km, err := Compile(src)
	if err != nil {
		k.fail(fmt.Errorf("keyboard: keymap compile: %w", err))
		return
	}

	// A new keymap invalidates the modifier state built against the old
	// one, and a half-typed accent with it.
	k.keymap, k.state = km, km.NewState()
	k.composer.Reset()
	k.stopRepeat()
}

// enter takes the focus.
//
// The event carries the keycodes already physically down, and they are
// deliberately dropped. Reporting them as presses would type the shortcut
// that raised the window into whatever is focused; and there is nothing to
// seed them into either, because the state this package keeps is the
// modifier masks, which arrive whole in their own event rather than being
// accumulated from individual keys.
func (k *Keyboard) enter(surface *wlcore.Surface) {
	k.focus = surface
	k.composer.Reset()
	k.stopRepeat()
	if k.OnFocus != nil {
		k.OnFocus(surface)
	}
}

// leave drops everything tied to having the focus. A release for a key
// held at this moment will be delivered to whoever gets the focus next, so
// a repeat kept running here would never be stopped by anything.
func (k *Keyboard) leave() {
	k.focus = nil
	k.composer.Reset()
	k.stopRepeat()
	if k.OnFocus != nil {
		k.OnFocus(nil)
	}
}

func (k *Keyboard) key(serial, tms, evdev uint32, state wlcore.KeyboardKeyState) {
	if k.state == nil {
		return
	}

	switch state {
	case wlcore.KeyboardKeyStateReleased:
		if k.held == evdev {
			k.stopRepeat()
		}
		k.emit(Released, serial, tms, evdev)
	case wlcore.KeyboardKeyStateRepeated:
		// Since wl_keyboard v10 the compositor may run repeat itself.
		// Normalizing it to the same state the timer produces is what
		// keeps a caller from having to know which path it is on.
		k.emit(Repeated, serial, tms, evdev)
	default:
		k.emit(Pressed, serial, tms, evdev)
		k.startRepeat(serial, tms, evdev)
	}
}

// emit resolves the keysym and the text for one key and hands it over.
func (k *Keyboard) emit(state KeyState, serial, tms, evdev uint32) {
	xkb := evdev + 8
	ev := Event{
		State:   state,
		Keycode: xkb,
		Evdev:   evdev,
		Sym:     k.state.Sym(xkb),
		Mods: Mods{
			Depressed: k.state.depressed,
			Latched:   k.state.latched,
			Locked:    k.state.locked,
			Effective: k.state.Effective(),
			Consumed:  k.state.Consumed(xkb),
		},
		Serial: serial,
		Time:   tms,
	}
	if state != Released {
		ev.Text = k.textFor(xkb, ev.Sym)
	}
	if k.OnKey != nil {
		k.OnKey(ev)
	}
}

// textFor composes the text a key types, or returns "" if it types none.
//
// A modifier must never reach the composer: feeding it one cancels the
// pending dead key, so the accent is discarded. The keymap is asked rather
// than the keysym tested, because AltGr arrives as ISO_Level3_Shift
// (0xfe03), nowhere near the 0xffe1-0xffee block holding Shift, Control,
// Alt and Super.
func (k *Keyboard) textFor(xkb uint32, sym Keysym) string {
	if k.keymap.IsModifierKey(xkb) {
		return ""
	}

	out := k.composer.Feed(sym)
	// Control characters are dropped here rather than in every caller:
	// Keysym.Rune maps Return to '\r' and Tab to '\t' through the legacy
	// table, so both would otherwise arrive as ordinary text and be stored
	// and drawn as U+FFFD. A caller that wants Return acts on the keysym.
	for _, r := range out {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return out
}

// setRepeatInfo reconfigures the timer. It can arrive at any time, not
// only during setup, and a new one abandons a repeat already in flight
// rather than letting it run at the old rate.
func (k *Keyboard) setRepeatInfo(rate, delay int32) {
	k.rate = max(rate, 0)
	k.delay = time.Duration(max(delay, 0)) * time.Millisecond
	k.stopRepeat()
}

// startRepeat arms the timer for a key that was just pressed, if it is a
// key that repeats at all. Modifiers typically are not: xkb_symbols says
// so with repeat=no, which the keymap already resolved.
func (k *Keyboard) startRepeat(serial, tms, evdev uint32) {
	if k.rate <= 0 || !k.state.Repeats(evdev+8) {
		k.stopRepeat()
		return
	}
	k.held, k.heldSerial, k.heldTime = evdev, serial, tms
	k.next = time.Now().Add(k.delay)
}

func (k *Keyboard) stopRepeat() {
	k.held, k.heldSerial, k.heldTime = 0, 0, 0
	k.next = time.Time{}
}

// RepeatInfo returns the repeat settings the compositor asked for: how
// many repeats a second, and how long a key is held before the first one.
// A rate of 0 means it does not want the client repeating at all.
//
// They are the user's preference, arriving over wl_keyboard.repeat_info,
// and this package honours them rather than picking its own. A client that
// finds the first repeat slow to arrive is looking at a desktop setting,
// not at this code. Both are zero until repeat_info has arrived.
func (k *Keyboard) RepeatInfo() (rate int32, delay time.Duration) {
	return k.rate, k.delay
}

// NextRepeat returns when the next repeat is due, or the zero time if
// nothing is repeating. It is meant to be handed straight to
// [wlcore.Conn.DispatchUntil], whose zero deadline means "no deadline" —
// so a loop needs no branch for the common case of no key held.
func (k *Keyboard) NextRepeat() time.Time { return k.next }

// Tick emits a repeat if one is due at now, and reports whether it did.
// Calling it when nothing is due is free, so a loop can call it after
// every dispatch without checking first.
//
// The next one is scheduled from when this one was *due*, not from now, so
// that the work the caller does between ticks — repainting, committing a
// frame — does not get added to the period. Scheduling from now instead
// stretches every interval by however long the loop took, which drifts the
// rate below what the compositor asked for and makes it uneven, since that
// work is never the same length twice.
//
// A loop that fell more than one interval behind cannot catch up that way
// without firing a burst, so there the cadence resynchronizes to now and
// the missed repeats are dropped. Dropping them is the right end of that
// trade: they would arrive as a rush of characters nobody typed.
func (k *Keyboard) Tick(now time.Time) bool {
	if k.held == 0 || k.next.IsZero() || now.Before(k.next) {
		return false
	}
	interval := time.Second / time.Duration(k.rate)
	if k.next = k.next.Add(interval); k.next.Before(now) {
		k.next = now.Add(interval)
	}

	// Held while the state changed underneath: emit reads the modifiers
	// as they are now, which is what a user holding a key and pressing
	// Shift expects.
	k.emit(Repeated, k.heldSerial, k.heldTime, k.held)
	return true
}
