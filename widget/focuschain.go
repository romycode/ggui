package widget

// Chain walks a tab order over a set of [Focusable] widgets and keeps at
// most one of them focused.
//
// A widget is told whether it is focused and never asks, which is what lets
// one be written and tested without knowing its siblings exist — and leaves
// somebody having to arbitrate. Chain is that somebody, and the smallest
// one that works: a slice in tab order, and the member it believes holds
// the focus. The caller feeds it key events instead of feeding them to each
// widget in turn.
//
// There is no way to ask a widget whether it wants the focus, so Chain does
// not ask: it gives the focus and then reads [Focusable.Focused] back. One
// that declines — a disabled [Button] does — is skipped, and if every
// member declines the focus ends up nowhere rather than on a widget that
// would ignore the keyboard.
//
// KeyDown consumes [KeyTab] and [KeyBacktab]; they never reach the focused
// widget. That is a real limit rather than an oversight: a widget that
// wants Tab for itself — a multi-line field inserting a tabulation — cannot
// get it through a chain today, and its caller has to look at the key
// before handing it over.
//
// Like every widget here, a Chain repaints nothing and is not safe for
// concurrent use: each method reports whether a repaint would look
// different, and the caller repaints.
//
// The zero value is an empty chain, legal but with nothing to move the
// focus between; build one with [NewChain].
type Chain struct {
	widgets []Focusable
	focused Focusable // the member the chain believes holds the focus
}

// NewChain returns a chain over the given widgets, in the order Tab is to
// visit them. Nil entries are dropped instead of panicking, so a caller
// that builds its list conditionally does not have to filter it; an empty
// chain is legal and does nothing.
//
// The chain reads the focus here but does not write it: a widget that
// arrives already focused becomes the chain's current one, and nobody is
// focused or unfocused. If more than one arrives focused they are all left
// as they are — the chain does not touch state it was not asked to touch —
// and the first call that moves the focus restores the one-at-a-time
// invariant by unfocusing the rest.
func NewChain(widgets ...Focusable) *Chain {
	c := &Chain{widgets: make([]Focusable, 0, len(widgets))}
	for _, w := range widgets {
		if w == nil {
			continue
		}
		if c.focused == nil && w.Focused() {
			c.focused = w
		}
		c.widgets = append(c.widgets, w)
	}
	return c
}

// Focused returns the widget the chain believes holds the focus, or nil if
// none does.
func (c *Chain) Focused() Focusable { return c.focused }

// Focus gives the focus to w and reports whether a repaint would look
// different — the same bool the rest of the package returns, here the OR of
// the widget losing the focus and the one taking it.
//
// A w that is nil or not a member of the chain is ignored, and so is the
// one that already has the focus. If w declines the focus the chain ends up
// with nothing focused: the old holder has already given it up by then, and
// handing it back would be a move the caller did not ask for.
func (c *Chain) Focus(w Focusable) bool {
	if w == nil || w == c.focused {
		return false
	}
	i := c.indexOf(w)
	if i < 0 {
		return false
	}
	return c.focusAt(i)
}

// Blur takes the focus away from whichever member holds it and reports
// whether a repaint would look different. The chain ends with nothing
// focused.
func (c *Chain) Blur() bool {
	changed := c.blurOthers(-1)
	c.focused = nil
	return changed
}

// Next moves the focus to the next member that accepts it, wrapping around
// at the end, and reports whether a repaint would look different. Members
// that decline are skipped. With nothing focused the walk starts at the
// first member.
//
// If nobody accepts, the focus ends up nowhere; the walk is one lap long,
// so a chain where every member is disabled terminates instead of spinning.
func (c *Chain) Next() bool { return c.move(1) }

// Prev is [Chain.Next] backwards: it moves the focus to the previous member
// that accepts it, wrapping around at the start, and with nothing focused
// it begins at the last member — the one Tab would have reached last.
func (c *Chain) Prev() bool { return c.move(-1) }

// KeyDown handles a key press and reports whether a repaint would look
// different. [KeyTab] and [KeyBacktab] move the focus and are consumed;
// every other key goes to the focused widget, or nowhere if none is
// focused, so the caller can hand the chain every press without first
// working out whose it is.
func (c *Chain) KeyDown(k Key) bool {
	switch k {
	case KeyTab:
		return c.Next()
	case KeyBacktab:
		return c.Prev()
	}
	if c.focused == nil {
		return false
	}
	return c.focused.KeyDown(k)
}

// KeyUp handles a key release and reports whether a repaint would look
// different. It goes to the focused widget, except for [KeyTab] and
// [KeyBacktab]: nothing latches on them, and their release belongs to the
// press that already moved the focus, not to the widget that just got it.
func (c *Chain) KeyUp(k Key) bool {
	if k == KeyTab || k == KeyBacktab || c.focused == nil {
		return false
	}
	return c.focused.KeyUp(k)
}

// move walks the chain by step, offering the focus to each member until one
// takes it. It is bounded by a single lap, which is what makes a chain that
// everybody declines end with the focus nowhere rather than looping.
func (c *Chain) move(step int) bool {
	n := len(c.widgets)
	if n == 0 {
		return false
	}
	// The walk starts one step before its first candidate. With nothing
	// focused that candidate is the end of the chain the direction comes
	// from: the first member going forward, the last one going back.
	i := c.indexOf(c.focused)
	if i < 0 {
		if step > 0 {
			i = n - 1
		} else {
			i = 0
		}
	}

	changed := false
	for range c.widgets {
		i = (i + step + n) % n
		if c.focusAt(i) {
			changed = true
		}
		if c.focused != nil {
			break
		}
	}
	return changed
}

// focusAt hands the focus to the member at i and reports whether a repaint
// would look different.
func (c *Chain) focusAt(i int) bool {
	changed := c.blurOthers(i)
	w := c.widgets[i]
	if w.SetFocused(true) {
		changed = true
	}
	// SetFocused returning false does not mean the widget refused, only
	// that nothing visible moved: a button whose style has no focus ring
	// accepts the focus and still looks exactly the same. Whether it took
	// it is readable from Focused and nowhere else.
	if w.Focused() {
		c.focused = w
	} else {
		c.focused = nil
	}
	return changed
}

// blurOthers takes the focus away from every member but the one at keep,
// which restores the one-at-a-time invariant on a chain that was handed
// more than one focused widget. A keep of -1 unfocuses all of them.
func (c *Chain) blurOthers(keep int) bool {
	changed := false
	for i, w := range c.widgets {
		if i == keep || !w.Focused() {
			continue
		}
		if w.SetFocused(false) {
			changed = true
		}
	}
	return changed
}

// indexOf returns the position of w in the chain, or -1 if it is not a
// member. The chain is a handful of widgets long and only moves the focus
// on a key press, so a scan costs nothing worth an index for.
func (c *Chain) indexOf(w Focusable) int {
	if w == nil {
		return -1
	}
	for i, m := range c.widgets {
		if m == w {
			return i
		}
	}
	return -1
}
