package eventloop

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Every producer's closures must run in the order that producer put them,
// and none may be lost, however many producers race.
func TestMailboxKeepsPerProducerOrderAndLosesNothing(t *testing.T) {
	const producers, perProducer = 64, 100
	m := newMailbox(nil)

	// Appended to only while running the taken closures, on this goroutine,
	// so it needs no lock.
	type entry struct{ producer, seq int }
	var ran []entry

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				m.put(func() { ran = append(ran, entry{p, i}) })
			}
		}()
	}
	wg.Wait()

	for _, fn := range m.take(nil) {
		fn()
	}

	if got := len(ran); got != producers*perProducer {
		t.Fatalf("ran %d closures, want %d", got, producers*perProducer)
	}
	next := make([]int, producers)
	for _, e := range ran {
		if e.seq != next[e.producer] {
			t.Fatalf("producer %d ran seq %d, want %d", e.producer, e.seq, next[e.producer])
		}
		next[e.producer]++
	}
}

// put is what the UI calls; if it could block on a stalled consumer, the
// UI would stall with it, which is exactly what this package exists to stop.
func TestMailboxPutNeverBlocksWithNoConsumer(t *testing.T) {
	m := newMailbox(nil)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100_000; i++ {
			m.put(func() {})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("put blocked with nobody taking")
	}
}

// A put that lands between the consumer's take and its wait must still wake
// it: the signal is a token, not an edge.
func TestMailboxSignalSurvivesAPutBetweenTakeAndWait(t *testing.T) {
	m := newMailbox(nil)

	if got := m.take(nil); len(got) != 0 {
		t.Fatalf("take on an empty mailbox returned %d closures", len(got))
	}
	m.put(func() {})

	select {
	case <-m.signal():
	case <-time.After(time.Second):
		t.Fatal("no signal for a put made after the last take")
	}
	if got := m.take(nil); len(got) != 1 {
		t.Fatalf("take returned %d closures, want 1", len(got))
	}
}

// The token must never be visible before the closure it announces. With one
// put at a time and a consumer already parked on the signal, a token sent
// too early wakes the consumer into an empty queue, and it goes back to
// sleep with the closure still to arrive. The stress test below cannot pin
// this: with many producers the consumer is rarely idle at the wrong moment.
func TestMailboxSignalIsNeverVisibleBeforeItsClosure(t *testing.T) {
	const rounds = 2000
	m := newMailbox(nil)

	acked := make(chan struct{})
	empty := make(chan int, 1)
	go func() {
		for i := 0; i < rounds; i++ {
			<-m.signal()
			if len(m.take(nil)) == 0 {
				empty <- i
				return
			}
			acked <- struct{}{}
		}
	}()

	for i := 0; i < rounds; i++ {
		m.put(func() {})
		select {
		case <-acked:
		case round := <-empty:
			t.Fatalf("round %d: the consumer was woken into an empty queue", round)
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: the consumer never woke", i)
		}
	}
}

// The stress version of the test above: a consumer that only ever waits on
// the signal must run every closure. A lost wakeup shows up as a hang.
func TestMailboxConsumerNeverMissesAWakeup(t *testing.T) {
	const total = 20_000
	m := newMailbox(nil)

	var ran atomic.Int64
	done := make(chan struct{})
	go func() {
		var batch []func()
		for ran.Load() < total {
			<-m.signal()
			batch = m.take(batch[:0])
			for _, fn := range batch {
				fn()
			}
		}
		close(done)
	}()

	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < total/8; i++ {
				m.put(func() { ran.Add(1) })
			}
		}()
	}
	wg.Wait()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("consumer stuck after %d of %d closures: a wakeup was lost", ran.Load(), total)
	}
}

func TestMailboxTakeEmptiesTheQueue(t *testing.T) {
	m := newMailbox(nil)
	m.put(func() {})
	m.put(func() {})

	if got := m.take(nil); len(got) != 2 {
		t.Fatalf("first take returned %d closures, want 2", len(got))
	}
	if got := m.take(nil); len(got) != 0 {
		t.Fatalf("second take returned %d closures, want 0", len(got))
	}
}

// take appends to dst, so a caller that passes dst[:0] reuses its backing
// array instead of allocating a slice per wakeup.
func TestMailboxTakeAppendsToDst(t *testing.T) {
	m := newMailbox(nil)
	m.put(func() {})

	dst := make([]func(), 0, 8)
	got := m.take(dst)
	if len(got) != 1 {
		t.Fatalf("take returned %d closures, want 1", len(got))
	}
	if &got[0] != &dst[:1][0] {
		t.Error("take did not reuse dst's backing array")
	}
}

// onPut is how Loop hooks its eventfd in: it has to fire once per put, and
// after the closure is already queued, or the woken side would find nothing.
func TestMailboxOnPutFiresOncePerPutAfterQueueing(t *testing.T) {
	var m *mailbox
	var calls, queuedAtCall int
	m = newMailbox(func() {
		calls++
		queuedAtCall = len(m.take(nil))
	})

	m.put(func() {})
	m.put(func() {})

	if calls != 2 {
		t.Fatalf("onPut called %d times, want 2", calls)
	}
	if queuedAtCall != 1 {
		t.Errorf("onPut saw %d queued closures, want the one just put", queuedAtCall)
	}
}

// onPut runs outside the lock: a hook that writes an eventfd or takes
// another lock must not be able to deadlock against a taker.
func TestMailboxOnPutRunsOutsideTheLock(t *testing.T) {
	var m *mailbox
	reentered := make(chan struct{})
	m = newMailbox(func() {
		m.take(nil) // would deadlock if onPut ran under m.mu
		close(reentered)
	})

	go m.put(func() {})

	select {
	case <-reentered:
	case <-time.After(time.Second):
		t.Fatal("onPut deadlocked: it ran with the mailbox lock held")
	}
}
