package pointer

// EventKind identifies a semantic pointer event.
type EventKind uint8

const (
	// Position reports a new surface-local pointer position.
	Position EventKind = iota
	// ButtonDown reports a physical button press.
	ButtonDown
	// ButtonUp reports a physical button release.
	ButtonUp
	// Click reports a press and release that did not become a drag.
	Click
	// DoubleClick reports a second compatible click.
	DoubleClick
	// DragStart reports the first motion beyond the drag threshold.
	DragStart
	// DragMove reports motion after a drag has started.
	DragMove
	// DragEnd reports the button release that ends a drag.
	DragEnd
)

// String returns the event kind's stable name.
func (k EventKind) String() string {
	switch k {
	case Position:
		return "position"
	case ButtonDown:
		return "button-down"
	case ButtonUp:
		return "button-up"
	case Click:
		return "click"
	case DoubleClick:
		return "double-click"
	case DragStart:
		return "drag-start"
	case DragMove:
		return "drag-move"
	case DragEnd:
		return "drag-end"
	default:
		return "unknown"
	}
}

// Event is one semantic pointer event in surface-local logical units.
// Fields that do not apply to Kind have their zero value.
type Event struct {
	// Kind identifies what happened.
	Kind EventKind
	// X and Y are the current surface-local position.
	X, Y float32
	// StartX and StartY are the origin of a drag.
	StartX, StartY float32
	// Button is the Linux input button code involved in the event.
	Button uint32
	// Serial ties a button interaction to the Wayland request that caused it.
	Serial uint32
	// Time is the compositor timestamp in milliseconds.
	Time uint32
}
