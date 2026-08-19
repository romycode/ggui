package wlcore

import (
	"encoding/binary"
	"os"
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

func TestReadBufFillAndDiscard(t *testing.T) {
	b := &readBuf{data: make([]byte, 16)}
	copy(b.free(), []byte("hello"))
	b.filled(5)
	if string(b.pending()) != "hello" {
		t.Fatalf("pending = %q, want %q", b.pending(), "hello")
	}
	b.discard(2)
	if string(b.pending()) != "llo" {
		t.Fatalf("pending = %q, want %q", b.pending(), "llo")
	}
}

func TestReadBufDiscardAllResetsToZero(t *testing.T) {
	b := &readBuf{data: make([]byte, 16)}
	copy(b.free(), []byte("hi"))
	b.filled(2)
	b.discard(2)
	if b.r != 0 || b.w != 0 {
		t.Fatalf("r=%d w=%d, want 0,0 tras vaciar", b.r, b.w)
	}
}

func TestReadBufFreeCompactsPending(t *testing.T) {
	b := &readBuf{data: make([]byte, 8)}
	copy(b.free(), []byte("abcd"))
	b.filled(4)
	b.discard(2) // pending = "cd", r=2 w=4

	free := b.free() // debe compactar: r=0, w=2
	if b.r != 0 || b.w != 2 {
		t.Fatalf("tras compactar r=%d w=%d, want 0,2", b.r, b.w)
	}
	if len(free) != 6 {
		t.Fatalf("free() len = %d, want 6 (8-2)", len(free))
	}
	if string(b.pending()) != "cd" {
		t.Fatalf("pending tras compactar = %q, want %q", b.pending(), "cd")
	}
}

func TestFdQueuePushPop(t *testing.T) {
	var q fdQueue
	q.push([]int{10, 11, 12})
	for _, want := range []int{10, 11, 12} {
		got, ok := q.pop()
		if !ok || got != want {
			t.Fatalf("pop() = %d, %v, want %d, true", got, ok, want)
		}
	}
	if _, ok := q.pop(); ok {
		t.Fatalf("pop() en cola vacía debería devolver ok=false")
	}
}

func TestFdQueueReusesArrayWhenEmptied(t *testing.T) {
	var q fdQueue
	q.push([]int{1, 2})
	q.pop()
	q.pop()
	if len(q.fds) != 0 || q.head != 0 {
		t.Fatalf("tras vaciar: fds=%v head=%d, want [] 0", q.fds, q.head)
	}
}

func TestFdQueueDrainClosesAll(t *testing.T) {
	r1, w1, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()
	defer w2.Close()

	var q fdQueue
	q.push([]int{int(r1.Fd()), int(r2.Fd())})
	q.drain()

	if err := r1.Close(); err == nil {
		t.Fatalf("r1 debería estar ya cerrado por drain()")
	}
	if err := r2.Close(); err == nil {
		t.Fatalf("r2 debería estar ya cerrado por drain()")
	}
}

func TestDropFDIgnoresNegative(t *testing.T) {
	DropFD(-1) // no debe hacer panic ni fallar
}

func TestAlign4(t *testing.T) {
	cases := map[int]int{0: 0, 1: 4, 2: 4, 3: 4, 4: 4, 5: 8}
	for in, want := range cases {
		if got := align4(in); got != want {
			t.Errorf("align4(%d) = %d, want %d", in, got, want)
		}
	}
}
