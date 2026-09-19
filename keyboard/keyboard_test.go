package keyboard

import (
	"testing"
	"time"

	"github.com/romycode/ggui/wayland/wlcore"
)

// testKeymapSrc is the smallest keymap that exercises everything a
// Keyboard does with one: a letter that repeats, a modifier that does not,
// a dead key, and Return, whose legacy keysym resolves to a control
// character.
const testKeymapSrc = `
xkb_keycodes "t" {
	<AD01> = 24;
	<LFSH> = 50;
	<RTRN> = 36;
	<AD12> = 35;
};
xkb_types "t" {
	type "ALPHABETIC" {
		modifiers= Shift;
		map[Shift]= 2;
	};
};
xkb_symbols "t" {
	key <AD01> { type= "ALPHABETIC", [ q, Q ] };
	key <LFSH> { repeat= no, [ Shift_L ] };
	key <RTRN> { [ Return ] };
	key <AD12> { [ dead_acute ] };
	modifier_map Shift { <LFSH> };
};
`

const (
	evdevQ      = 24 - 8
	evdevShift  = 50 - 8
	evdevReturn = 36 - 8
	evdevAcute  = 35 - 8
)

// newTestKeyboard returns a Keyboard with a compiled keymap and no
// wl_keyboard behind it, plus the events it emits. Everything below the
// protocol is reachable this way; what needs a live compositor is the
// wiring, not the behaviour.
func newTestKeyboard(t *testing.T) (*Keyboard, *[]Event) {
	t.Helper()

	km, err := Compile(testKeymapSrc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var got []Event
	k := &Keyboard{keymap: km, state: km.NewState()}
	k.OnKey = func(ev Event) { got = append(got, ev) }
	return k, &got
}

func TestNewRejectsANilConnOrSeat(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Error("a nil conn and seat were accepted")
	}
}

func TestPressAndReleaseCarryTheKeysymAndTheKeycodes(t *testing.T) {
	k, got := newTestKeyboard(t)

	k.key(7, 100, evdevQ, wlcore.KeyboardKeyStatePressed)
	k.key(8, 120, evdevQ, wlcore.KeyboardKeyStateReleased)

	if len(*got) != 2 {
		t.Fatalf("got %d events, want a press and a release", len(*got))
	}
	press, release := (*got)[0], (*got)[1]

	if press.State != Pressed || release.State != Released {
		t.Fatalf("states are %v and %v, want pressed and released", press.State, release.State)
	}
	// The +8 is the trap this pins: wl_keyboard sends evdev, XKB indexes
	// its keymap eight higher.
	if press.Evdev != evdevQ || press.Keycode != evdevQ+8 {
		t.Errorf("evdev=%d keycode=%d, want %d and %d", press.Evdev, press.Keycode, evdevQ, evdevQ+8)
	}
	if press.Sym != ParseKeysym("q") {
		t.Errorf("sym = %#x, want q", press.Sym)
	}
	if press.Text != "q" {
		t.Errorf("text = %q, want %q", press.Text, "q")
	}
	if press.Serial != 7 || press.Time != 100 {
		t.Errorf("serial=%d time=%d, want 7 and 100", press.Serial, press.Time)
	}
}

// A release types nothing. Emitting text for it would double every
// keystroke in a text field.
func TestAReleaseCarriesNoText(t *testing.T) {
	k, got := newTestKeyboard(t)

	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStateReleased)

	if len(*got) != 1 {
		t.Fatalf("got %d events, want 1", len(*got))
	}
	if text := (*got)[0].Text; text != "" {
		t.Errorf("a release carried text %q, want none", text)
	}
}

// Return resolves to "\r" through the legacy keysym table, which every
// caller would otherwise have to know to filter before storing it.
func TestControlCharactersNeverArriveAsText(t *testing.T) {
	k, got := newTestKeyboard(t)

	k.key(1, 0, evdevReturn, wlcore.KeyboardKeyStatePressed)

	ev := (*got)[0]
	if ev.Text != "" {
		t.Errorf("Return carried text %q, want none", ev.Text)
	}
	if ev.Sym != ParseKeysym("Return") {
		t.Errorf("sym = %#x, want Return: the keysym is how a caller acts on it", ev.Sym)
	}
}

// Feeding a modifier to the composer cancels a pending dead key, which
// discards the accent. The keymap is asked rather than the keysym tested,
// because AltGr is nowhere near the Shift/Control/Alt/Super block.
func TestAModifierDoesNotReachTheComposer(t *testing.T) {
	k, got := newTestKeyboard(t)

	k.key(1, 0, evdevAcute, wlcore.KeyboardKeyStatePressed)
	if text := (*got)[0].Text; text != "" {
		t.Fatalf("the dead key produced %q, want nothing yet", text)
	}
	if _, pending := k.composer.Pending(); !pending {
		t.Fatal("the dead key did not arm the composer")
	}

	k.key(2, 0, evdevShift, wlcore.KeyboardKeyStatePressed)
	if _, pending := k.composer.Pending(); !pending {
		t.Fatal("a modifier cancelled the pending dead key")
	}

	k.key(3, 0, evdevQ, wlcore.KeyboardKeyStatePressed)
	if text := (*got)[2].Text; text == "" || text == "q" {
		t.Errorf("text after the dead key = %q, want the accented letter", text)
	}
}

func TestModifiersAreReportedWithWhatTheKeySpent(t *testing.T) {
	k, got := newTestKeyboard(t)
	k.state.UpdateMask(ModShift, 0, 0, 0)

	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	ev := (*got)[0]
	if ev.Text != "Q" {
		t.Errorf("text = %q, want Q: Shift did not reach the level", ev.Text)
	}
	if ev.Mods.Depressed != ModShift || ev.Mods.Effective != ModShift {
		t.Errorf("mods = %+v, want Shift depressed and effective", ev.Mods)
	}
	// Shift went into choosing the character, so a shortcut must not also
	// see it — that is the difference between Ctrl+Shift+1 and "!".
	if ev.Mods.Consumed&ModShift == 0 {
		t.Error("Shift chose the level and was not reported as consumed")
	}
	if ev.Mods.Unconsumed()&ModShift != 0 {
		t.Errorf("Unconsumed = %#x, want Shift removed", ev.Mods.Unconsumed())
	}
}

// Nothing may be emitted before a keymap has arrived: there is no way to
// resolve a keysym, and guessing one would be worse than staying quiet.
func TestNoEventsBeforeAKeymapArrives(t *testing.T) {
	var got []Event
	k := &Keyboard{OnKey: func(ev Event) { got = append(got, ev) }}

	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	if len(got) != 0 {
		t.Fatalf("emitted %d events without a keymap, want 0", len(got))
	}
}

func TestKeyboardWithoutHandlersDoesNotPanic(t *testing.T) {
	km, err := Compile(testKeymapSrc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	k := &Keyboard{keymap: km, state: km.NewState()}

	k.setRepeatInfo(25, 300)
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)
	k.Tick(time.Now().Add(time.Second))
	k.enter(nil)
	k.leave()
	k.fail(errTest)
}

var errTest = errorString("test")

type errorString string

func (e errorString) Error() string { return string(e) }

func TestRepeatStartsAfterTheDelayAndThenRuns(t *testing.T) {
	k, got := newTestKeyboard(t)
	k.setRepeatInfo(25, 400) // 25/s, 400ms delay

	start := time.Now()
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	due := k.NextRepeat()
	if due.IsZero() {
		t.Fatal("pressing a repeating key scheduled no repeat")
	}
	if wait := due.Sub(start); wait < 350*time.Millisecond || wait > 450*time.Millisecond {
		t.Errorf("first repeat due in %v, want about the 400ms delay", wait)
	}

	// Nothing is due yet.
	if k.Tick(start) {
		t.Error("Tick fired before the delay had passed")
	}
	if len(*got) != 1 {
		t.Fatalf("got %d events, want just the press", len(*got))
	}

	if !k.Tick(due) {
		t.Fatal("Tick did not fire when the repeat was due")
	}
	if len(*got) != 2 {
		t.Fatalf("got %d events, want the press and one repeat", len(*got))
	}

	rep := (*got)[1]
	if rep.State != Repeated {
		t.Errorf("state = %v, want repeated", rep.State)
	}
	if rep.Evdev != evdevQ || rep.Text != "q" {
		t.Errorf("repeat = evdev %d text %q, want %d and q", rep.Evdev, rep.Text, evdevQ)
	}
	// A repeat invents no serial: none arrived with it.
	if rep.Serial != 1 {
		t.Errorf("serial = %d, want the press's 1", rep.Serial)
	}

	// The second repeat comes at the rate, not the delay.
	if gap := k.NextRepeat().Sub(due); gap < 30*time.Millisecond || gap > 50*time.Millisecond {
		t.Errorf("second repeat %v after the first, want about 40ms for 25/s", gap)
	}
}

// The work a caller does between ticks — repainting, committing a frame —
// must not be added to the repeat period, or the rate drifts below what the
// compositor asked for and wobbles with however long that work took. So the
// next repeat is scheduled from when this one was due, not from now.
func TestTheCadenceDoesNotDriftWithTheWorkBetweenTicks(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.setRepeatInfo(25, 100) // 40ms apart
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	due := k.NextRepeat()
	// The loop answers the repeat 15ms late, as a redraw would.
	k.Tick(due.Add(15 * time.Millisecond))

	if gap := k.NextRepeat().Sub(due); gap < 35*time.Millisecond || gap > 45*time.Millisecond {
		t.Fatalf("next repeat is %v after the last due time, want about 40ms: "+
			"the caller's work was added to the period", gap)
	}
}

// Falling behind by more than a whole interval cannot be caught up without
// firing a burst, so there the cadence resynchronizes and the missed
// repeats are dropped rather than arriving as a rush of characters.
func TestALateTickDoesNotFireABurst(t *testing.T) {
	k, got := newTestKeyboard(t)
	k.setRepeatInfo(25, 100)
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	late := k.NextRepeat().Add(5 * time.Second)
	if !k.Tick(late) {
		t.Fatal("a late Tick did not fire")
	}
	if len(*got) != 2 {
		t.Fatalf("got %d events, want the press and exactly one repeat", len(*got))
	}
	if next := k.NextRepeat(); !next.After(late) {
		t.Errorf("next repeat is %v, not scheduled forward from the late tick", next.Sub(late))
	}
	// One interval past the late tick, not a queue of everything missed.
	if gap := k.NextRepeat().Sub(late); gap > 60*time.Millisecond {
		t.Errorf("next repeat is %v after the late tick, want one interval", gap)
	}
}

func TestReleasingTheKeyStopsTheRepeat(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.setRepeatInfo(25, 100)
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	k.key(2, 0, evdevQ, wlcore.KeyboardKeyStateReleased)

	if !k.NextRepeat().IsZero() {
		t.Fatal("releasing the key left a repeat scheduled")
	}
	if k.Tick(time.Now().Add(time.Hour)) {
		t.Fatal("a released key still repeated")
	}
}

// The release goes to whatever gets the focus next, so a repeat left
// running here would never be stopped by anything.
func TestLosingTheFocusStopsTheRepeat(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.setRepeatInfo(25, 100)
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	k.leave()

	if !k.NextRepeat().IsZero() {
		t.Fatal("losing the focus left a repeat scheduled")
	}
	if k.Focus() != nil {
		t.Fatal("leave did not clear the focus")
	}
}

// A half-typed accent must not survive a focus change, or the next
// window's first letter silently absorbs it.
func TestLosingTheFocusDropsAPendingDeadKey(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.key(1, 0, evdevAcute, wlcore.KeyboardKeyStatePressed)

	k.leave()

	if _, pending := k.composer.Pending(); pending {
		t.Fatal("a dead key survived the focus leaving")
	}
}

// A modifier does not repeat: xkb_symbols says so with repeat=no, which
// the keymap already resolved.
func TestAKeyThatDoesNotRepeatSchedulesNothing(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.setRepeatInfo(25, 100)

	k.key(1, 0, evdevShift, wlcore.KeyboardKeyStatePressed)

	if !k.NextRepeat().IsZero() {
		t.Error("a modifier scheduled a repeat")
	}
}

// rate 0 is how a compositor says it does not want the client repeating —
// including when it has taken repeat over itself.
func TestRateZeroDisablesRepeat(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.setRepeatInfo(0, 400)

	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	if !k.NextRepeat().IsZero() {
		t.Error("rate 0 still scheduled a repeat")
	}
}

// repeat_info can arrive at any time, not only during setup, and a new one
// must not leave a repeat running at the old rate.
func TestNewRepeatInfoAbandonsARepeatInFlight(t *testing.T) {
	k, _ := newTestKeyboard(t)
	k.setRepeatInfo(25, 100)
	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStatePressed)

	k.setRepeatInfo(10, 600)

	if !k.NextRepeat().IsZero() {
		t.Error("a new repeat_info left the old repeat scheduled")
	}
}

// Since wl_keyboard v10 the compositor may repeat for us. Both paths have
// to reach the caller as the same thing.
func TestACompositorDrivenRepeatArrivesAsARepeat(t *testing.T) {
	k, got := newTestKeyboard(t)

	k.key(1, 0, evdevQ, wlcore.KeyboardKeyStateRepeated)

	if len(*got) != 1 {
		t.Fatalf("got %d events, want 1", len(*got))
	}
	ev := (*got)[0]
	if ev.State != Repeated {
		t.Errorf("state = %v, want repeated", ev.State)
	}
	if ev.Text != "q" {
		t.Errorf("text = %q, want q: a compositor repeat still types", ev.Text)
	}
	// It must not also arm our own timer, or the key would repeat twice.
	if !k.NextRepeat().IsZero() {
		t.Error("a compositor-driven repeat also armed the client timer")
	}
}

// NextRepeat's zero value is what DispatchUntil reads as "no deadline", so
// a loop needs no branch for the common case of no key held.
func TestNextRepeatIsZeroWhenNothingRepeats(t *testing.T) {
	k, _ := newTestKeyboard(t)

	if !k.NextRepeat().IsZero() {
		t.Fatal("a fresh Keyboard reported a repeat due")
	}
	if k.Tick(time.Now()) {
		t.Fatal("Tick fired with nothing held")
	}
}

func TestEnterAndLeaveReportTheFocus(t *testing.T) {
	k, _ := newTestKeyboard(t)
	var seen []*wlcore.Surface
	k.OnFocus = func(s *wlcore.Surface) { seen = append(seen, s) }

	k.enter(nil)
	k.leave()

	if len(seen) != 2 {
		t.Fatalf("got %d focus callbacks, want 2", len(seen))
	}
	if seen[1] != nil {
		t.Error("leave did not report a nil surface")
	}
}
