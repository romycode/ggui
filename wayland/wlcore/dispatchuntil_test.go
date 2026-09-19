package wlcore

import (
	"net"
	"testing"
	"time"
)

// newDispatchTestConn wires a Conn to a socketpair with wl_display
// registered, which is what every dispatch needs to have somewhere to
// deliver to.
func newDispatchTestConn(t *testing.T) (*Conn, *net.UnixConn) {
	t.Helper()

	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := newDisplay(displayID, 1, c)
	c.display = disp
	c.Register(disp)
	return c, server
}

// A deadline that passes with nothing to read is the ordinary case, not an
// error: the caller asked for its turn back so it could service a timer.
func TestDispatchUntilReturnsNilWhenTheDeadlinePasses(t *testing.T) {
	c, _ := newDispatchTestConn(t)

	start := time.Now()
	if err := c.DispatchUntil(start.Add(40 * time.Millisecond)); err != nil {
		t.Fatalf("DispatchUntil returned %v, want nil on a quiet deadline", err)
	}
	if waited := time.Since(start); waited < 30*time.Millisecond {
		t.Errorf("returned after %v, want to have waited for the deadline", waited)
	}
	if err := c.Err(); err != nil {
		t.Fatalf("a passing deadline killed the connection: %v", err)
	}
}

// The deadline must not outlive the call that asked for it, or one timed
// dispatch would put a deadline on every blocking one after it.
func TestDispatchUntilDoesNotLeaveTheDeadlineOnTheSocket(t *testing.T) {
	c, server := newDispatchTestConn(t)

	if err := c.DispatchUntil(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("DispatchUntil: %v", err)
	}

	// A plain Dispatch must now wait for data rather than inheriting the
	// deadline that already passed.
	done := make(chan error, 1)
	go func() { done <- c.Dispatch() }()

	select {
	case err := <-done:
		t.Fatalf("Dispatch returned %v immediately: it inherited the deadline", err)
	case <-time.After(50 * time.Millisecond):
	}

	// Unblock it so the goroutine does not outlive the test.
	body := NewEncoder().Uint32(1).Uint32(1).Bytes()
	if _, err := server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch never returned after data arrived")
	}
}

// Waiting on a deadline must not cost messages: whatever arrives before it
// passes is dispatched exactly as a blocking Dispatch would.
func TestDispatchUntilDeliversMessagesThatBeatTheDeadline(t *testing.T) {
	c, server := newDispatchTestConn(t)

	got := make(chan uint32, 1)
	c.display.listener = DisplayListener{
		DeleteID: func(id uint32) { got <- id },
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		body := NewEncoder().Uint32(4242).Bytes()
		server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body))
	}()

	if err := c.DispatchUntil(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("DispatchUntil: %v", err)
	}
	select {
	case id := <-got:
		if id != 4242 {
			t.Fatalf("delete_id carried %d, want 4242", id)
		}
	default:
		t.Fatal("the message arrived before the deadline and was not dispatched")
	}
}

// A zero deadline means none at all, which makes DispatchUntil the same
// call as Dispatch — worth pinning so a caller with no timer pending can
// pass the zero value instead of branching.
func TestDispatchUntilWithAZeroDeadlineBlocks(t *testing.T) {
	c, server := newDispatchTestConn(t)

	done := make(chan error, 1)
	go func() { done <- c.DispatchUntil(time.Time{}) }()

	select {
	case err := <-done:
		t.Fatalf("a zero deadline returned %v instead of blocking", err)
	case <-time.After(60 * time.Millisecond):
	}

	body := NewEncoder().Uint32(7).Bytes()
	if _, err := server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DispatchUntil: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DispatchUntil never returned after data arrived")
	}
}

// A real failure still has to be terminal. A timeout is the one error that
// is not, and it must not open the door to the others being swallowed.
func TestDispatchUntilStillReportsARealFailure(t *testing.T) {
	c, server := newDispatchTestConn(t)
	server.Close()

	if err := c.DispatchUntil(time.Now().Add(2 * time.Second)); err == nil {
		t.Fatal("a hung-up compositor reported no error")
	}
	if c.Err() == nil {
		t.Fatal("the failure was not recorded on the connection")
	}
}

// Deadlines must not desynchronize the stream: the read consumes nothing
// when it times out, so a message split across the timeout still decodes.
func TestDispatchUntilKeepsTheStreamAlignedAcrossTimeouts(t *testing.T) {
	c, server := newDispatchTestConn(t)

	seen := make(chan uint32, 4)
	c.display.listener = DisplayListener{
		DeleteID: func(id uint32) { seen <- id },
	}

	// Several quiet deadlines before anything is written.
	for range 3 {
		if err := c.DispatchUntil(time.Now().Add(10 * time.Millisecond)); err != nil {
			t.Fatalf("DispatchUntil: %v", err)
		}
	}

	for _, id := range []uint32{11, 22, 33} {
		body := NewEncoder().Uint32(id).Bytes()
		if _, err := server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(seen) < 3 && time.Now().Before(deadline) {
		if err := c.DispatchUntil(time.Now().Add(50 * time.Millisecond)); err != nil {
			t.Fatalf("DispatchUntil: %v", err)
		}
	}

	for _, want := range []uint32{11, 22, 33} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("delete_id carried %d, want %d: the stream lost alignment", got, want)
			}
		default:
			t.Fatalf("only %d of 3 messages arrived", len(seen))
		}
	}
}
