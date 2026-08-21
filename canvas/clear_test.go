package canvas

import (
	"errors"
	"testing"
)

func TestClearReplacesEveryVisiblePixel(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.Clear(Color{R: 0x12, G: 0x34, B: 0x56, A: 0xFF})

	want := uint32(0xFF123456)
	for y := range 4 {
		for x := range 6 {
			if got := at(c, x, y); got != want {
				t.Fatalf("pixel (%d,%d) = %#08x, want %#08x", x, y, got, want)
			}
		}
	}
}

func TestClearWithTransparentProducesZero(t *testing.T) {
	// The distinguishing test: source-over with a transparent source would
	// leave the buffer untouched. Clear must zero it.
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.Clear(Color{})

	for y := range 4 {
		for x := range 4 {
			if got := at(c, x, y); got != 0x00000000 {
				t.Fatalf("pixel (%d,%d) = %#08x, want 0x00000000", x, y, got)
			}
		}
	}
}

func TestClearNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 32)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.Clear(Color{R: 255, A: 255})

	if !paddingIntact(c, sentinel) {
		t.Error("Clear wrote into the row padding")
	}
}

func TestClearDamagesTheWholeVisibleRegion(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 32)
	c.Clear(Color{})
	dmg, ok := c.Damage()
	if !ok {
		t.Fatal("Damage() not-ok after Clear")
	}
	want := PixelRect{X: 0, Y: 0, Width: 6, Height: 4}
	if dmg != want {
		t.Errorf("Damage() = %+v, want %+v (visible region, not the stride)", dmg, want)
	}
}

func TestClearIsNoOpOnPoisonedCanvas(t *testing.T) {
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFF000000)
	c.FillRect(Rect{Width: -1, Height: 1}, Color{A: 255})
	c.Clear(Color{R: 255, G: 255, B: 255, A: 255})

	if got := at(c, 0, 0); got != 0xFF000000 {
		t.Errorf("Clear ran on a poisoned canvas: (0,0) = %#08x", got)
	}
}

func TestClearRectReplacesWholePixels(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.ClearRect(Rect{X: 2, Y: 2, Width: 3, Height: 3}, Color{})

	for y := range 8 {
		for x := range 8 {
			want := uint32(0xFFFFFFFF)
			if x >= 2 && x < 5 && y >= 2 && y < 5 {
				want = 0x00000000
			}
			if got := at(c, x, y); got != want {
				t.Fatalf("pixel (%d,%d) = %#08x, want %#08x", x, y, got, want)
			}
		}
	}
}

func TestClearRectInterpolatesAtSubPixelBoundaries(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	// The left edge lands mid-pixel in column 2.
	c.ClearRect(Rect{X: 2.5, Y: 0, Width: 2, Height: 8}, Color{})

	partial := at(c, 2, 0)
	a := partial >> 24 & 0xFF
	if a < 120 || a > 136 {
		t.Errorf("half-cleared pixel alpha = %d, want ~128 (lerp, not source-over)", a)
	}
	if got := at(c, 3, 0); got != 0x00000000 {
		t.Errorf("fully cleared pixel = %#08x, want 0x00000000", got)
	}
	if got := at(c, 5, 0); got != 0xFFFFFFFF {
		t.Errorf("untouched pixel = %#08x, want white", got)
	}
}

func TestClearRectWithTransparentColorStillWrites(t *testing.T) {
	// Unlike FillRect, a zero-alpha ClearRect is not a no-op: erasing to
	// transparent is the operation's whole purpose.
	c := newTestCanvas(t, 4, 4, 1, 0)
	fillAll(c, 0xFFFFFFFF)
	c.ClearRect(Rect{X: 0, Y: 0, Width: 4, Height: 4}, Color{})

	if got := at(c, 0, 0); got != 0x00000000 {
		t.Errorf("(0,0) = %#08x, want 0x00000000", got)
	}
	if _, ok := c.Damage(); !ok {
		t.Error("a transparent ClearRect did not extend the damage")
	}
}

func TestClearRectAppliesScaleAndClips(t *testing.T) {
	c := newTestCanvas(t, 8, 8, 2, 0) // 16x16 physical
	fillAll(c, 0xFFFFFFFF)
	c.ClearRect(Rect{X: -2, Y: -2, Width: 4, Height: 4}, Color{})

	if err := c.Err(); err != nil {
		t.Fatalf("a partially offscreen ClearRect is not an error: %v", err)
	}
	if got := at(c, 0, 0); got != 0x00000000 {
		t.Errorf("(0,0) = %#08x, want cleared", got)
	}
	if got := at(c, 4, 4); got != 0xFFFFFFFF {
		t.Errorf("(4,4) = %#08x, want untouched", got)
	}
	dmg, _ := c.Damage()
	want := PixelRect{X: 0, Y: 0, Width: 4, Height: 4}
	if dmg != want {
		t.Errorf("Damage() = %+v, want %+v", dmg, want)
	}
}

func TestClearRectNoOpsAndErrors(t *testing.T) {
	t.Run("zero width is a no-op", func(t *testing.T) {
		c := newTestCanvas(t, 4, 4, 1, 0)
		fillAll(c, 0xFFFFFFFF)
		c.ClearRect(Rect{X: 1, Y: 1, Width: 0, Height: 2}, Color{})
		if err := c.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
		if _, ok := c.Damage(); ok {
			t.Error("a no-op extended the damage")
		}
		if got := at(c, 1, 1); got != 0xFFFFFFFF {
			t.Errorf("a no-op wrote (1,1) = %#08x", got)
		}
	})

	t.Run("negative height is an error", func(t *testing.T) {
		c := newTestCanvas(t, 4, 4, 1, 0)
		fillAll(c, 0xFFFFFFFF)
		c.ClearRect(Rect{X: 1, Y: 1, Width: 2, Height: -2}, Color{})
		err := c.Err()
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Err() = %v, want ErrInvalidArgument", err)
		}
		want := `canvas: ClearRect: invalid argument "rect.Height": must not be negative (got -2)`
		if err.Error() != want {
			t.Errorf("Err() =\n  %q\nwant\n  %q", err, want)
		}
		if got := at(c, 1, 1); got != 0xFFFFFFFF {
			t.Error("an invalid ClearRect modified the buffer")
		}
	})
}

func TestClearRectNeverTouchesPadding(t *testing.T) {
	c := newTestCanvas(t, 6, 4, 1, 32)
	const sentinel = 0xCAFEBABE
	fillPadding(c, sentinel)
	c.ClearRect(Rect{X: 0, Y: 0, Width: 6, Height: 4}, Color{})
	if !paddingIntact(c, sentinel) {
		t.Error("ClearRect wrote into the row padding")
	}
}
