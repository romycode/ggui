package wlcore

import "testing"

func TestFixedFloat64RoundTrip(t *testing.T) {
	cases := []float64{0, 1, -1, 0.5, -0.5, 3.14, -3.14, 100.25}
	for _, v := range cases {
		f := FixedFromFloat64(v)
		got := f.Float64()
		if diff := got - v; diff > 1.0/256.0 || diff < -1.0/256.0 {
			t.Errorf("FixedFromFloat64(%v).Float64() = %v, want within 1/256 of %v", v, got, v)
		}
	}
}

func TestFixedFromFloat64Rounds(t *testing.T) {
	// 1.5 * (1/256) está justo a mitad de camino entre dos representables:
	// distingue redondeo de truncamiento hacia cero.
	f := FixedFromFloat64(1.0 / 256.0 * 1.5)
	if f != 2 {
		t.Errorf("FixedFromFloat64 truncó en vez de redondear: got %d, want 2", f)
	}
}

func TestFixedZeroValue(t *testing.T) {
	var f Fixed
	if f.Float64() != 0 {
		t.Errorf("zero value Float64() = %v, want 0", f.Float64())
	}
}
