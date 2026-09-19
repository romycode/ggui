package eventloop

import (
	"sync"
	"testing"
	"time"

	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
)

func position(x float32) Event {
	return Event{Kind: EvPointer, Pointer: pointer.Event{Kind: pointer.Position, X: x}}
}

func dragMove(x float32) Event {
	return Event{Kind: EvPointer, Pointer: pointer.Event{Kind: pointer.DragMove, X: x}}
}

func button(kind pointer.EventKind) Event {
	return Event{Kind: EvPointer, Pointer: pointer.Event{Kind: kind}}
}

func key(state keyboard.KeyState, evdev uint32) Event {
	return Event{Kind: EvKey, Key: keyboard.Event{State: state, Evdev: evdev}}
}

// describe renders a drained batch as one comparable string per event.
func describe(evs []Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		switch ev.Kind {
		case EvPointer:
			out[i] = "pointer:" + ev.Pointer.Kind.String()
		case EvKey:
			out[i] = "key:" + ev.Key.State.String()
		default:
			out[i] = ev.Kind.String()
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A mouse can report at 1 kHz; the UI paints at 60 Hz. Every Position the UI
// has not read yet is stale the moment a newer one arrives, so only the
// newest is kept.
func TestInboxCoalescesConsecutivePositions(t *testing.T) {
	in := NewInbox()
	for i := 0; i < 1000; i++ {
		in.Push(position(float32(i)))
	}

	got := in.Drain(nil)
	if len(got) != 1 {
		t.Fatalf("drained %d events, want 1", len(got))
	}
	if got[0].Pointer.X != 999 {
		t.Errorf("kept X=%v, want the newest, 999", got[0].Pointer.X)
	}
}

func TestInboxCoalescesConsecutiveDragMoves(t *testing.T) {
	in := NewInbox()
	for i := 0; i < 50; i++ {
		in.Push(dragMove(float32(i)))
	}

	got := in.Drain(nil)
	if len(got) != 1 || got[0].Pointer.X != 49 {
		t.Fatalf("drained %v, want a single DragMove with X=49", describe(got))
	}
}

// Coalescing looks only at the tail. Merging across another event would
// reorder motion relative to it: a Position must not slide past the button
// press that came between two of them.
func TestInboxOnlyCoalescesAdjacentEvents(t *testing.T) {
	tests := []struct {
		name string
		push []Event
		want []string
	}{
		{
			name: "position around a button",
			push: []Event{position(1), button(pointer.ButtonDown), position(2)},
			want: []string{"pointer:position", "pointer:button-down", "pointer:position"},
		},
		{
			name: "drag move then position",
			push: []Event{dragMove(1), position(2)},
			want: []string{"pointer:drag-move", "pointer:position"},
		},
		{
			name: "position then drag move",
			push: []Event{position(1), dragMove(2)},
			want: []string{"pointer:position", "pointer:drag-move"},
		},
		{
			name: "position around a key",
			push: []Event{position(1), key(keyboard.Pressed, 30), position(2)},
			want: []string{"pointer:position", "key:pressed", "pointer:position"},
		},
		{
			name: "position around focus",
			push: []Event{position(1), {Kind: EvPointerFocus}, position(2)},
			want: []string{"pointer:position", "pointer-focus", "pointer:position"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInbox()
			for _, ev := range tt.push {
				in.Push(ev)
			}
			if got := describe(in.Drain(nil)); !equalStrings(got, tt.want) {
				t.Errorf("drained %v, want %v", got, tt.want)
			}
		})
	}
}

// Nothing a user meant is ever dropped or merged: a lost click or keystroke
// is a bug, where a lost Position is only a skipped frame of motion.
func TestInboxNeverDropsOrReordersWhatTheUserDid(t *testing.T) {
	in := NewInbox()
	want := []Event{
		{Kind: EvConfigure, Width: 100, Height: 50},
		{Kind: EvKeyboardFocus},
		key(keyboard.Pressed, 30),
		key(keyboard.Released, 30),
		button(pointer.ButtonDown),
		button(pointer.ButtonUp),
		button(pointer.Click),
		button(pointer.DoubleClick),
		button(pointer.DragStart),
		button(pointer.DragEnd),
		{Kind: EvBufferRelease},
		{Kind: EvFrameDone, Time: 7},
		{Kind: EvClosed},
	}
	for _, ev := range want {
		in.Push(ev)
	}

	got := in.Drain(nil)
	if len(got) != len(want) {
		t.Fatalf("drained %d events, want %d: %v", len(got), len(want), describe(got))
	}
	for i := range want {
		if got[i].Kind != want[i].Kind {
			t.Fatalf("event %d is %v, want %v", i, got[i].Kind, want[i].Kind)
		}
	}
	if got[0].Width != 100 || got[0].Height != 50 || got[11].Time != 7 {
		t.Error("payload fields were not carried through")
	}
}

// A repeat is synthesized by the client, not typed. A UI that fell behind
// would otherwise wake up to a rush of characters nobody pressed, so the
// backlog is bounded — but only for repeats.
func TestInboxBoundsPendingRepeatsButNotPresses(t *testing.T) {
	in := NewInbox()
	in.Push(key(keyboard.Pressed, 30))
	for i := 0; i < 100; i++ {
		in.Push(key(keyboard.Repeated, 30))
	}
	in.Push(key(keyboard.Released, 30))
	in.Push(key(keyboard.Pressed, 31))

	var pressed, released, repeated int
	got := in.Drain(nil)
	for _, ev := range got {
		switch ev.Key.State {
		case keyboard.Pressed:
			pressed++
		case keyboard.Released:
			released++
		case keyboard.Repeated:
			repeated++
		}
	}
	if pressed != 2 || released != 1 {
		t.Errorf("kept %d presses and %d releases, want 2 and 1", pressed, released)
	}
	if repeated != maxPendingRepeats {
		t.Errorf("kept %d repeats, want the bound, %d", repeated, maxPendingRepeats)
	}
	if last := got[len(got)-1]; last.Key.Evdev != 31 {
		t.Error("the press after the repeat storm is not last: order was lost")
	}
}

// The bound counts what is pending, not what ever arrived: once the UI has
// drained, repeats are accepted again.
func TestInboxAcceptsRepeatsAgainAfterADrain(t *testing.T) {
	in := NewInbox()
	for i := 0; i < maxPendingRepeats*2; i++ {
		in.Push(key(keyboard.Repeated, 30))
	}
	in.Drain(nil)

	in.Push(key(keyboard.Repeated, 30))
	if got := in.Drain(nil); len(got) != 1 {
		t.Fatalf("drained %d after the queue emptied, want 1", len(got))
	}
}

// The Wayland goroutine calls Push from inside a dispatch. If it could block
// on a UI that is busy, the socket would stall with it.
func TestInboxPushNeverBlocksWithNobodyDraining(t *testing.T) {
	in := NewInbox()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200_000; i++ {
			in.Push(position(float32(i)))
			in.Push(key(keyboard.Repeated, 30))
			in.Push(button(pointer.Click))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Push blocked with nobody draining")
	}
}

func TestInboxDrainEmptiesAndAppendsToDst(t *testing.T) {
	in := NewInbox()
	in.Push(button(pointer.Click))

	dst := make([]Event, 0, 8)
	got := in.Drain(dst)
	if len(got) != 1 || &got[0] != &dst[:1][0] {
		t.Fatalf("Drain did not append to dst's backing array (len %d)", len(got))
	}
	if again := in.Drain(nil); len(again) != 0 {
		t.Errorf("a second Drain returned %d events, want 0", len(again))
	}
}

// Drain must not hand back a slot that Push later coalesces into: the UI
// owns what it drained, and a Position pushed afterwards is a new event.
func TestInboxDrainedEventsAreNotRewrittenByLaterPushes(t *testing.T) {
	in := NewInbox()
	in.Push(position(1))
	got := in.Drain(nil)

	in.Push(position(2))
	if got[0].Pointer.X != 1 {
		t.Errorf("a drained Position changed to X=%v after a later Push", got[0].Pointer.X)
	}
}

// The token means "there may be something": a Push after the last Drain must
// leave it readable, exactly as with the mailbox.
func TestInboxSignalFollowsPush(t *testing.T) {
	in := NewInbox()
	in.Push(button(pointer.Click))

	select {
	case <-in.Signal():
	case <-time.After(time.Second):
		t.Fatal("no signal after a Push")
	}
	if got := in.Drain(nil); len(got) != 1 {
		t.Fatalf("drained %d, want 1", len(got))
	}
}

// The token must never be visible before the event it announces, for the
// same reason as in the mailbox: a consumer woken into an empty queue goes
// back to sleep with the event still to arrive.
func TestInboxSignalIsNeverVisibleBeforeItsEvent(t *testing.T) {
	const rounds = 2000
	in := NewInbox()

	acked := make(chan struct{})
	empty := make(chan int, 1)
	go func() {
		for i := 0; i < rounds; i++ {
			<-in.Signal()
			if len(in.Drain(nil)) == 0 {
				empty <- i
				return
			}
			acked <- struct{}{}
		}
	}()

	for i := 0; i < rounds; i++ {
		in.Push(button(pointer.Click))
		select {
		case <-acked:
		case round := <-empty:
			t.Fatalf("round %d: the consumer was woken into an empty queue", round)
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: the consumer never woke", i)
		}
	}
}

// With a producer racing a drainer, every click must come out exactly once
// and in order, whatever happens to the motion around them.
func TestInboxConcurrentPushAndDrainKeepsEveryClickInOrder(t *testing.T) {
	const clicks = 5000
	in := NewInbox()

	var got []float32
	done := make(chan struct{})
	go func() {
		defer close(done)
		var batch []Event
		for len(got) < clicks {
			<-in.Signal()
			batch = in.Drain(batch[:0])
			for _, ev := range batch {
				if ev.Pointer.Kind == pointer.Click {
					got = append(got, ev.Pointer.X)
				}
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < clicks; i++ {
			in.Push(position(float32(i)))
			in.Push(Event{Kind: EvPointer, Pointer: pointer.Event{Kind: pointer.Click, X: float32(i)}})
		}
	}()
	wg.Wait()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("consumer stuck after %d of %d clicks", len(got), clicks)
	}
	for i, x := range got {
		if x != float32(i) {
			t.Fatalf("click %d has X=%v: a click was lost or reordered", i, x)
		}
	}
}

func TestEventKindsHaveStableNames(t *testing.T) {
	tests := []struct {
		kind EventKind
		want string
	}{
		{EvKey, "key"},
		{EvPointer, "pointer"},
		{EvKeyboardFocus, "keyboard-focus"},
		{EvPointerFocus, "pointer-focus"},
		{EvConfigure, "configure"},
		{EvBufferRelease, "buffer-release"},
		{EvFrameDone, "frame-done"},
		{EvClosed, "closed"},
		{EventKind(255), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
