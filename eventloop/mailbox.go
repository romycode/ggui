package eventloop

import "sync"

// mailbox is a FIFO of closures that any goroutine may put into and one
// goroutine takes from. It is the only thing in this package that two
// goroutines touch at once, so it carries its own lock; everything on either
// side of it stays single-goroutine.
//
// put never blocks and never fails: a consumer that is stalled, busy or gone
// costs memory, not the producer's time. That asymmetry is the point. The
// producer is the UI or the Wayland goroutine, and neither may wait on the
// other.
//
// The consumer learns there is work either from signal, a channel it can
// select on, or from the onPut hook given to newMailbox, which is how a
// consumer parked in poll(2) gets woken by writing an eventfd. Both fire on
// every put; a consumer uses whichever suits how it waits.
type mailbox struct {
	mu    sync.Mutex
	queue []func()

	// sig holds at most one token. It says "the queue may be non-empty", not
	// how many closures are in it, so a put that finds the token already
	// there has nothing to add: the consumer will take everything queued
	// before it looks again.
	sig   chan struct{}
	onPut func()
}

// newMailbox returns an empty mailbox. onPut, if non-nil, is called after
// every put, once the closure is queued and with the lock released, so a
// hook may take other locks or call take without deadlocking. It is fixed
// for the mailbox's life, which is what lets put read it without the lock.
func newMailbox(onPut func()) *mailbox {
	return &mailbox{sig: make(chan struct{}, 1), onPut: onPut}
}

// put queues fn behind everything already queued. It is safe from any
// goroutine and never blocks. Closures put by one goroutine run in the order
// that goroutine put them; between goroutines there is no order to promise.
func (m *mailbox) put(fn func()) {
	m.mu.Lock()
	m.queue = append(m.queue, fn)
	m.mu.Unlock()

	// After the append and after the unlock, in that order. The token has to
	// be visible only once the closure is, or a consumer woken by it could
	// take an empty queue and go back to sleep with the closure still to
	// come.
	select {
	case m.sig <- struct{}{}:
	default:
	}
	if m.onPut != nil {
		m.onPut()
	}
}

// take appends every queued closure to dst, empties the queue and returns
// the extended slice. A consumer that passes its previous batch as dst[:0]
// reuses one backing array for the life of the loop instead of allocating a
// slice per wakeup.
//
// It does not wait: on an empty mailbox it returns dst unchanged. Waiting is
// the consumer's business, on signal or on whatever onPut wakes.
func (m *mailbox) take(dst []func()) []func() {
	m.mu.Lock()
	dst = append(dst, m.queue...)
	// Zeroed before the length is reset, or the slots past it would keep
	// every taken closure, and whatever it captured, reachable until the
	// queue grows back over them.
	clear(m.queue)
	m.queue = m.queue[:0]
	m.mu.Unlock()
	return dst
}

// signal returns the channel that holds a token whenever a put has happened
// since the consumer last received from it. A receive can be spurious — the
// queue may already have been taken by then — so the consumer takes after
// every receive and treats an empty result as nothing to do.
func (m *mailbox) signal() <-chan struct{} { return m.sig }
