package pointer

import "testing"

func TestEventKindsHaveStableNames(t *testing.T) {
	tests := []struct {
		kind EventKind
		want string
	}{
		{Position, "position"},
		{ButtonDown, "button-down"},
		{ButtonUp, "button-up"},
		{Click, "click"},
		{DoubleClick, "double-click"},
		{DragStart, "drag-start"},
		{DragMove, "drag-move"},
		{DragEnd, "drag-end"},
		{EventKind(255), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
