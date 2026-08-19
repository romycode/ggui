package wlcore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
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

func TestDecoderUint32AndID(t *testing.T) {
	payload := NewEncoder().Uint32(42).ID(7).Bytes()
	d := &Decoder{buf: payload}
	if got := d.Uint32(); got != 42 {
		t.Fatalf("Uint32() = %d, want 42", got)
	}
	if got := d.ID(); got != 7 {
		t.Fatalf("ID() = %d, want 7", got)
	}
	if d.Err() != nil {
		t.Fatalf("Err() = %v, want nil", d.Err())
	}
}

func TestDecoderInt32Negative(t *testing.T) {
	payload := NewEncoder().Int32(-5).Bytes()
	d := &Decoder{buf: payload}
	if got := d.Int32(); got != -5 {
		t.Fatalf("Int32() = %d, want -5", got)
	}
}

func TestDecoderFixed(t *testing.T) {
	payload := NewEncoder().Fixed(FixedFromFloat64(1.5)).Bytes()
	d := &Decoder{buf: payload}
	if got := d.Fixed().Float64(); got != 1.5 {
		t.Fatalf("Fixed().Float64() = %v, want 1.5", got)
	}
}

func TestDecoderString(t *testing.T) {
	payload := NewEncoder().String("hola").Bytes()
	d := &Decoder{buf: payload}
	if got := d.String(); got != "hola" {
		t.Fatalf("String() = %q, want %q", got, "hola")
	}
	if d.Err() != nil {
		t.Fatalf("Err() = %v, want nil", d.Err())
	}
}

func TestDecoderStringOptNilAndSome(t *testing.T) {
	payload := NewEncoder().StringOpt(nil).Bytes()
	d := &Decoder{buf: payload}
	if got := d.StringOpt(); got != nil {
		t.Fatalf("StringOpt() = %v, want nil", got)
	}

	s := "hi"
	payload2 := NewEncoder().StringOpt(&s).Bytes()
	d2 := &Decoder{buf: payload2}
	got := d2.StringOpt()
	if got == nil || *got != "hi" {
		t.Fatalf("StringOpt() = %v, want *\"hi\"", got)
	}
}

func TestDecoderArray(t *testing.T) {
	payload := NewEncoder().Array([]byte{1, 2, 3}).Bytes()
	d := &Decoder{buf: payload}
	got := d.Array()
	want := []byte{1, 2, 3}
	if !bytes.Equal(got, want) {
		t.Fatalf("Array() = %v, want %v", got, want)
	}
}

func TestDecoderShortMessageIsSticky(t *testing.T) {
	d := &Decoder{buf: []byte{1, 2}} // menos de 4 bytes
	d.Uint32()
	if !errors.Is(d.Err(), ErrShortMessage) {
		t.Fatalf("Err() = %v, want ErrShortMessage", d.Err())
	}
	if got := d.Uint32(); got != 0 {
		t.Fatalf("lectura tras error = %d, want 0", got)
	}
}

func TestDecoderBadStringNoNul(t *testing.T) {
	e := NewEncoder().Uint32(3)
	e.buf = append(e.buf, 'a', 'b', 'c', 0) // "abc" sin nul final + padding manual
	d := &Decoder{buf: e.Bytes()}
	_ = d.String()
	if !errors.Is(d.Err(), ErrBadString) {
		t.Fatalf("Err() = %v, want ErrBadString", d.Err())
	}
}

func TestDecoderFDPopsFromConnQueue(t *testing.T) {
	c := &Conn{}
	c.fds.push([]int{99})
	d := &Decoder{buf: []byte{}, conn: c}
	if got := d.FD(); got != 99 {
		t.Fatalf("FD() = %d, want 99", got)
	}
}

func TestDecoderFDNoFDAvailable(t *testing.T) {
	c := &Conn{}
	d := &Decoder{buf: []byte{}, conn: c}
	if got := d.FD(); got != -1 {
		t.Fatalf("FD() = %d, want -1", got)
	}
	if !errors.Is(d.Err(), ErrNoFD) {
		t.Fatalf("Err() = %v, want ErrNoFD", d.Err())
	}
}

func TestSendWritesHeaderAndBody(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	e := NewEncoder().ID(1).Uint32(42)
	if err := c.Send(3, 7, e); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 64)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 16 { // 8 header + 4 ID + 4 Uint32
		t.Fatalf("n = %d, want 16", n)
	}
	objectID := binary.NativeEndian.Uint32(buf[0:4])
	sizeOp := binary.NativeEndian.Uint32(buf[4:8])
	size := sizeOp >> 16
	opcode := uint16(sizeOp & 0xffff)
	if objectID != 3 {
		t.Errorf("objectID = %d, want 3", objectID)
	}
	if size != 16 {
		t.Errorf("size = %d, want 16", size)
	}
	if opcode != 7 {
		t.Errorf("opcode = %d, want 7", opcode)
	}
}

func TestSendRejectsOversizedMessage(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	big := make([]byte, maxMessageSize)
	e := NewEncoder().Array(big)
	err := c.Send(1, 0, e)
	if err == nil {
		t.Fatal("Send con payload gigante debería fallar")
	}
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("Send() = %v, want ErrMessageTooLarge", err)
	}
}

func TestSendPassesFDsViaSCMRights(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	e := NewEncoder().Int32(4)
	if err := c.Send(1, 0, e, int(r.Fd())); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 32)
	oob := make([]byte, 32)
	n, oobn, _, _, err := server.ReadMsgUnix(buf, oob)
	if err != nil {
		t.Fatalf("ReadMsgUnix: %v", err)
	}
	if n != 12 { // 8 header + 4 Int32
		t.Fatalf("n = %d, want 12", n)
	}
	scms, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		t.Fatalf("ParseSocketControlMessage: %v", err)
	}
	if len(scms) != 1 {
		t.Fatalf("scms = %d, want 1", len(scms))
	}
	gotFds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		t.Fatalf("ParseUnixRights: %v", err)
	}
	if len(gotFds) != 1 {
		t.Fatalf("gotFds = %d, want 1", len(gotFds))
	}
	unix.Close(gotFds[0])
}

func TestDispatchDeliversToRegisteredProxy(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)

	body := NewEncoder().Uint32(99).Bytes()
	if _, err := server.Write(rawMessage(5, 3, body)); err != nil {
		t.Fatal(err)
	}

	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(p.dispatched) != 1 || p.dispatched[0] != 3 {
		t.Fatalf("dispatched = %v, want [3]", p.dispatched)
	}
}

func TestDispatchIgnoresUnknownObjectID(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	if _, err := server.Write(rawMessage(999, 0, nil)); err != nil {
		t.Fatal(err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch no debería fallar por un id desconocido: %v", err)
	}
}

func TestDispatchHandlesTwoMessagesInOneRead(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)

	buf := append(rawMessage(5, 1, nil), rawMessage(5, 2, nil)...)
	if _, err := server.Write(buf); err != nil {
		t.Fatal(err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(p.dispatched) != 2 || p.dispatched[0] != 1 || p.dispatched[1] != 2 {
		t.Fatalf("dispatched = %v, want [1 2]", p.dispatched)
	}
}

// El compositor puede partir un mensaje entre dos writes: el primer Dispatch
// ve solo el header, no entrega nada y no falla; el segundo completa el
// mensaje y lo entrega.
func TestDispatchReassemblesMessageSplitAcrossReads(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)

	msg := rawMessage(5, 4, NewEncoder().Uint32(7).Bytes())
	if _, err := server.Write(msg[:8]); err != nil { // solo el header
		t.Fatal(err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch con el mensaje a medias: %v", err)
	}
	if len(p.dispatched) != 0 {
		t.Fatalf("dispatched = %v, want [] (mensaje incompleto)", p.dispatched)
	}

	if _, err := server.Write(msg[8:]); err != nil { // el body
		t.Fatal(err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch tras completar el mensaje: %v", err)
	}
	if len(p.dispatched) != 1 || p.dispatched[0] != 4 {
		t.Fatalf("dispatched = %v, want [4]", p.dispatched)
	}
}

func TestSendNilPayloadIsEmptyBody(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	if err := c.Send(3, 1, nil); err != nil {
		t.Fatalf("Send con payload nil: %v", err)
	}
	buf := make([]byte, 32)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Fatalf("bytes enviados = %d, want 8 (solo el header)", n)
	}
	sizeOp := binary.NativeEndian.Uint32(buf[4:8])
	if size := int(sizeOp >> 16); size != 8 {
		t.Fatalf("size del header = %d, want 8", size)
	}
}

func TestDispatchRejectsCorruptHeaderSize(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)

	buf := make([]byte, 8)
	binary.NativeEndian.PutUint32(buf[0:4], 1)
	binary.NativeEndian.PutUint32(buf[4:8], 0) // size=0, ilegal (<8)
	if _, err := server.Write(buf); err != nil {
		t.Fatal(err)
	}
	if err := c.Dispatch(); err == nil {
		t.Fatal("Dispatch con header corrupto debería fallar")
	}
	if c.Err() == nil {
		t.Fatal("un fallo de Dispatch debe marcar la conexión como terminal")
	}
}

func TestRunExitsOnConnectionClose(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	server.Close()

	if err := c.Run(); err == nil {
		t.Fatal("Run() debería devolver error cuando el otro lado cierra")
	}
}
