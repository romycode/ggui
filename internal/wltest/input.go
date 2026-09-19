package wltest

import (
	_ "embed"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/wlcore"
)

// keymapSource is a real XKB v1 keymap, the same fixture keyboard's own
// tests compile: three groups (us, es(deadtilde), us) captured from a live
// compositor. It is embedded rather than read from disk because wltest is
// imported from other packages' tests, whose working directory is their
// own.
//
//go:embed testdata/live-multigroup.xkb
var keymapSource string

// repeatRate and repeatDelay are what wl_keyboard.repeat_info announces:
// the defaults a desktop ships with.
const (
	repeatRate  = 25
	repeatDelay = 600
)

func (s *Server) handleSeat(a *args, opcode uint16, id uint32) {
	switch opcode {
	case reqSeatGetPointer:
		newID := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[newID] = "wl_pointer"
		s.pointerID = newID
	case reqSeatGetKeyboard:
		newID := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[newID] = "wl_keyboard"
		s.keyboard = newID
		s.sendKeymap(newID)
		s.send(newID, evtKeyboardRepeatInfo, repeatRate, repeatDelay)
	case reqSeatGetTouch:
		newID := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[newID] = "wl_touch"
	case reqSeatRelease:
		if s.seat == id {
			s.seat = 0
		}
		s.deleteID(id)
	}
}

func (s *Server) releaseKeyboard(id uint32) {
	if s.keyboard == id {
		s.keyboard, s.keyboardFocus = 0, false
	}
	s.deleteID(id)
}

func (s *Server) releasePointer(id uint32) {
	if s.pointerID == id {
		s.pointerID, s.pointerFocus = 0, false
	}
	s.deleteID(id)
}

// sendKeymap hands the client the keymap over SCM_RIGHTS, as a sealed
// memfd whose size counts the trailing nul, which is what the protocol
// promises and what keyboard.Keyboard relies on when it maps it.
func (s *Server) sendKeymap(keyboardID uint32) {
	if s.keymapFD < 0 {
		fd, size, err := newKeymapFD()
		if err != nil {
			s.errorf("building the keymap memfd: %v", err)
			return
		}
		s.keymapFD, s.keymapSize = fd, size
	}
	e := wlcore.NewEncoder().Uint32(keymapFormatXkbV1).Uint32(s.keymapSize)
	s.sendEncoded(keyboardID, evtKeyboardKeymap, e, s.keymapFD)
}

// newKeymapFD writes the embedded keymap into a sealed memfd. The fd is
// kept for the Server's lifetime: SCM_RIGHTS hands the client a
// duplicate, so the same one can be sent again.
func newKeymapFD() (fd int, size uint32, err error) {
	fd, err = unix.MemfdCreate("wltest-keymap", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, 0, err
	}
	data := append([]byte(keymapSource), 0)
	if _, err := unix.Pwrite(fd, data, 0); err != nil {
		unix.Close(fd)
		return -1, 0, err
	}
	// Sealing is what lets a client map it read-only without trusting us
	// not to shrink it underneath the mapping.
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS,
		unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_WRITE|unix.F_SEAL_SEAL); err != nil {
		unix.Close(fd)
		return -1, 0, err
	}
	return fd, uint32(len(data)), nil
}

// FocusKeyboard gives the window the keyboard focus, or takes it away. The
// injection methods focus the window by themselves the first time, so a
// test only needs this to test focus itself.
func (s *Server) FocusKeyboard(focused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focusKeyboard(focused)
}

func (s *Server) focusKeyboard(focused bool) {
	if s.keyboard == 0 {
		s.errorf("keyboard focus with no wl_keyboard")
		return
	}
	if s.surface == 0 {
		s.errorf("keyboard focus with no wl_surface")
		return
	}
	if focused == s.keyboardFocus {
		return
	}
	s.keyboardFocus = focused
	if !focused {
		s.send(s.keyboard, evtKeyboardLeave, s.nextSerial(), s.surface)
		return
	}
	// enter carries the keys already held, which is none of them here.
	e := wlcore.NewEncoder().Uint32(s.nextSerial()).ID(s.surface).Array(nil)
	s.sendEncoded(s.keyboard, evtKeyboardEnter, e, -1)
	s.send(s.keyboard, evtKeyboardModifiers, s.nextSerial(), 0, 0, 0, 0)
}

// Modifiers sends wl_keyboard.modifiers with the given masks, so a test
// can hold Shift or lock Caps without inventing a keymap.
func (s *Server) Modifiers(depressed, latched, locked, group uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keyboard == 0 {
		s.errorf("Modifiers with no wl_keyboard")
		return
	}
	s.send(s.keyboard, evtKeyboardModifiers, s.nextSerial(), depressed, latched, locked, group)
}

// Key presses or releases one key, by its Linux evdev code (KEY_A is 30).
// The window is given the keyboard focus first if it does not have it.
func (s *Server) Key(evdev uint32, pressed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keyboard == 0 {
		s.errorf("Key with no wl_keyboard")
		return
	}
	if !s.keyboardFocus {
		s.focusKeyboard(true)
	}
	state := uint32(keyStateReleased)
	if pressed {
		state = keyStatePressed
	}
	s.send(s.keyboard, evtKeyboardKey, s.nextSerial(), s.nowMS(), evdev, state)
}

// FocusPointer puts the pointer inside the window, or takes it out. Like
// the keyboard, the injection methods do it by themselves the first time.
func (s *Server) FocusPointer(focused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focusPointer(focused)
}

func (s *Server) focusPointer(focused bool) {
	if s.pointerID == 0 {
		s.errorf("pointer focus with no wl_pointer")
		return
	}
	if s.surface == 0 {
		s.errorf("pointer focus with no wl_surface")
		return
	}
	if focused == s.pointerFocus {
		return
	}
	s.pointerFocus = focused
	if !focused {
		s.send(s.pointerID, evtPointerLeave, s.nextSerial(), s.surface)
		s.pointerFrame()
		return
	}
	e := wlcore.NewEncoder().Uint32(s.nextSerial()).ID(s.surface).
		Fixed(wlcore.FixedFromFloat64(s.pointerX)).
		Fixed(wlcore.FixedFromFloat64(s.pointerY))
	s.sendEncoded(s.pointerID, evtPointerEnter, e, -1)
	s.pointerFrame()
}

// pointerFrame ends a pointer event group. wl_pointer.frame only exists
// from version 5 on, and the fake never sends an event the negotiated
// version does not have.
func (s *Server) pointerFrame() {
	if s.seatVersion >= 5 {
		s.send(s.pointerID, evtPointerFrame)
	}
}

// PointerMotion moves the pointer to a surface-local position in logical
// units. The window is given the pointer focus first if it does not have
// it.
func (s *Server) PointerMotion(x, y float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pointerID == 0 {
		s.errorf("PointerMotion with no wl_pointer")
		return
	}
	s.pointerX, s.pointerY = x, y
	if !s.pointerFocus {
		s.focusPointer(true)
		return // the enter already carried the position
	}
	e := wlcore.NewEncoder().Uint32(s.nowMS()).
		Fixed(wlcore.FixedFromFloat64(x)).
		Fixed(wlcore.FixedFromFloat64(y))
	s.sendEncoded(s.pointerID, evtPointerMotion, e, -1)
	s.pointerFrame()
}

// PointerButton presses or releases a pointer button, by its Linux evdev
// code (BTN_LEFT is 0x110).
func (s *Server) PointerButton(button uint32, pressed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pointerID == 0 {
		s.errorf("PointerButton with no wl_pointer")
		return
	}
	if !s.pointerFocus {
		s.focusPointer(true)
	}
	state := uint32(keyStateReleased)
	if pressed {
		state = keyStatePressed
	}
	s.send(s.pointerID, evtPointerButton, s.nextSerial(), s.nowMS(), button, state)
	s.pointerFrame()
}
