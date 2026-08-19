package wlcore

import (
	"encoding/binary"
	"testing"
)

func TestEncoderUint32(t *testing.T) {
	got := NewEncoder().Uint32(0x01020304).Bytes()
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
}

func TestEncoderChaining(t *testing.T) {
	got := NewEncoder().ID(1).Uint32(2).Bytes()
	if len(got) != 8 {
		t.Fatalf("len = %d, want 8", len(got))
	}
}

func TestEncoderString(t *testing.T) {
	got := NewEncoder().String("super").Bytes()
	if len(got) != 12 { // len(4) + "super\0"(6) + padding(2) = 12
		t.Fatalf("len = %d, want 12", len(got))
	}
	n := binary.NativeEndian.Uint32(got[0:4])
	if n != 6 {
		t.Fatalf("longitud codificada = %d, want 6 (incluye nul)", n)
	}
	if string(got[4:9]) != "super" {
		t.Fatalf("payload = %q, want %q", got[4:9], "super")
	}
	if got[9] != 0 {
		t.Fatalf("falta el nul terminador")
	}
}

func TestEncoderStringOptNil(t *testing.T) {
	got := NewEncoder().StringOpt(nil).Bytes()
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 (solo la longitud, sin datos)", len(got))
	}
	if binary.NativeEndian.Uint32(got) != 0 {
		t.Fatalf("longitud codificada = %d, want 0", binary.NativeEndian.Uint32(got))
	}
}

func TestEncoderStringOptSome(t *testing.T) {
	s := "hi"
	got := NewEncoder().StringOpt(&s).Bytes()
	want := NewEncoder().String("hi").Bytes()
	if len(got) != len(want) {
		t.Fatalf("StringOpt(&s) len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("StringOpt(&s) = %v, want %v", got, want)
		}
	}
}

func TestEncoderArrayPadding(t *testing.T) {
	got := NewEncoder().Array([]byte{1, 2, 3}).Bytes()
	if len(got) != 8 { // longitud(4) + 3 bytes + 1 padding = 8
		t.Fatalf("len = %d, want 8", len(got))
	}
	if got[len(got)-1] != 0 {
		t.Fatalf("falta padding a múltiplo de 4")
	}
}

func TestEncoderBufAlwaysMultipleOf4(t *testing.T) {
	e := NewEncoder().String("x").Array([]byte{1}).String("abc")
	if len(e.Bytes())%4 != 0 {
		t.Fatalf("buf no es múltiplo de 4: %d bytes", len(e.Bytes()))
	}
}

func TestEncoderFixed(t *testing.T) {
	got := NewEncoder().Fixed(FixedFromFloat64(1.5)).Bytes()
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if Fixed(binary.NativeEndian.Uint32(got)).Float64() != 1.5 {
		t.Fatalf("decodificado = %v, want 1.5", Fixed(binary.NativeEndian.Uint32(got)).Float64())
	}
}
