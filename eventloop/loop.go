package eventloop

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/wlcore"
)

// The states of [Loop.state].
const (
	// loopIdle: neither Run nor Close has claimed the loop.
	loopIdle int32 = iota
	// loopRunning: Run has claimed it, and has returned or will.
	loopRunning
	// loopClosedFirst: Close claimed it before Run did, and released the
	// eventfd. The Run that comes next reports the close.
	loopClosedFirst
	// loopClosedReported: that Run has reported it, so one more is a second
	// Run like any other.
	loopClosedReported
)

// Loop is the Wayland goroutine: it waits on the connection's socket and on
// an eventfd at once, so it wakes for a message from the compositor, for a
// closure another goroutine [Loop.Post]ed, and for its own timer.
//
// The goroutine that calls [Loop.Run] becomes the one goroutine that owns the
// Conn. Everything that touches the connection — dispatching, the requests
// the generated code sends, creating and destroying objects — happens there,
// either because the loop dispatched it or because it was posted.
//
// Post and Close are the only methods safe to call from another goroutine.
// Deadline and OnTick are read and called from the loop goroutine, and must
// be set before Run.
type Loop struct {
	// Deadline returns when the loop must wake for a timer of its own, or
	// the zero time when it needs none. It is asked before every wait, so
	// it can change whenever what it depends on does: a key press arms a
	// repeat, a release cancels it. Nil means never.
	//
	// It is meant to be [keyboard.Keyboard.NextRepeat].
	Deadline func() time.Time
	// OnTick is called when Deadline has passed, with the current time. It
	// must move the deadline forward or clear it: a deadline that stays in
	// the past makes the loop spin. Nil ignores it.
	//
	// It is meant to be [keyboard.Keyboard.Tick].
	OnTick func(now time.Time)

	conn   *wlcore.Conn
	sockFD int
	wakeFD int
	mb     *mailbox

	// pending is true from the first Post after a wakeup was consumed until
	// the loop consumes the next one. It exists so that a burst of Posts
	// costs one write to the eventfd, not one each.
	pending atomic.Bool
	// state says who has claimed the loop: Run, or Close when the loop never
	// ran, so that the eventfd is released exactly once and Run cannot start
	// after. A single word, because "Close got there first" and "Run already
	// ran" have to be told apart by one atomic step: two flags would leave a
	// moment in which Run sees the claim and not yet its reason.
	state atomic.Int32

	// betweenDrainAndClear is a test seam, nil in real use. It runs inside
	// takePosted, between the first two steps of the wakeup handshake, which
	// is the one place a Post can land that a stress test hits only one run
	// in a dozen.
	betweenDrainAndClear func()

	// mu makes closing the eventfd safe against a Post that is about to
	// write to it. Writing to a descriptor that another goroutine has just
	// closed can hit whatever the kernel handed that number to next, so the
	// check for closed and the write are one critical section, taken shared.
	mu     sync.RWMutex
	closed bool
}

// New returns a Loop for conn. Nothing runs until [Loop.Run].
//
// The loop owns an eventfd from here on. It is released when Run returns,
// or by [Loop.Close] if Run was never called.
func New(conn *wlcore.Conn) (*Loop, error) {
	if conn == nil {
		return nil, errors.New("eventloop: nil conn")
	}
	rc, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("eventloop: socket: %w", err)
	}
	sockFD := -1
	if err := rc.Control(func(fd uintptr) { sockFD = int(fd) }); err != nil {
		return nil, fmt.Errorf("eventloop: socket: %w", err)
	}
	wakeFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("eventloop: eventfd: %w", err)
	}

	l := &Loop{conn: conn, sockFD: sockFD, wakeFD: wakeFD}
	l.mb = newMailbox(l.wake)
	return l, nil
}

// Post queues fn to run on the loop goroutine, after everything already
// posted, and wakes the loop if it is waiting. It is safe from any
// goroutine and never blocks.
//
// This is how the UI talks to the compositor: a request is a closure that
// makes it. Closures posted after [Loop.Run] has returned are dropped, so a
// UI that is a moment slower to notice the shutdown neither blocks nor
// piles up work nobody will run.
func (l *Loop) Post(fn func()) {
	if fn == nil {
		return
	}
	l.mu.RLock()
	if !l.closed {
		l.mb.put(fn) // calls wake, still under the read lock
	}
	l.mu.RUnlock()
}

// wake makes the eventfd readable. The caller holds l.mu for reading, so the
// descriptor cannot be closed underneath the write.
func (l *Loop) wake() {
	if !l.pending.CompareAndSwap(false, true) {
		return // a wakeup is already on its way and will find our closure
	}
	if err := writeWake(func(p []byte) (int, error) {
		return unix.Write(l.wakeFD, p)
	}); err != nil {
		// Under the read lock the descriptor cannot be closed, so this is an
		// unexpected write failure. Clear the coalescing bit so a later Post
		// can retry instead of leaving the queue permanently deaf.
		l.pending.Store(false)
	}
}

// writeWake writes one eventfd token. EINTR is transparent to callers and
// EAGAIN means the eventfd is already readable, so both preserve the wakeup
// guarantee. The writer is injected so the retry policy can be tested without
// depending on signal timing.
func writeWake(write func([]byte) (int, error)) error {
	var one [8]byte
	binary.NativeEndian.PutUint64(one[:], 1)
	for {
		n, err := write(one[:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) {
			return nil
		}
		if err != nil {
			return err
		}
		if n != len(one) {
			return io.ErrShortWrite
		}
		return nil
	}
}

// Close ends the connection and wakes the loop so that [Loop.Run] notices
// and returns [wlcore.ErrClosed]. It is safe from any goroutine, and from
// one that is not the loop's: closing the socket here, rather than asking the
// loop to do it, is what breaks a loop stuck in a write to a compositor that
// stopped reading.
//
// Close may come before Run. Then it releases the eventfd itself, and the
// first Run that follows returns at once with the connection's terminal
// error, [wlcore.ErrClosed] after this orderly close, having done nothing. It
// is a close, not a second Run: only a Run that comes after that one, or
// after a Run that already started, is refused.
//
// Prefer it to closing the Conn directly from another goroutine: closing
// the socket does not wake a poll waiting on it, so the loop would sleep
// until something else happened.
func (l *Loop) Close() {
	l.conn.Close()
	if l.state.CompareAndSwap(loopIdle, loopClosedFirst) {
		l.release() // Run was never called, so nobody else will
		return
	}
	l.mu.RLock()
	if !l.closed {
		l.wake()
	}
	l.mu.RUnlock()
}

// Run is the Wayland goroutine's loop. It blocks until the connection ends
// and returns the reason: [wlcore.ErrClosed] after an orderly [Loop.Close],
// otherwise what took the connection down. Run may be called only once, and a
// second call returns an error saying so. The exception is a Run that comes
// after [Loop.Close] was called on a loop that never ran: that one is the close
// itself, and returns the connection's terminal error at once, [wlcore.ErrClosed]
// after an orderly close.
//
// One pass through the loop does three things, in this order:
//
//  1. runs the closures posted since the last pass;
//  2. dispatches what the compositor sent, if the socket is readable;
//  3. calls OnTick if Deadline has passed.
//
// Dispatching before the tick is what lets a key release that arrives in
// the same pass as a due repeat cancel it. An overdue timer never starves
// the socket, because all three run every pass: this is the difference from
// waiting with [wlcore.Conn.DispatchUntil], whose read is skipped once the
// deadline has passed.
//
// Run ends by closing the connection and dropping every closure still
// queued.
func (l *Loop) Run() error {
	if !l.state.CompareAndSwap(loopIdle, loopRunning) {
		if l.state.CompareAndSwap(loopClosedFirst, loopClosedReported) {
			// Close already released the eventfd and closed the connection, so
			// there is nothing to run and nothing to release; what is left is to
			// say why. Close recorded ErrClosed unless the connection had already
			// ended for another reason, and that reason is the truer answer.
			if err := l.conn.Err(); err != nil {
				return err
			}
			return wlcore.ErrClosed
		}
		return errors.New("eventloop: Run called more than once")
	}
	// Deferred in reverse: the connection closes, its pending fds are
	// dropped, and only then is the eventfd released.
	defer l.release()
	defer l.conn.DrainFDs()
	defer l.conn.Close()

	fds := [2]unix.PollFd{
		{Fd: int32(l.sockFD), Events: unix.POLLIN},
		{Fd: int32(l.wakeFD), Events: unix.POLLIN},
	}
	var batch []func()

	for {
		select {
		case <-l.conn.Done():
			return l.conn.Err()
		default:
		}

		timeout := -1
		if d := l.deadline(); !d.IsZero() {
			timeout = pollTimeout(time.Until(d))
		}
		fds[0].Revents, fds[1].Revents = 0, 0
		if _, err := unix.Poll(fds[:], timeout); err != nil {
			if err == unix.EINTR {
				continue // a signal, most likely the runtime's own preemption
			}
			return fmt.Errorf("eventloop: poll: %w", err)
		}

		if fds[1].Revents != 0 {
			batch = l.takePosted(batch[:0])
			for _, fn := range batch {
				fn()
			}
			clear(batch) // do not keep what the closures captured
		}

		// A closure may have closed the connection; dispatching on it would
		// only turn an orderly close into a read error.
		select {
		case <-l.conn.Done():
			return l.conn.Err()
		default:
		}

		if fds[0].Revents != 0 {
			if err := l.conn.Dispatch(); err != nil {
				return err
			}
		}

		// Asked again rather than reused from before the wait: dispatching
		// may just have armed a repeat or cancelled one.
		if d := l.deadline(); l.OnTick != nil && !d.IsZero() {
			if now := time.Now(); !now.Before(d) {
				l.OnTick(now)
			}
		}
	}
}

func (l *Loop) deadline() time.Time {
	if l.Deadline == nil {
		return time.Time{}
	}
	return l.Deadline()
}

// takePosted consumes the wakeup and returns the closures queued so far.
//
// The order is the whole point: drain the eventfd, then clear pending, then
// take the queue. What has to hold is that pending is never left true with
// the eventfd empty and no Post on its way to write it, because then every
// later Post sees pending set, skips its write, and the loop sleeps on a
// queue that has work in it.
//
//   - Between the drain and the clear, pending is still true, so every Post
//     skips its write. That is safe: its closure was queued before it looked
//     at pending, and the take comes after the clear.
//   - After the clear, a Post that sets pending goes on to write, and that
//     write comes after our drain, so nothing swallows it. At worst the loop
//     wakes once more to an empty queue.
//
// Clearing pending before draining looks equivalent and is not: a Post that
// slips in between sets pending and writes, the drain swallows the write,
// and pending stays true with nothing left to wake the loop.
func (l *Loop) takePosted(dst []func()) []func() {
	var buf [8]byte
	unix.Read(l.wakeFD, buf[:]) // EAGAIN just means it was already drained
	if l.betweenDrainAndClear != nil {
		l.betweenDrainAndClear()
	}
	l.pending.Store(false)
	return l.mb.take(dst)
}

// release closes the eventfd and drops queued closures. After it, Post is a
// no-op. The lock is taken exclusively so that no Post is mid-write.
func (l *Loop) release() {
	l.mu.Lock()
	l.closed = true
	unix.Close(l.wakeFD)
	l.mu.Unlock()

	l.mb.take(nil)
}

// pollTimeout converts a wait to poll's milliseconds. It rounds up: a wait
// truncated to the millisecond below would wake just before the deadline,
// find it not yet passed, and go round again for the remaining fraction.
func pollTimeout(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	ms := (d + time.Millisecond - 1) / time.Millisecond
	if ms > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(ms)
}
