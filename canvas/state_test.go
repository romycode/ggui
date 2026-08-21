package canvas

import (
	"errors"
	"testing"
)

func TestErrIsNilOnFreshCanvas(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	if err := c.Err(); err != nil {
		t.Errorf("Err() on a fresh canvas = %v, want nil", err)
	}
}

func TestFailKeepsTheFirstError(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	first := invalidArg("FillRect", "rect.Width", "must not be negative (got -1)")
	second := invalidArg("FillCircle", "radius", "must not be negative (got -2)")

	c.fail(first)
	c.fail(second)

	if got := c.Err(); got != first {
		t.Errorf("Err() = %v, want the first error %v", got, first)
	}
	if !errors.Is(c.Err(), ErrInvalidArgument) {
		t.Error("stored error no longer matches ErrInvalidArgument")
	}
}

func TestFailedCanvasIsPoisonedForever(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.fail(invalidArg("FillRect", "rect.Width", "must not be negative (got -1)"))
	if !c.failed() {
		t.Fatal("failed() = false after fail()")
	}
	// There is no API to clear it: confirm the accessor keeps reporting.
	if c.Err() == nil {
		t.Error("Err() went back to nil")
	}
}

func TestDamageEmptyOnFreshCanvas(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	if _, ok := c.Damage(); ok {
		t.Error("Damage() reported ok on a canvas that has not been written")
	}
}

func TestAddDamageUnionsRectangles(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.addDamage(PixelRect{X: 2, Y: 3, Width: 4, Height: 4}) // x 2..6, y 3..7
	c.addDamage(PixelRect{X: 8, Y: 1, Width: 2, Height: 2}) // x 8..10, y 1..3

	got, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() reported not-ok after two writes")
	}
	want := PixelRect{X: 2, Y: 1, Width: 8, Height: 6} // x 2..10, y 1..7
	if got != want {
		t.Errorf("Damage() = %+v, want %+v", got, want)
	}
}

func TestAddDamageIgnoresEmptyRectangles(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.addDamage(PixelRect{X: 5, Y: 5, Width: 0, Height: 3})
	c.addDamage(PixelRect{X: 5, Y: 5, Width: 3, Height: 0})
	if _, ok := c.Damage(); ok {
		t.Error("empty rectangles extended the damage")
	}
}

func TestResetDamage(t *testing.T) {
	c := newTestCanvas(t, 16, 16, 1, 0)
	c.addDamage(PixelRect{X: 1, Y: 1, Width: 2, Height: 2})
	c.ResetDamage()
	if _, ok := c.Damage(); ok {
		t.Error("Damage() still reports ok after ResetDamage")
	}
	// Accumulation restarts cleanly rather than resurrecting the old box.
	c.addDamage(PixelRect{X: 10, Y: 10, Width: 1, Height: 1})
	got, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok after reset and a new write")
	}
	want := PixelRect{X: 10, Y: 10, Width: 1, Height: 1}
	if got != want {
		t.Errorf("Damage() = %+v, want %+v", got, want)
	}
}

func TestClipRect(t *testing.T) {
	c := newTestCanvas(t, 20, 10, 1, 0) // 20x10 physical
	cases := []struct {
		name           string
		x0, y0, x1, y1 float32
		want           PixelRect
		wantOK         bool
	}{
		{"whole pixels", 2, 3, 6, 7, PixelRect{2, 3, 4, 4}, true},
		{"fractional expands outward", 2.3, 3.7, 5.1, 6.2, PixelRect{2, 3, 4, 4}, true},
		{"clipped left and top", -5, -5, 3, 3, PixelRect{0, 0, 3, 3}, true},
		{"clipped right and bottom", 18, 8, 40, 40, PixelRect{18, 8, 2, 2}, true},
		{"entirely left", -20, 0, -1, 10, PixelRect{}, false},
		{"entirely right", 25, 0, 40, 10, PixelRect{}, false},
		{"entirely above", 0, -20, 20, -1, PixelRect{}, false},
		{"entirely below", 0, 12, 20, 30, PixelRect{}, false},
		{"empty box", 5, 5, 5, 5, PixelRect{}, false},
		{"inverted box", 8, 8, 2, 2, PixelRect{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := c.clipRect(tc.x0, tc.y0, tc.x1, tc.y1)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Errorf("clipRect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestClipRectNeverLeavesVisibleRegion(t *testing.T) {
	c := newTestCanvas(t, 20, 10, 1, 64) // stride 64, visible 20x10
	got, ok := c.clipRect(-1000, -1000, 1000, 1000)
	if !ok {
		t.Fatal("clipRect over the whole plane reported not-ok")
	}
	if got.X < 0 || got.Y < 0 || got.X+got.Width > c.PixelWidth() || got.Y+got.Height > c.PixelHeight() {
		t.Errorf("clipRect = %+v, outside the visible %dx%d region", got, c.PixelWidth(), c.PixelHeight())
	}
}
