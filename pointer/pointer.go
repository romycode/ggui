package pointer

import (
	"errors"
	"fmt"

	"github.com/romycode/ggui/wayland/wlcore"
)

type pointerDevice interface {
	SetListener(wlcore.PointerListener)
	Release() error
}

// Pointer turns a seat's wl_pointer events into semantic [Event] values.
//
// The caller retains the seat listener and forwards every capability update
// through [Pointer.SetCapabilities]. Callbacks run synchronously in wlcore's
// dispatch goroutine. Pointer is not safe for concurrent use.
type Pointer struct {
	conn *wlcore.Conn
	seat *wlcore.Seat
	wl   pointerDevice

	acquire func() (pointerDevice, error)
	focus   *wlcore.Surface
	gesture gestureState

	// OnEvent receives position, button, click and drag events. Nil ignores
	// them.
	OnEvent func(Event)
	// OnFocus receives the focused surface, or nil when pointer focus is
	// lost. Nil ignores focus changes.
	OnFocus func(surface *wlcore.Surface)
	// OnError reports failures acquiring or releasing the pointer device.
	// Nil discards them.
	OnError func(err error)
}

// New returns a Pointer for the given connection and seat. The underlying
// wl_pointer is acquired when SetCapabilities first reports pointer support.
func New(conn *wlcore.Conn, seat *wlcore.Seat) (*Pointer, error) {
	if conn == nil || seat == nil {
		return nil, errors.New("pointer: nil conn or seat")
	}
	if seat.Version() < 3 {
		return nil, fmt.Errorf("pointer: seat version 3 or newer required, got %d", seat.Version())
	}
	p := &Pointer{conn: conn, seat: seat}
	p.acquire = func() (pointerDevice, error) { return seat.GetPointer() }
	p.gesture.emit = p.emit
	return p, nil
}

// Focus returns the surface holding pointer focus, or nil.
func (p *Pointer) Focus() *wlcore.Surface { return p.focus }

// SetCapabilities follows the seat's pointer capability in both directions.
// Call it for every wl_seat.capabilities event so the underlying device is
// acquired, released, and acquired again as hardware comes and goes.
func (p *Pointer) SetCapabilities(caps wlcore.SeatCapability) {
	hasPointer := caps.Has(wlcore.SeatCapabilityPointer)
	switch {
	case hasPointer && p.wl == nil:
		wl, err := p.acquire()
		if err != nil {
			p.fail(fmt.Errorf("pointer: get_pointer: %w", err))
			return
		}
		p.wl = wl
		p.listen(wl)
	case !hasPointer && p.wl != nil:
		p.detach()
	}
}

// Close releases the wl_pointer. Pointer must not be used afterwards.
func (p *Pointer) Close() error {
	if p.wl == nil {
		return nil
	}
	err := p.wl.Release()
	p.wl = nil
	p.leave()
	return err
}

func (p *Pointer) listen(wl pointerDevice) {
	wl.SetListener(wlcore.PointerListener{
		Enter: func(_ uint32, surface *wlcore.Surface, x, y wlcore.Fixed) {
			p.enter(surface, x, y)
		},
		Leave: func(uint32, *wlcore.Surface) {
			p.leave()
		},
		Motion: func(t uint32, x, y wlcore.Fixed) {
			p.motion(t, x, y)
		},
		Button: func(serial, t, button uint32, state wlcore.PointerButtonState) {
			p.button(serial, t, button, state)
		},
	})
}

func (p *Pointer) detach() {
	err := p.wl.Release()
	p.wl = nil
	p.leave()
	if err != nil {
		p.fail(fmt.Errorf("pointer: release: %w", err))
	}
}

func (p *Pointer) enter(surface *wlcore.Surface, x, y wlcore.Fixed) {
	p.focus = surface
	generation := p.gesture.generation
	if p.OnFocus != nil {
		p.OnFocus(surface)
	}
	if p.gesture.generation != generation || p.focus != surface {
		return
	}
	p.gesture.enter(float32(x.Float64()), float32(y.Float64()))
}

func (p *Pointer) leave() {
	hadFocus := p.focus != nil
	p.focus = nil
	p.gesture.leave()
	if hadFocus && p.OnFocus != nil {
		p.OnFocus(nil)
	}
}

func (p *Pointer) motion(t uint32, x, y wlcore.Fixed) {
	p.gesture.motion(t, float32(x.Float64()), float32(y.Float64()))
}

func (p *Pointer) button(serial, t, button uint32, state wlcore.PointerButtonState) {
	p.gesture.button(serial, t, button, state == wlcore.PointerButtonStatePressed)
}

func (p *Pointer) emit(ev Event) {
	if p.OnEvent != nil {
		p.OnEvent(ev)
	}
}

func (p *Pointer) fail(err error) {
	if p.OnError != nil {
		p.OnError(err)
	}
}
