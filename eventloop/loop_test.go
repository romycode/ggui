package eventloop

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/internal/wltest"
	"github.com/romycode/ggui/wayland/wlcore"
)

// startLoop runs a Loop on its own goroutine, which becomes the Wayland
// goroutine, and stops it when the test ends.
func startLoop(t *testing.T, conn *wlcore.Conn, configure func(*Loop)) (*Loop, <-chan error) {
	t.Helper()
	l, err := New(conn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if configure != nil {
		configure(l)
	}
	// Closed after the error is sent, so a test that reads the error itself
	// does not leave the cleanup below waiting for a second value.
	done := make(chan error, 1)
	go func() {
		done <- l.Run()
		close(done)
	}()
	t.Cleanup(func() {
		l.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not return after Close")
		}
	})
	return l, done
}

// A Post has to wake a loop that is parked in poll with nothing to read,
// and promptly: this is what lets the UI run without waiting for the
// compositor to say something.
func TestPostWakesALoopWithNothingToRead(t *testing.T) {
	conn, _ := wltest.NewConn(t)
	l, _ := startLoop(t, conn, nil)
	time.Sleep(50 * time.Millisecond) // let it park

	ran := make(chan struct{})
	start := time.Now()
	l.Post(func() { close(ran) })

	select {
	case <-ran:
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("took %v to wake, want under 50ms", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Post never woke the loop")
	}
}

func TestWriteWakeRetriesInterruptedWrite(t *testing.T) {
	var attempts int
	write := func([]byte) (int, error) {
		attempts++
		if attempts == 1 {
			return 0, unix.EINTR
		}
		return 8, nil
	}

	if err := writeWake(write); err != nil {
		t.Fatalf("writeWake: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("write attempts = %d, want 2", attempts)
	}
}

func TestWriteWakeTreatsWouldBlockAsSuccess(t *testing.T) {
	if err := writeWake(func([]byte) (int, error) {
		return 0, unix.EAGAIN
	}); err != nil {
		t.Fatalf("writeWake returned %v for EAGAIN", err)
	}
}

// Every closure posted from every goroutine runs, each producer's in the
// order it posted them, and all on the one goroutine that runs the loop.
func TestPostRunsEveryClosureInOrderOnTheLoopGoroutine(t *testing.T) {
	const producers, perProducer = 8, 1250
	conn, _ := wltest.NewConn(t)
	l, _ := startLoop(t, conn, nil)

	type entry struct{ producer, seq int }
	var ran []entry // only appended from the loop goroutine
	var wg sync.WaitGroup
	done := make(chan struct{})

	var loopG atomic.Int64
	l.Post(func() { loopG.Store(goroutineID()) })

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				l.Post(func() {
					if goroutineID() != loopG.Load() {
						t.Error("closure ran off the loop goroutine")
					}
					ran = append(ran, entry{p, i})
					if len(ran) == producers*perProducer {
						close(done)
					}
				})
			}
		}()
	}
	wg.Wait()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("only %d of %d closures ran: a wakeup was lost", len(ran), producers*perProducer)
	}
	next := make([]int, producers)
	for _, e := range ran {
		if e.seq != next[e.producer] {
			t.Fatalf("producer %d ran seq %d, want %d", e.producer, e.seq, next[e.producer])
		}
		next[e.producer]++
	}
}

// goroutineID reads the id from the stack header. Test-only: it is how the
// tests check that something ran on the loop goroutine and not elsewhere.
func goroutineID() int64 {
	var buf [32]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [running]:..."
	var id int64
	for _, c := range buf[len("goroutine "):n] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + int64(c-'0')
	}
	return id
}

// The dangerous moment is a Post landing just as the loop finishes a batch
// and goes back to poll. One at a time, at varying phases, each must run.
func TestPostNeverLosesAWakeupAroundTheLoopReturningToPoll(t *testing.T) {
	conn, _ := wltest.NewConn(t)
	l, _ := startLoop(t, conn, nil)

	for i := 0; i < 3000; i++ {
		ran := make(chan struct{})
		l.Post(func() { close(ran) })
		select {
		case <-ran:
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: the posted closure never ran", i)
		}
		switch i % 4 {
		case 1:
			runtime.Gosched()
		case 2:
			time.Sleep(20 * time.Microsecond)
		case 3:
			time.Sleep(200 * time.Microsecond)
		}
	}
}

// The exact interleaving that once left the loop deaf. A Post lands between
// the loop draining the eventfd and clearing the pending flag. Done in the
// wrong order — clear, then drain — that Post sets the flag and writes the
// eventfd, the drain swallows the write, and the flag stays set with nothing
// readable: every later Post sees it, skips its write, and the loop sleeps on
// a queue with work in it. A stress test finds this one run in a dozen, so
// the test puts the Post exactly there.
func TestPostDuringTheWakeupHandshakeDoesNotLeaveTheLoopDeaf(t *testing.T) {
	conn, _ := wltest.NewConn(t)

	var injected atomic.Bool
	ranB := make(chan struct{})
	l, _ := startLoop(t, conn, func(l *Loop) {
		l.betweenDrainAndClear = func() {
			if injected.CompareAndSwap(false, true) {
				l.Post(func() { close(ranB) })
			}
		}
	})

	ranA := make(chan struct{})
	l.Post(func() { close(ranA) })
	for _, ch := range []chan struct{}{ranA, ranB} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("a closure posted around the handshake never ran")
		}
	}

	// The real check: after that, an ordinary Post must still wake the loop.
	ranC := make(chan struct{})
	l.Post(func() { close(ranC) })
	select {
	case <-ranC:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop went deaf: a later Post never woke it")
	}
}

// Messages from the compositor are dispatched with no Post involved. The
// request itself is sent from a Post, since Conn is the loop goroutine's.
func TestLoopDispatchesWhatTheCompositorSends(t *testing.T) {
	conn, comp := wltest.NewConn(t)
	l, _ := startLoop(t, conn, nil)

	got := make(chan uint32, 1)
	l.Post(func() {
		cb, err := conn.Display().Sync()
		if err != nil {
			t.Errorf("Sync: %v", err)
			return
		}
		cb.SetListener(wlcore.CallbackListener{Done: func(data uint32) { got <- data }})
	})
	comp.AnswerSync()

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the callback event was never dispatched")
	}
}

// The timer is the loop's own: with nothing to read and nothing posted,
// OnTick still fires when Deadline says it is due.
func TestOnTickFiresWhenTheDeadlineIsDue(t *testing.T) {
	conn, _ := wltest.NewConn(t)

	due := time.Now().Add(40 * time.Millisecond)
	var fired atomic.Bool
	ticked := make(chan time.Time, 4)
	startLoop(t, conn, func(l *Loop) {
		l.Deadline = func() time.Time {
			if fired.Load() {
				return time.Time{}
			}
			return due
		}
		l.OnTick = func(now time.Time) {
			fired.Store(true)
			ticked <- now
		}
	})

	select {
	case now := <-ticked:
		if now.Before(due) {
			t.Errorf("ticked at %v, before the deadline %v", now, due)
		}
		if late := now.Sub(due); late > 100*time.Millisecond {
			t.Errorf("ticked %v after the deadline", late)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnTick never fired")
	}
}

// With no deadline the loop must sleep until something happens, not spin
// and not tick.
func TestOnTickDoesNotFireWithoutADeadline(t *testing.T) {
	conn, _ := wltest.NewConn(t)

	var ticks atomic.Int64
	startLoop(t, conn, func(l *Loop) {
		l.OnTick = func(time.Time) { ticks.Add(1) }
	})
	time.Sleep(100 * time.Millisecond)

	if n := ticks.Load(); n != 0 {
		t.Errorf("OnTick fired %d times with no deadline", n)
	}
}

// An overdue timer must not starve the socket. This is the failure
// DispatchUntil has — a deadline already past skips the read — and the reason
// the loop polls instead: whatever is readable is read on the same pass that
// serves the timer.
func TestOverdueTimerDoesNotStarveTheSocket(t *testing.T) {
	conn, comp := wltest.NewConn(t)

	var overdue atomic.Bool
	overdue.Store(true)
	l, _ := startLoop(t, conn, func(l *Loop) {
		l.Deadline = func() time.Time {
			if overdue.Load() {
				return time.Now().Add(-time.Second)
			}
			return time.Time{}
		}
		// A real Tick moves the deadline on; this one does not, so the
		// timer stays overdue until the socket has been served.
		l.OnTick = func(time.Time) {}
	})

	got := make(chan struct{}, 1)
	// Posted, so that Sync runs on the loop goroutine.
	l.Post(func() {
		cb, err := conn.Display().Sync()
		if err != nil {
			t.Errorf("Sync: %v", err)
			return
		}
		cb.SetListener(wlcore.CallbackListener{Done: func(uint32) {
			overdue.Store(false)
			got <- struct{}{}
		}})
	})
	comp.AnswerSync()

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the socket starved behind an overdue timer")
	}
}

// Closing has to reach a loop that is parked in poll: closing the socket
// does not wake poll(2) on it, the eventfd does.
func TestCloseWakesALoopParkedInPoll(t *testing.T) {
	conn, _ := wltest.NewConn(t)
	l, done := startLoop(t, conn, nil)
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	l.Close()

	select {
	case err := <-done:
		if !errors.Is(err, wlcore.ErrClosed) {
			t.Errorf("Run returned %v, want ErrClosed", err)
		}
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Errorf("took %v to stop", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close never stopped the loop")
	}
}

// A compositor that goes away ends the loop with an error, not a hang.
func TestRunReturnsWhenTheCompositorHangsUp(t *testing.T) {
	conn, comp := wltest.NewConn(t)
	_, done := startLoop(t, conn, nil)
	time.Sleep(50 * time.Millisecond)

	comp.Conn().Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Run returned nil after the compositor hung up")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not notice the compositor hanging up")
	}
	select {
	case <-conn.Done():
	default:
		t.Error("the connection is not marked done after Run returned")
	}
}

// The UI goroutine keeps posting for a moment after the loop is gone. That
// must neither block, panic, nor grow a queue nobody will ever read.
func TestPostAfterRunReturnedIsDropped(t *testing.T) {
	conn, _ := wltest.NewConn(t)
	l, done := startLoop(t, conn, nil)
	l.Close()
	<-done

	finished := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			l.Post(func() { t.Error("a closure ran after Run returned") })
		}
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Post blocked after Run returned")
	}
}

func TestRunTwiceIsAnError(t *testing.T) {
	conn, _ := wltest.NewConn(t)
	l, _ := startLoop(t, conn, nil)
	time.Sleep(20 * time.Millisecond)

	if err := l.Run(); err == nil {
		t.Fatal("a second Run returned nil")
	}
}

// runWithin runs l.Run on its own goroutine and returns what it returned, or
// fails the test if it does not return in time. Run is the one call here that
// could block for good, and the point of the tests below is that it must not.
func runWithin(t *testing.T, l *Loop) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- l.Run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

// Close before Run is an orderly close, not a second Run. Whoever owns the loop
// may quit before the goroutine that runs it has got as far as Run, and what
// that goroutine has to see is the same ErrClosed an orderly Close gives a
// running loop. The eventfd is released by Close, once, and Run must not touch
// it.
func TestRunAfterCloseReturnsErrClosedAtOnce(t *testing.T) {
	conn, _ := wltest.NewConn(t)
	l, err := New(conn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Close()
	l.Close() // twice, and neither may panic or release the eventfd again

	if err := runWithin(t, l); !errors.Is(err, wlcore.ErrClosed) {
		t.Errorf("Run after Close returned %v, want ErrClosed", err)
	}
	select {
	case <-conn.Done():
	default:
		t.Error("the connection is not marked done")
	}
	l.Post(func() { t.Error("a closure ran on a loop that never started") })
}

// A Run that comes after another one is still an error, whichever way the
// first ended, and it is not ErrClosed: it is the caller's bug, not a close.
func TestASecondRunIsStillAnErrorWhateverEndedTheFirst(t *testing.T) {
	t.Run("after a Run that was closed", func(t *testing.T) {
		conn, _ := wltest.NewConn(t)
		l, done := startLoop(t, conn, nil)
		l.Close()
		<-done

		err := runWithin(t, l)
		if err == nil || errors.Is(err, wlcore.ErrClosed) {
			t.Errorf("a second Run returned %v, want the more-than-once error", err)
		}
	})
	t.Run("after a Close-before-Run", func(t *testing.T) {
		conn, _ := wltest.NewConn(t)
		l, err := New(conn)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		l.Close()
		if err := runWithin(t, l); !errors.Is(err, wlcore.ErrClosed) {
			t.Fatalf("the first Run returned %v, want ErrClosed", err)
		}
		err = runWithin(t, l)
		if err == nil || errors.Is(err, wlcore.ErrClosed) {
			t.Errorf("a second Run returned %v, want the more-than-once error", err)
		}
	})
}

func TestNewRejectsANilConn(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) returned nil error")
	}
}
