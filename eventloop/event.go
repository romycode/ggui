package eventloop

import (
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/wayland/wlcore"
)

// EventKind says which fields of an [Event] mean anything.
type EventKind uint8

const (
	// EvKey carries a key press, release or repeat in Key.
	EvKey EventKind = iota
	// EvPointer carries a semantic pointer event in Pointer.
	EvPointer
	// EvKeyboardFocus reports keyboard focus entering Surface, or leaving
	// it when Surface is nil.
	EvKeyboardFocus
	// EvPointerFocus reports pointer focus entering Surface, or leaving it
	// when Surface is nil.
	EvPointerFocus
	// EvConfigure reports the size the compositor wants, in Width and
	// Height. Zero in either means "you decide".
	EvConfigure
	// EvScale reports the physical scale the compositor wants, in Scale.
	// Like EvConfigure it is something the compositor decided, not a stream
	// of samples, so it is never merged with another.
	EvScale
	// EvBufferRelease reports that the compositor is done reading Buffer
	// and it may be drawn into again.
	EvBufferRelease
	// EvFrameDone reports that the compositor is ready for another frame.
	// Time is its millisecond timestamp, the clock an animation runs on.
	EvFrameDone
	// EvClosed reports that the connection has ended. It is always the last
	// event.
	EvClosed
)

// String returns the kind's name, for logs and test failures.
func (k EventKind) String() string {
	switch k {
	case EvKey:
		return "key"
	case EvPointer:
		return "pointer"
	case EvKeyboardFocus:
		return "keyboard-focus"
	case EvPointerFocus:
		return "pointer-focus"
	case EvConfigure:
		return "configure"
	case EvScale:
		return "scale"
	case EvBufferRelease:
		return "buffer-release"
	case EvFrameDone:
		return "frame-done"
	case EvClosed:
		return "closed"
	}
	return "unknown"
}

// Event is one thing the Wayland goroutine has to tell the UI. Kind picks
// which of the other fields are set; the rest are zero.
//
// It is a value, and copying it copies everything the UI needs. The one
// exception is the pointers to wlcore objects, which are there so the UI can
// tell one surface or buffer from another. The UI must compare them and
// nothing else: calling a method on one would touch the connection from the
// wrong goroutine.
type Event struct {
	// Kind picks which of the fields below mean anything.
	Kind EventKind

	// Key is set for EvKey.
	Key keyboard.Event
	// Pointer is set for EvPointer.
	Pointer pointer.Event

	// Surface is set for EvKeyboardFocus and EvPointerFocus. Identity only.
	Surface *wlcore.Surface
	// Buffer is set for EvBufferRelease. Identity only.
	Buffer *wlcore.Buffer

	// Width and Height are set for EvConfigure.
	Width, Height int32
	// Scale is set for EvScale: physical pixels per logical unit.
	Scale float32
	// Time is set for EvFrameDone.
	Time uint32
}
