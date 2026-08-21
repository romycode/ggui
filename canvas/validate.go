package canvas

import "fmt"

// finite rejects NaN and infinities. Every float32 argument goes through it
// before anything is scaled, because a non-finite value poisons the whole
// bounding-box computation downstream.
func finite(op, arg string, v float32) error {
	if !isFinite32(v) {
		return invalidArg(op, arg, fmt.Sprintf("must be finite (got %v)", v))
	}
	return nil
}

// nonNegative rejects values below zero. Zero itself is never an error: it
// is the documented no-op for dimensions, radii and stroke widths, so a
// theme with radius 0 does not force a branch at the call site.
func nonNegative(op, arg string, v float32) error {
	if v < 0 {
		return invalidArg(op, arg, fmt.Sprintf("must not be negative (got %v)", v))
	}
	return nil
}

// measure is the pair of checks every radius and stroke width needs.
func measure(op, arg string, v float32) error {
	if err := finite(op, arg, v); err != nil {
		return err
	}
	return nonNegative(op, arg, v)
}

// validRect checks all four fields of a logical rectangle. Field names in
// the error match what the caller wrote, so "rect.Width", not "width".
func validRect(op string, r Rect) error {
	if err := finite(op, "rect.X", r.X); err != nil {
		return err
	}
	if err := finite(op, "rect.Y", r.Y); err != nil {
		return err
	}
	if err := measure(op, "rect.Width", r.Width); err != nil {
		return err
	}
	return measure(op, "rect.Height", r.Height)
}

// validPoint checks both coordinates of a point. arg is the parameter name
// as declared ("from", "to", "center"), and the error names the component.
func validPoint(op, arg string, p Point) error {
	if err := finite(op, arg+".X", p.X); err != nil {
		return err
	}
	return finite(op, arg+".Y", p.Y)
}
