package pointer

import (
	"math"
	"testing"
)

func newGestureRecorder() (*gestureState, *[]Event) {
	var events []Event
	g := &gestureState{emit: func(ev Event) { events = append(events, ev) }}
	return g, &events
}

func assertKinds(t *testing.T, events []Event, want []EventKind) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].Kind != want[i] {
			t.Errorf("event %d kind = %v, want %v", i, events[i].Kind, want[i])
		}
	}
}

func TestPositionAndButtonEdgesUseTheLatestCoordinates(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(10, 20)
	g.motion(100, 12, 24)
	g.button(7, 110, 0x110, true)
	g.button(8, 120, 0x110, false)

	wantKinds := []EventKind{Position, Position, ButtonDown, ButtonUp, Click}
	assertKinds(t, *got, wantKinds)
	if ev := (*got)[2]; ev.X != 12 || ev.Y != 24 || ev.Button != 0x110 || ev.Serial != 7 {
		t.Fatalf("button down = %+v", ev)
	}
}

func TestReleaseWithoutPressOnlyEmitsButtonUp(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(1, 2)
	*got = nil
	g.button(9, 30, 0x111, false)

	assertKinds(t, *got, []EventKind{ButtonUp})
}

func TestExactThresholdRemainsAClick(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(0, 0)
	g.button(1, 10, 0x110, true)
	*got = nil

	g.motion(20, 0, 4)
	g.button(2, 30, 0x110, false)

	assertKinds(t, *got, []EventKind{Position, ButtonUp, Click})
}

func TestCrossingThresholdProducesACompleteDragCycle(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(10, 10)
	g.button(7, 100, 0x110, true)
	*got = nil

	g.motion(110, 15, 10)
	g.motion(120, 16, 12)
	g.button(8, 130, 0x110, false)

	assertKinds(t, *got, []EventKind{
		Position, DragStart, Position, DragMove, ButtonUp, DragEnd,
	})
	if ev := (*got)[1]; ev.StartX != 10 || ev.StartY != 10 || ev.Serial != 7 {
		t.Fatalf("drag start = %+v", ev)
	}
	if ev := (*got)[5]; ev.X != 16 || ev.Y != 12 || ev.StartX != 10 || ev.StartY != 10 || ev.Serial != 8 || ev.Time != 130 {
		t.Fatalf("drag end = %+v", ev)
	}
}

func TestSimultaneousButtonsStartDragsInPressOrder(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(0, 0)
	g.button(1, 10, 0x111, true)
	g.button(2, 20, 0x110, true)
	*got = nil

	g.motion(30, 5, 0)

	assertKinds(t, *got, []EventKind{Position, DragStart, DragStart})
	if (*got)[1].Button != 0x111 || (*got)[2].Button != 0x110 {
		t.Fatalf("drag starts are buttons %#x then %#x, want press order", (*got)[1].Button, (*got)[2].Button)
	}
}

func TestCompatibleSecondClickBecomesDoubleClickAtInclusiveLimits(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(0, 0)
	clickButton(g, 0x110, 100)
	g.motion(200, 0, 4)
	clickButton(g, 0x110, 600)

	if ev := (*got)[len(*got)-1]; ev.Kind != DoubleClick {
		t.Fatalf("last event = %v, want double-click", ev.Kind)
	}
}

func TestIncompatibleSecondClickStartsANewPair(t *testing.T) {
	tests := []struct {
		name   string
		button uint32
		time   uint32
		x, y   float32
	}{
		{name: "button", button: 0x111, time: 200},
		{name: "time", button: 0x110, time: 601},
		{name: "distance", button: 0x110, time: 200, x: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, got := newGestureRecorder()
			g.enter(0, 0)
			clickButton(g, 0x110, 100)
			g.motion(150, tt.x, tt.y)
			clickButton(g, tt.button, tt.time)

			if ev := (*got)[len(*got)-1]; ev.Kind != Click {
				t.Fatalf("last event = %v, want click", ev.Kind)
			}
		})
	}
}

func TestLeaveClearsDoubleClickHistory(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(2, 3)
	clickButton(g, 0x110, 100)
	g.leave()
	g.enter(2, 3)
	clickButton(g, 0x110, 200)

	if ev := (*got)[len(*got)-1]; ev.Kind != Click {
		t.Fatalf("last event = %v, want click", ev.Kind)
	}
}

func TestTimestampWraparoundCanStillDoubleClick(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(2, 3)
	clickButton(g, 0x110, math.MaxUint32-100)
	clickButton(g, 0x110, 100)

	assertKinds(t, *got, []EventKind{
		Position, ButtonDown, ButtonUp, Click,
		ButtonDown, ButtonUp, DoubleClick,
	})
}

func TestDuplicatePressDoesNotDuplicateActiveState(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(1, 2)
	g.button(1, 10, 0x110, true)
	g.button(2, 20, 0x110, true)

	if len(g.presses) != 1 {
		t.Fatalf("got %d active presses, want 1", len(g.presses))
	}
	*got = nil
	g.button(3, 30, 0x110, false)
	assertKinds(t, *got, []EventKind{ButtonUp, Click})
}

func TestLeaveCancelsAllActiveDrags(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(0, 0)
	g.button(1, 10, 0x110, true)
	g.button(2, 20, 0x111, true)
	g.motion(30, 5, 0)
	g.leave()
	*got = nil

	g.button(3, 40, 0x110, false)
	g.button(4, 50, 0x111, false)

	assertKinds(t, *got, []EventKind{ButtonUp, ButtonUp})
}

func TestCancellationInsideDragCallbackStopsTheMotion(t *testing.T) {
	g, got := newGestureRecorder()
	g.enter(0, 0)
	g.button(1, 10, 0x110, true)
	g.button(2, 20, 0x111, true)
	*got = nil
	g.emit = func(ev Event) {
		*got = append(*got, ev)
		if ev.Kind == DragStart {
			g.leave()
		}
	}

	g.motion(30, 5, 0)

	assertKinds(t, *got, []EventKind{Position, DragStart})
	if len(g.presses) != 0 {
		t.Fatalf("cancellation left %d active presses", len(g.presses))
	}
}

func TestCancellationInsideButtonDownDoesNotRestoreThePress(t *testing.T) {
	g, _ := newGestureRecorder()
	g.emit = func(ev Event) {
		if ev.Kind == ButtonDown {
			g.leave()
		}
	}

	g.button(1, 10, 0x110, true)

	if len(g.presses) != 0 {
		t.Fatalf("cancellation left %d active presses", len(g.presses))
	}
}

func TestCancellationInsideClickDoesNotRestoreClickHistory(t *testing.T) {
	g, _ := newGestureRecorder()
	g.enter(0, 0)
	g.button(1, 10, 0x110, true)
	g.emit = func(ev Event) {
		if ev.Kind == Click {
			g.leave()
		}
	}

	g.button(2, 20, 0x110, false)

	if g.lastClick.valid {
		t.Fatal("cancellation restored double-click history")
	}
}

func clickButton(g *gestureState, button, releaseTime uint32) {
	g.button(1, releaseTime-1, button, true)
	g.button(2, releaseTime, button, false)
}
