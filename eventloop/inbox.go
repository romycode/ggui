package eventloop

import (
	"sync"

	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
)

// maxPendingRepeats bounds how many key repeats wait in an Inbox for the UI.
// A held key repeats a few dozen times a second, so 64 is a couple of
// seconds of backlog: past that the UI is not keeping up and the extra
// repeats are not worth delivering late.
const maxPendingRepeats = 64

// Inbox carries events from the Wayland goroutine to the UI goroutine.
//
// Push never blocks. The Wayland goroutine calls it from inside a dispatch,
// so a Push that waited on a busy UI would stop the socket from being read,
// the ping from being answered and every other object's events from being
// delivered — the coupling this package exists to remove.
//
// What Push does instead of waiting is decide what is worth keeping, and the
// rule is about what the event means to the user:
//
//   - Pointer motion is a stream of positions where only the newest matters,
//     so a Position or DragMove that arrives while the previous event is
//     also one replaces it.
//   - Key repeats are synthesized, not typed, and are bounded at
//     [maxPendingRepeats] pending. Past that the new ones are dropped.
//   - Everything else is something the user did or the compositor decided,
//     and is never dropped, merged or reordered.
//
// Coalescing only ever looks at the newest pending event. Merging across
// another event would move motion past whatever came between the two.
type Inbox struct {
	mu      sync.Mutex
	queue   []Event
	repeats int // EvKey events with State Repeated currently in queue

	// sig holds at most one token, meaning "the queue may be non-empty".
	sig chan struct{}
}

// NewInbox returns an empty Inbox.
func NewInbox() *Inbox {
	return &Inbox{sig: make(chan struct{}, 1)}
}

// Push queues ev for the UI. It is safe from any goroutine and never blocks.
func (in *Inbox) Push(ev Event) {
	in.mu.Lock()
	in.push(ev)
	in.mu.Unlock()

	// After the unlock and after the event is queued, in that order: the
	// token must not be visible before what it announces. Sent even when ev
	// was merged or dropped — the queue is non-empty then too, and a spare
	// token only costs the consumer one empty Drain.
	select {
	case in.sig <- struct{}{}:
	default:
	}
}

// push applies the queueing policy. The lock is held.
func (in *Inbox) push(ev Event) {
	switch ev.Kind {
	case EvPointer:
		if k := ev.Pointer.Kind; k == pointer.Position || k == pointer.DragMove {
			if n := len(in.queue); n > 0 {
				last := &in.queue[n-1]
				if last.Kind == EvPointer && last.Pointer.Kind == k {
					*last = ev
					return
				}
			}
		}
	case EvKey:
		if ev.Key.State == keyboard.Repeated {
			if in.repeats >= maxPendingRepeats {
				return
			}
			in.repeats++
		}
	}
	in.queue = append(in.queue, ev)
}

// Drain appends every pending event to dst, oldest first, empties the Inbox
// and returns the extended slice. A consumer that passes its previous batch
// as dst[:0] reuses one backing array instead of allocating per wakeup.
//
// It does not wait: on an empty Inbox it returns dst unchanged.
func (in *Inbox) Drain(dst []Event) []Event {
	in.mu.Lock()
	dst = append(dst, in.queue...)
	// Zeroed before the length is reset so the slots past it do not keep
	// the strings and object pointers of events the UI already has.
	clear(in.queue)
	in.queue = in.queue[:0]
	in.repeats = 0
	in.mu.Unlock()
	return dst
}

// Signal returns the channel that holds a token whenever a Push has happened
// since the consumer last received from it. A receive can be spurious, so
// the consumer drains after every one and treats an empty result as nothing
// to do.
func (in *Inbox) Signal() <-chan struct{} { return in.sig }
