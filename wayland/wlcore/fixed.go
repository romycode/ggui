package wlcore

import "math"

// Fixed is a signed 24.8 fixed-point number, packed into an int32 — the
// format Wayland uses for "fixed" arguments on the wire. math.Round,
// not truncation: truncating systematically biases toward zero.
type Fixed int32

func (f Fixed) Float64() float64 { return float64(f) / 256.0 }

func FixedFromFloat64(v float64) Fixed { return Fixed(math.Round(v * 256.0)) }
