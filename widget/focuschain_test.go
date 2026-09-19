package widget

import (
	"testing"
	"time"
)

// fakeFocusable reaches the corners of the [Focusable] contract a Button
// cannot. A Button only ever declines the focus by being disabled, and it
// always reports a visible change when it takes it; the chain has to cope
// with a widget that declines for its own reasons, and with one that accepts
// while reporting that a repaint would look no different. It also records
// every key it is handed, which is how a test tells "the chain consumed it"
// apart from "the chain forwarded it".
type fakeFocusable struct {
	name   string
	refuse bool // declines the focus, the way a disabled Button does
	silent bool // takes the focus but reports no visible change

	focused bool
	downs   []Key
	ups     []Key
}

func (f *fakeFocusable) Focused() bool { return f.focused }

func (f *fakeFocusable) SetFocused(focused bool) bool {
	if focused && f.refuse {
		return false
	}
	changed := f.focused != focused
	f.focused = focused
	return changed && !f.silent
}

func (f *fakeFocusable) KeyDown(k Key) bool {
	f.downs = append(f.downs, k)
	return f.focused
}

func (f *fakeFocusable) KeyUp(k Key) bool {
	f.ups = append(f.ups, k)
	return f.focused
}

func (f *fakeFocusable) sawDown(k Key) bool {
	for _, got := range f.downs {
		if got == k {
			return true
		}
	}
	return false
}

// newNamedButton is newTestButton with a label of its own, so a failure
// message can say which member of a chain the focus landed on.
func newNamedButton(name string) (*Button, *int) {
	b, _, clicks := newTestButton()
	b.Label = name
	return b, clicks
}

// who names whoever the chain reports focused, so a failure reads as prose
// instead of a printed struct.
func who(w Focusable) string {
	switch v := w.(type) {
	case nil:
		return "nobody"
	case *Button:
		return v.Label
	case *fakeFocusable:
		return v.name
	}
	return "an unknown widget"
}

// assertOneFocusHolder checks the chain's central invariant: whoever Focused
// names believes it is focused, and nobody else does.
func assertOneFocusHolder(t *testing.T, c *Chain, ws ...Focusable) {
	t.Helper()

	var held []Focusable
	for _, w := range ws {
		if w.Focused() {
			held = append(held, w)
		}
	}
	if len(held) > 1 {
		t.Fatalf("%d widgets hold the focus at once (%s and %s), want at most 1", len(held), who(held[0]), who(held[1]))
	}
	switch {
	case len(held) == 0 && c.Focused() != nil:
		t.Fatalf("the chain reports %s focused, but no widget thinks it holds the focus", who(c.Focused()))
	case len(held) == 1 && c.Focused() == nil:
		t.Fatalf("%s holds the focus, but the chain reports nobody focused", who(held[0]))
	case len(held) == 1 && c.Focused() != held[0]:
		t.Fatalf("the chain reports %s focused while %s is the one holding it", who(c.Focused()), who(held[0]))
	}
}

func TestANewChainFocusesNobodyAndKeepsTheGivenOrder(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	c, _ := newNamedButton("c")
	ch := NewChain(a, b, c)

	if ch.Focused() != nil {
		t.Fatalf("a new chain reports %s focused, want nobody", who(ch.Focused()))
	}
	for _, want := range []*Button{a, b, c} {
		ch.Next()
		if ch.Focused() != want {
			t.Fatalf("tabbing reached %s, want %s: the chain did not keep the given order", who(ch.Focused()), want.Label)
		}
	}
}

// The chain does not reach into widgets it was merely handed, so a widget the
// caller had already focused keeps the focus until the chain is asked to move
// it.
func TestNewChainLeavesAnAlreadyFocusedWidgetAlone(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	b.SetFocused(true)

	NewChain(a, b)

	if !b.Focused() {
		t.Fatal("building a chain took the focus away from a widget that already had it")
	}
	if a.Focused() {
		t.Fatal("building a chain handed the focus to its first member")
	}
}

// A caller laying out widgets from a slice will hand over holes in it; the
// chain must drop them rather than carry a nil it will later call.
func TestNewChainSkipsNilMembers(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	ch := NewChain(nil, a, nil, b)

	for _, want := range []*Button{a, b, a} {
		ch.Next()
		if ch.Focused() != want {
			t.Fatalf("tabbing reached %s, want %s: a nil member was not skipped", who(ch.Focused()), want.Label)
		}
	}
}

func TestAnEmptyChainIsInertAndDoesNotPanic(t *testing.T) {
	for _, ch := range []*Chain{NewChain(), NewChain(nil, nil)} {
		if ch.Next() || ch.Prev() || ch.Blur() {
			t.Fatal("an empty chain reported a visible change")
		}
		if ch.KeyDown(KeyTab) || ch.KeyDown(KeyEnter) || ch.KeyUp(KeySpace) {
			t.Fatal("an empty chain reported a visible change from a key")
		}
		if ch.Focused() != nil {
			t.Fatalf("an empty chain reports %s focused, want nobody", who(ch.Focused()))
		}
	}
}

func TestFocusMovesTheFocusBetweenTwoMembers(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	ch := NewChain(a, b)

	if !ch.Focus(a) {
		t.Fatal("focusing an unfocused member reported no change")
	}
	if !ch.Focus(b) {
		t.Fatal("moving the focus from one member to another reported no change")
	}
	if a.Focused() {
		t.Fatal("the widget that lost the focus still thinks it holds it")
	}
	assertOneFocusHolder(t, ch, a, b)
	if ch.Focused() != b {
		t.Fatalf("chain focused %s, want b", who(ch.Focused()))
	}
}

func TestRefocusingTheFocusedMemberChangesNothing(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	ch := NewChain(a, b)
	ch.Focus(a)

	if ch.Focus(a) {
		t.Fatal("focusing the already focused member reported a change")
	}
	assertOneFocusHolder(t, ch, a, b)
}

// A widget the chain does not own is not the chain's to focus, and neither is
// nil: both leave the current holder exactly where it was.
func TestFocusingANonMemberOrNilIsANoOp(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	outsider, _ := newNamedButton("outsider")
	ch := NewChain(a, b)
	ch.Focus(a)

	if ch.Focus(outsider) {
		t.Fatal("focusing a widget outside the chain reported a change")
	}
	if outsider.Focused() {
		t.Fatal("a widget outside the chain was given the focus")
	}
	if ch.Focus(nil) {
		t.Fatal("focusing nil reported a change")
	}
	if ch.Focused() != a {
		t.Fatalf("chain focused %s after two no-ops, want a", who(ch.Focused()))
	}
	assertOneFocusHolder(t, ch, a, b, outsider)
}

// The old holder is unfocused before the new one is offered the focus, so a
// widget that declines leaves the chain empty rather than rolling back.
func TestFocusingAMemberThatDeclinesLeavesNobodyFocused(t *testing.T) {
	a, _ := newNamedButton("a")
	off, _ := newNamedButton("off")
	off.Disabled = true
	ch := NewChain(a, off)
	ch.Focus(a)

	if !ch.Focus(off) {
		t.Fatal("moving the focus off a focused widget onto one that declines reported no change")
	}
	if ch.Focused() != nil {
		t.Fatalf("chain focused %s after the target declined, want nobody", who(ch.Focused()))
	}
	assertOneFocusHolder(t, ch, a, off)

	if ch.Focus(off) {
		t.Fatal("focusing a member that declines, with nobody focused, reported a change")
	}
}

func TestBlurTakesTheFocusOffTheHolder(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	ch := NewChain(a, b)
	ch.Focus(b)

	if !ch.Blur() {
		t.Fatal("blurring a chain with a focused member reported no change")
	}
	if b.Focused() {
		t.Fatal("the blurred widget still thinks it holds the focus")
	}
	assertOneFocusHolder(t, ch, a, b)
	if ch.Blur() {
		t.Fatal("blurring a chain with nobody focused reported a change")
	}
}

// SetFocused returning false means "a repaint would look no different", not
// "I refuse". A chain that read refusal out of that bool would make a widget
// with no visible focus state permanently untabbable, and Focused is the only
// answer to who holds the focus.
func TestAMemberThatTakesTheFocusSilentlyIsStillFocused(t *testing.T) {
	quiet := &fakeFocusable{name: "quiet", silent: true}
	b, _ := newNamedButton("b")
	ch := NewChain(quiet, b)

	if ch.Focus(quiet) {
		t.Fatal("a widget that takes the focus without changing its appearance reported a change")
	}
	if ch.Focused() != quiet {
		t.Fatalf("chain focused %s, want quiet: a silent SetFocused was read as a refusal", who(ch.Focused()))
	}
	assertOneFocusHolder(t, ch, quiet, b)

	ch.Blur()
	if ch.Next() {
		t.Fatal("tabbing onto a widget with no visible focus state reported a change")
	}
	if ch.Focused() != quiet {
		t.Fatalf("Next reached %s, want quiet: a silent member was skipped", who(ch.Focused()))
	}
}

// The same trap as above, with the widget a caller actually ships: a button
// styled with no focus ring is an ordinary, tabbable button.
func TestAButtonWithNoFocusRingIsStillFocusedAndReachable(t *testing.T) {
	plain, _ := newNamedButton("plain")
	plain.Style.FocusRingWidth = 0
	b, _ := newNamedButton("b")
	ch := NewChain(plain, b)

	ch.Focus(plain)
	if !plain.Focused() {
		t.Fatal("a button styled with no focus ring did not take the focus")
	}
	if ch.Focused() != plain {
		t.Fatalf("chain focused %s, want the button with no focus ring", who(ch.Focused()))
	}

	ch.Blur()
	ch.Next()
	if ch.Focused() != plain {
		t.Fatalf("Next reached %s, want the button with no focus ring first", who(ch.Focused()))
	}
}

func TestNextWalksForwardAndWrapsAround(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	c, _ := newNamedButton("c")
	ch := NewChain(a, b, c)

	for i, want := range []*Button{a, b, c, a} {
		if !ch.Next() {
			t.Fatalf("Next reported no change on step %d", i)
		}
		if ch.Focused() != want {
			t.Fatalf("step %d reached %s, want %s", i, who(ch.Focused()), want.Label)
		}
		assertOneFocusHolder(t, ch, a, b, c)
	}
}

func TestPrevWalksBackwardAndWrapsAround(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	c, _ := newNamedButton("c")
	ch := NewChain(a, b, c)
	ch.Focus(c)

	for i, want := range []*Button{b, a, c} {
		if !ch.Prev() {
			t.Fatalf("Prev reported no change on step %d", i)
		}
		if ch.Focused() != want {
			t.Fatalf("step %d reached %s, want %s", i, who(ch.Focused()), want.Label)
		}
		assertOneFocusHolder(t, ch, a, b, c)
	}
}

// Whichever end Prev picks with nobody focused, it has to pick one: a
// backtab into a window that has not been tabbed into yet must land
// somewhere.
// Entering the chain from nowhere lands at whichever end the direction
// comes from: Tab at the first member, Shift+Tab at the last. Landing on
// the first in both directions would make the two keys agree on the one
// press where the user most expects them to differ.
func TestEnteringAnUnfocusedChainLandsAtTheEndTheDirectionComesFrom(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	c, _ := newNamedButton("c")

	forward := NewChain(a, b, c)
	if !forward.Next() {
		t.Fatal("Next with nobody focused reported no change")
	}
	if forward.Focused() != a {
		t.Fatalf("Next from nowhere reached %s, want the first member a", who(forward.Focused()))
	}
	assertOneFocusHolder(t, forward, a, b, c)

	// A separate chain over separate widgets, so the forward walk above
	// cannot be what put the focus anywhere.
	d, _ := newNamedButton("d")
	e, _ := newNamedButton("e")
	f, _ := newNamedButton("f")

	backward := NewChain(d, e, f)
	if !backward.Prev() {
		t.Fatal("Prev with nobody focused reported no change")
	}
	if backward.Focused() != f {
		t.Fatalf("Prev from nowhere reached %s, want the last member f", who(backward.Focused()))
	}
	assertOneFocusHolder(t, backward, d, e, f)
}

func TestTabbingSkipsMembersThatDeclineTheFocus(t *testing.T) {
	a, _ := newNamedButton("a")
	off, _ := newNamedButton("off")
	off.Disabled = true
	c, _ := newNamedButton("c")
	ch := NewChain(a, off, c)

	ch.Next()
	ch.Next()
	if ch.Focused() != c {
		t.Fatalf("Next reached %s, want c: the disabled member was not skipped", who(ch.Focused()))
	}
	assertOneFocusHolder(t, ch, a, off, c)

	ch.Prev()
	if ch.Focused() != a {
		t.Fatalf("Prev reached %s, want a: the disabled member was not skipped", who(ch.Focused()))
	}
	assertOneFocusHolder(t, ch, a, off, c)
}

// A chain where nobody can take the focus is the case that hangs an
// implementation that keeps walking until it finds a taker. Drive it off the
// test goroutine so such an implementation fails here, naming the problem,
// instead of tripping the whole package's test timeout.
func TestTabbingAChainWhereEveryMemberDeclinesTerminates(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	c, _ := newNamedButton("c")
	for _, w := range []*Button{a, b, c} {
		w.Disabled = true
	}
	ch := NewChain(a, b, c)

	done := make(chan bool, 1)
	go func() { done <- ch.Next() || ch.Prev() }()

	select {
	case changed := <-done:
		if changed {
			t.Fatal("tabbing a chain where every member declines reported a visible change")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tabbing a chain where every member declines never returned")
	}
	if ch.Focused() != nil {
		t.Fatalf("chain focused %s where every member declines, want nobody", who(ch.Focused()))
	}
	assertOneFocusHolder(t, ch, a, b, c)
}

// Tab is the chain's own key. Letting it through as well would activate the
// widget the user was only trying to leave.
func TestTabMovesTheFocusAndNeverReachesTheFocusedWidget(t *testing.T) {
	a, aClicks := newNamedButton("a")
	b, bClicks := newNamedButton("b")
	ch := NewChain(a, b)
	ch.Focus(a)

	if !ch.KeyDown(KeyTab) {
		t.Fatal("Tab moving the focus reported no change")
	}
	if a.Focused() {
		t.Fatal("Tab left the focus on the widget it should have moved away from")
	}
	if ch.Focused() != b {
		t.Fatalf("Tab reached %s, want b", who(ch.Focused()))
	}
	if *aClicks != 0 || *bClicks != 0 {
		t.Fatalf("OnClick called %d and %d times for a Tab, want 0 and 0", *aClicks, *bClicks)
	}
	if a.Pressed() {
		t.Fatal("Tab armed the widget it moved away from")
	}
}

func TestBacktabMovesTheFocusBackwardAndIsAlsoConsumed(t *testing.T) {
	a := &fakeFocusable{name: "a"}
	b := &fakeFocusable{name: "b"}
	ch := NewChain(a, b)
	ch.Focus(b)

	ch.KeyDown(KeyBacktab)

	if ch.Focused() != a {
		t.Fatalf("Backtab reached %s, want a", who(ch.Focused()))
	}
	if b.sawDown(KeyBacktab) || a.sawDown(KeyBacktab) {
		t.Fatal("Backtab was forwarded to a widget instead of being consumed by the chain")
	}
	assertOneFocusHolder(t, ch, a, b)
}

// Tab is handled by the chain rather than forwarded, so it still has work to
// do when nothing is focused yet: it is how the user tabs into the window.
func TestTabWithNobodyFocusedEntersTheChain(t *testing.T) {
	a, _ := newNamedButton("a")
	b, _ := newNamedButton("b")
	ch := NewChain(a, b)

	if !ch.KeyDown(KeyTab) {
		t.Fatal("Tab with nobody focused reported no change")
	}
	if ch.Focused() != a {
		t.Fatalf("Tab with nobody focused reached %s, want a", who(ch.Focused()))
	}
}

func TestOtherKeysReachTheFocusedMemberAndNobodyElse(t *testing.T) {
	a, aClicks := newNamedButton("a")
	b, bClicks := newNamedButton("b")
	ch := NewChain(a, b)
	ch.Focus(b)

	ch.KeyDown(KeyEnter)

	if *bClicks != 1 {
		t.Fatalf("OnClick called %d times on the focused widget for an Enter, want 1", *bClicks)
	}
	if *aClicks != 0 {
		t.Fatalf("OnClick called %d times on an unfocused widget, want 0", *aClicks)
	}
}

// Space arms on the press and fires on the release, and the chain has to keep
// both halves going to the same widget for that to work at all.
func TestSpaceArmsAndFiresThroughTheChain(t *testing.T) {
	a, _ := newNamedButton("a")
	b, clicks := newNamedButton("b")
	ch := NewChain(a, b)
	ch.Focus(b)

	if !ch.KeyDown(KeySpace) {
		t.Fatal("arming the focused widget with Space through the chain reported no change")
	}
	if !b.Pressed() {
		t.Fatal("the focused widget is not showing as pressed with Space held")
	}
	if *clicks != 0 {
		t.Fatalf("OnClick called %d times on the press, want 0", *clicks)
	}

	if !ch.KeyUp(KeySpace) {
		t.Fatal("releasing Space through the chain reported no change")
	}
	if *clicks != 1 {
		t.Fatalf("OnClick called %d times, want 1", *clicks)
	}
}

func TestKeysAreIgnoredWithNobodyFocused(t *testing.T) {
	a, aClicks := newNamedButton("a")
	b, bClicks := newNamedButton("b")
	ch := NewChain(a, b)

	for _, k := range []Key{KeyNone, KeySpace, KeyEnter, KeyEscape} {
		if ch.KeyDown(k) {
			t.Fatalf("key %d reported a change on a chain with nobody focused", k)
		}
		if ch.KeyUp(k) {
			t.Fatalf("releasing key %d reported a change on a chain with nobody focused", k)
		}
	}
	if *aClicks != 0 || *bClicks != 0 {
		t.Fatalf("OnClick called %d and %d times with nobody focused, want 0 and 0", *aClicks, *bClicks)
	}
	if a.Pressed() || b.Pressed() {
		t.Fatal("a key armed a widget on a chain with nobody focused")
	}
}
