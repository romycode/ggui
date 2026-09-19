package pointer

import (
	"errors"
	"testing"

	"github.com/romycode/ggui/wayland/wlcore"
)

type fakeDevice struct {
	listener   wlcore.PointerListener
	releases   int
	releaseErr error
}

func (d *fakeDevice) SetListener(l wlcore.PointerListener) { d.listener = l }

func (d *fakeDevice) Release() error {
	d.releases++
	return d.releaseErr
}

func newTestPointer() (*Pointer, *[]Event) {
	var events []Event
	p := &Pointer{}
	p.OnEvent = func(ev Event) { events = append(events, ev) }
	p.gesture.emit = p.emit
	return p, &events
}

func TestNewRejectsANilConnOrSeat(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Error("a nil conn and seat were accepted")
	}
}

func TestNewRejectsASeatTooOldToReleasePointer(t *testing.T) {
	conn := &wlcore.Conn{}
	seat := &wlcore.Seat{ProxyBase: wlcore.NewProxyBase(2, 2, conn)}

	if _, err := New(conn, seat); err == nil {
		t.Error("a seat below version 3 was accepted")
	}
}

func TestEnterMotionAndLeaveTranslateWaylandValues(t *testing.T) {
	p, got := newTestPointer()
	surface := &wlcore.Surface{}
	var focuses []*wlcore.Surface
	p.OnFocus = func(s *wlcore.Surface) { focuses = append(focuses, s) }

	p.enter(surface, wlcore.FixedFromFloat64(1.25), wlcore.FixedFromFloat64(2.5))
	p.motion(10, wlcore.FixedFromFloat64(3.5), wlcore.FixedFromFloat64(4.75))
	p.leave()

	if len(*got) != 2 || (*got)[0].Kind != Position || (*got)[1].Kind != Position {
		t.Fatalf("events = %+v", *got)
	}
	if (*got)[0].X != 1.25 || (*got)[0].Y != 2.5 || (*got)[1].X != 3.5 || (*got)[1].Y != 4.75 {
		t.Fatalf("positions = %+v", *got)
	}
	if len(focuses) != 2 || focuses[0] != surface || focuses[1] != nil {
		t.Fatalf("focuses = %+v", focuses)
	}
	if p.Focus() != nil {
		t.Fatal("leave retained the focused surface")
	}
}

func TestCapabilitiesAcquireOnceReleaseAndReacquire(t *testing.T) {
	p, _ := newTestPointer()
	devices := []*fakeDevice{{}, {}}
	acquires := 0
	p.acquire = func() (pointerDevice, error) {
		d := devices[acquires]
		acquires++
		return d, nil
	}

	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	if acquires != 1 {
		t.Fatalf("acquired %d times, want 1", acquires)
	}
	if devices[0].listener.Motion == nil || devices[0].listener.Button == nil {
		t.Fatal("acquired device has no pointer listener")
	}

	p.SetCapabilities(0)
	if devices[0].releases != 1 {
		t.Fatalf("released %d times, want 1", devices[0].releases)
	}
	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	if acquires != 2 {
		t.Fatalf("acquired %d times after capability returned, want 2", acquires)
	}
}

func TestCapabilityErrorsAreReported(t *testing.T) {
	wantAcquire := errors.New("acquire failed")
	p, _ := newTestPointer()
	var got []error
	p.OnError = func(err error) { got = append(got, err) }
	p.acquire = func() (pointerDevice, error) { return nil, wantAcquire }

	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	if len(got) != 1 || !errors.Is(got[0], wantAcquire) {
		t.Fatalf("acquire errors = %v", got)
	}

	wantRelease := errors.New("release failed")
	d := &fakeDevice{releaseErr: wantRelease}
	p.acquire = func() (pointerDevice, error) { return d, nil }
	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	p.SetCapabilities(0)
	if len(got) != 2 || !errors.Is(got[1], wantRelease) {
		t.Fatalf("release errors = %v", got)
	}
}

func TestReleaseErrorCallbackObservesDetachedState(t *testing.T) {
	p, _ := newTestPointer()
	d := &fakeDevice{releaseErr: errors.New("release failed")}
	p.acquire = func() (pointerDevice, error) { return d, nil }
	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	p.enter(&wlcore.Surface{}, 0, 0)
	var attached bool
	p.OnError = func(error) {
		attached = p.wl != nil || p.Focus() != nil
	}

	p.SetCapabilities(0)

	if attached {
		t.Fatal("release error callback observed an attached pointer")
	}
}

func TestCapabilityLossCancelsFocusPressesAndClickHistory(t *testing.T) {
	p, _ := newTestPointer()
	d := &fakeDevice{}
	p.acquire = func() (pointerDevice, error) { return d, nil }
	var focuses []*wlcore.Surface
	p.OnFocus = func(surface *wlcore.Surface) { focuses = append(focuses, surface) }
	p.SetCapabilities(wlcore.SeatCapabilityPointer)
	p.enter(&wlcore.Surface{}, 0, 0)
	p.gesture.button(1, 10, 0x110, true)
	p.gesture.lastClick = click{valid: true}

	p.SetCapabilities(0)

	if p.Focus() != nil || len(p.gesture.presses) != 0 || p.gesture.lastClick.valid {
		t.Fatalf("state survived detach: focus=%v presses=%d click=%+v", p.Focus(), len(p.gesture.presses), p.gesture.lastClick)
	}
	if len(focuses) != 2 || focuses[1] != nil {
		t.Fatalf("focuses = %v, want surface then nil", focuses)
	}
}

func TestCloseIsIdempotentAndReturnsReleaseError(t *testing.T) {
	want := errors.New("release failed")
	p, _ := newTestPointer()
	d := &fakeDevice{releaseErr: want}
	p.wl = d

	if err := p.Close(); !errors.Is(err, want) {
		t.Fatalf("Close error = %v, want %v", err, want)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	if d.releases != 1 {
		t.Fatalf("released %d times, want 1", d.releases)
	}
}

func TestFocusCallbackCanCloseBeforePositionIsEmitted(t *testing.T) {
	p, got := newTestPointer()
	p.wl = &fakeDevice{}
	p.OnFocus = func(*wlcore.Surface) {
		if err := p.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	p.enter(&wlcore.Surface{}, 0, 0)

	if len(*got) != 0 {
		t.Fatalf("got events after focus callback closed pointer: %+v", *got)
	}
}
