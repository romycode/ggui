package eventloop

// FrameClock decides when the UI may paint. It is the state machine behind
// "at most one frame in flight": a UI that painted whenever it had something
// to say would run ahead of a slow compositor, queue frames nobody sees and
// burn CPU on a window that is hidden. Instead it paints, then waits for the
// compositor's frame callback before painting again.
//
//	idle ──(invalidated or animating)──► paint, ask for a callback ──► waiting
//	waiting ──FrameDone──► (invalidated or animating) ? paint again : idle
//
// A static UI therefore costs nothing after its last paint, an animation
// runs at the compositor's pace, and a window the compositor stops sending
// callbacks to — a hidden one — stops painting by itself.
//
// FrameClock holds no time and knows nothing of Wayland: the caller reports
// what happened and asks what to do, which is what makes the rules testable
// as a table. It is not safe for concurrent use; it belongs to the UI
// goroutine. The zero value is an idle clock with a free buffer, ready to
// use.
type FrameClock struct {
	// waiting is true from Painted until the FrameDone that answers it.
	waiting bool
	// dirty records that something changed since the last paint.
	dirty bool
	// animating means the UI wants a frame on every callback, whether or
	// not anything was invalidated.
	animating bool
	// starved means every buffer is with the compositor, so painting now
	// would tear. It is the inverse of "free" so that the zero value is
	// the usable one.
	starved bool
}

// Invalidate records that the UI changed and wants a repaint.
func (c *FrameClock) Invalidate() { c.dirty = true }

// SetAnimating says whether the UI wants a frame on every callback. An
// animation turns it on when it starts and off when it settles, which is
// what lets an idle window stop asking for frames.
func (c *FrameClock) SetAnimating(on bool) { c.animating = on }

// SetBufferFree says whether a buffer the compositor is not reading is
// available to paint into. While it is false nothing paints, and the wish
// to paint is kept: it is honored the moment a buffer frees.
func (c *FrameClock) SetBufferFree(free bool) { c.starved = !free }

// FrameDone reports that the compositor answered the frame callback, so the
// frame in flight is over and another may start. Calling it with no frame in
// flight is harmless and invents no work.
func (c *FrameClock) FrameDone() { c.waiting = false }

// ShouldPaint reports whether the UI should paint now: no frame in flight,
// a buffer to paint into, and a reason to.
func (c *FrameClock) ShouldPaint() bool {
	return !c.waiting && !c.starved && (c.dirty || c.animating)
}

// Painted records that a frame was painted and presented. It clears the
// invalidate, since that paint covered it, and starts waiting for the
// compositor. Whatever is invalidated from here on is for the next frame.
func (c *FrameClock) Painted() {
	c.waiting = true
	c.dirty = false
}
