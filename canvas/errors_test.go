package canvas

import (
	"errors"
	"testing"
)

func TestInvalidArgMessage(t *testing.T) {
	cases := []struct {
		op, arg, detail string
		want            string
	}{
		{
			"New", "buffer.Stride", "must be at least buffer.Width (got 100)",
			`canvas: New: invalid argument "buffer.Stride": must be at least buffer.Width (got 100)`,
		},
		{
			"New", "buffer.Pixels", "buffer is too short (need 4096, got 4000)",
			`canvas: New: invalid argument "buffer.Pixels": buffer is too short (need 4096, got 4000)`,
		},
		{
			"New", "scale", "must be finite and greater than zero (got 0)",
			`canvas: New: invalid argument "scale": must be finite and greater than zero (got 0)`,
		},
		{
			"FillCircle", "radius", "must not be negative (got -3)",
			`canvas: FillCircle: invalid argument "radius": must not be negative (got -3)`,
		},
		{
			"Line", "cap", "unknown LineCap(4)",
			`canvas: Line: invalid argument "cap": unknown LineCap(4)`,
		},
	}
	for _, c := range cases {
		got := invalidArg(c.op, c.arg, c.detail).Error()
		if got != c.want {
			t.Errorf("invalidArg(%q, %q, %q) =\n  %q\nwant\n  %q", c.op, c.arg, c.detail, got, c.want)
		}
	}
}

func TestInvalidArgIsErrInvalidArgument(t *testing.T) {
	err := invalidArg("FillRect", "rect.Width", "must not be negative (got -1)")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("invalidArg result does not match ErrInvalidArgument")
	}
}

func TestLineCapConstants(t *testing.T) {
	// The zero value must be LineCapButt: a zero-valued LineCap in a
	// caller's struct literal has to mean the plainest cap, not an error.
	var zero LineCap
	if zero != LineCapButt {
		t.Errorf("zero LineCap = %d, want LineCapButt (%d)", zero, LineCapButt)
	}
	if LineCapSquare != 1 || LineCapRound != 2 {
		t.Errorf("LineCap constants moved: Square=%d Round=%d, want 1 and 2", LineCapSquare, LineCapRound)
	}
}
