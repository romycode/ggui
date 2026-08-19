package wlcore

import "math"

// Fixed es un fixed-point 24.8 con signo, empaquetado en un int32 — el
// formato que usa Wayland para argumentos "fixed" en el wire. math.Round,
// no truncado: truncar sesga sistemáticamente hacia cero.
type Fixed int32

func (f Fixed) Float64() float64 { return float64(f) / 256.0 }

func FixedFromFloat64(v float64) Fixed { return Fixed(math.Round(v * 256.0)) }
