package wlcore

import "math"

// Fixed is a signed 24.8 fixed-point number, packed into an int32 — the
// format Wayland uses for "fixed" arguments on the wire. math.Round,
// not truncation: truncating systematically biases toward zero.
type Fixed int32

// Float64 returns the value f approximates. The round trip through
// [FixedFromFloat64] is lossy: 24.8 resolves to 1/256.
func (f Fixed) Float64() float64 { return float64(f) / 256.0 }

// FixedFromFloat64 converts v to 24.8, rounding to the nearest 1/256 (see
// [Fixed] for why not truncating).
func FixedFromFloat64(v float64) Fixed { return Fixed(math.Round(v * 256.0)) }
