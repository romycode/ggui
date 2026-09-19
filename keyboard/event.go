package keyboard

// KeyState is what happened to a key.
type KeyState uint8

const (
	// Pressed is a key going down.
	Pressed KeyState = iota
	// Released is a key coming up.
	Released
	// Repeated is the same key firing again while held. It is synthesized
	// by [Keyboard.Tick], or delivered by a compositor that took repeat
	// over; both arrive here, so a caller never has to tell them apart.
	Repeated
)

// String returns the state's name, for logs and test failures.
func (s KeyState) String() string {
	switch s {
	case Pressed:
		return "pressed"
	case Released:
		return "released"
	case Repeated:
		return "repeated"
	}
	return "unknown"
}

// Mods is the modifier state at the moment of an event.
//
// Effective is the three masks OR-ed, which is what a shortcut compares
// against — but only after subtracting Consumed. A keymap that reaches its
// level through Shift has spent that Shift choosing the character, so
// Ctrl+Shift+1 giving "!" must not also match a Ctrl+Shift+1 accelerator.
// Comparing against the raw mask instead is the classic shortcut bug.
type Mods struct {
	// Depressed are the modifiers physically held.
	Depressed uint32
	// Latched are the modifiers armed for one key, as sticky keys does.
	Latched uint32
	// Locked are the modifiers toggled on, as Caps Lock does.
	Locked uint32
	// Effective is Depressed, Latched and Locked OR-ed together.
	Effective uint32
	// Consumed are the modifiers the key's type spent choosing the level,
	// which a shortcut has to subtract from Effective.
	Consumed uint32
}

// Unconsumed is Effective with Consumed removed: the mask a shortcut
// compares against.
//
//	if ev.Sym == SymX && ev.Unconsumed() == ModCtrl { … }
func (m Mods) Unconsumed() uint32 { return m.Effective &^ m.Consumed }

// Event is one key press, release or repeat, with the keysym resolved and
// the text already composed.
type Event struct {
	// State is what happened to the key.
	State KeyState
	// Keycode is the XKB keycode, which is Evdev plus 8. It is what the
	// keymap is indexed by.
	Keycode uint32
	// Evdev is the raw kernel keycode the compositor sent, for a shortcut
	// that means a physical position rather than a symbol.
	Evdev uint32
	// Sym is the keysym the keymap resolved for this keycode and these
	// modifiers.
	Sym Keysym
	// Text is what the key types, with dead keys already applied: empty
	// for a release, for a key that produces no text, and for the press
	// that only arms a dead key. Control characters never appear here —
	// Return and Tab resolve to "\r" and "\t" through the legacy keysym
	// table, which is a trap every caller would otherwise have to know
	// about.
	Text string
	// Mods is the modifier state when the key was pressed.
	Mods Mods
	// Serial is the event's serial, which any request that has to be tied
	// to a user interaction needs — a selection, a popup grab. A repeat
	// carries the serial of the press that started it, since no new one
	// arrived.
	Serial uint32
	// Time is the compositor's millisecond timestamp, with the same
	// caveat as Serial for a repeat.
	Time uint32
}
