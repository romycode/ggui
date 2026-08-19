# wlcore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the hand-written `wlcore` Wayland client runtime (connection, wire format, event dispatch, object lifecycle) described in `docs/wlcore.md`, plus the minimal hand-written stand-ins for the three "bootstrap" protocol objects (`wl_display`, `wl_callback`, `wl_registry`) that `waygenerator` would otherwise generate — `waygenerator` itself does not exist yet.

**Architecture:** A single Go package `wlcore` at `wayland/wlcore/`. Pure hand-written infrastructure (`Encoder`/`Decoder`, connection, object registry, id lifecycle) plus three provisional protocol-object files that follow exactly the naming/shape conventions `waygenerator.md` defines, so they can be deleted and regenerated once the real generator exists without changing their public API. Built bottom-up: standalone pure types first (fully TDD), then the mutually-referential core (`Conn`/`Proxy`/`Decoder`, which are inherently circular per `wlcore.md`'s own "Bootstrap circular, asumido" note) as one scaffolding step, then behavior layered on top with tests.

**Tech Stack:** Go 1.26, stdlib only except `golang.org/x/sys/unix` (memfd/mmap not needed yet in this plan; `unix.Socketpair`/`unix.CmsgSpace`/`unix.ParseSocketControlMessage`/`unix.ParseUnixRights`/`unix.UnixRights`/`unix.Close` are). No test framework beyond stdlib `testing`.

**Spec:** `docs/wlcore.md` (runtime), `docs/waygenerator.md` (naming/contract conventions the bootstrap files must follow, and the file-layout section)

## Global Constraints

- Module: `github.com/romycode/ggui`, Go 1.26.6 (`go.mod`, unchanged).
- Package path: `wayland/wlcore` (import path `github.com/romycode/ggui/wayland/wlcore`).
- Byte order: `binary.NativeEndian`, never `binary.LittleEndian`.
- Max Wayland message size: `0xFFFF` (65535) bytes total including 8-byte header — enforced in `Conn.Send`.
- Read buffer size: `maxMessageSize + 1` = 64 KiB, fixed capacity, compacted not grown.
- Max fds per `recvmsg`: 28 (`maxFDsPerRead`, matches libwayland `MAX_FDS_OUT`).
- `displayID = 1`, `maxClientID = 0xFEFFFFFF`, `serverIDBase = 0xFF000000`.
- `Decoder` never panics on malformed input; sticky error, checked once via `Err()`.
- No background goroutines: `Dispatch`/`Run` are called by the application's own goroutine (single-threaded contract for `Conn`).
- Hand-written files the generator must never touch (per `waygenerator.md`): `conn.go`, `proxy.go`, `fixed.go`, `wire.go`, `registry.go`. This plan additionally introduces three **provisional** files (`display_bootstrap.go`, `callback_bootstrap.go`, `registry_bootstrap.go`) standing in for generator output until `waygenerator` exists — no `// Code generated ... DO NOT EDIT.` header on these (that would be a lie today), but a comment noting they're provisional.
- Within `wlcore`, core protocol types are **not** self-qualified (no `wlcore.` prefix in their own package).
- Dependency: `golang.org/x/sys/unix` must be added via `go get` (Task 4) — no other third-party dependency.

---

## Task 1: `Fixed` — 24.8 fixed-point type

**Files:**
- Create: `wayland/wlcore/fixed.go`
- Test: `wayland/wlcore/fixed_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Fixed int32`, `func (f Fixed) Float64() float64`, `func FixedFromFloat64(v float64) Fixed` — used by `Encoder.Fixed`/`Decoder.Fixed` in Task 2/5.

- [ ] **Step 1: Write the failing test**

Create `wayland/wlcore/fixed_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p wayland/wlcore && cd wayland/wlcore && go test ./... -run TestFixed -v`
Expected: FAIL (build error — `Fixed`/`FixedFromFloat64` undefined)

- [ ] **Step 3: Write minimal implementation**

Create `wayland/wlcore/fixed.go`:

```go
package wlcore

import "math"

// Fixed es un fixed-point 24.8 con signo, empaquetado en un int32 — el
// formato que usa Wayland para argumentos "fixed" en el wire. math.Round,
// no truncado: truncar sesga sistemáticamente hacia cero.
type Fixed int32

func (f Fixed) Float64() float64 { return float64(f) / 256.0 }

func FixedFromFloat64(v float64) Fixed { return Fixed(math.Round(v * 256.0)) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd wayland/wlcore && go test ./... -run TestFixed -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/fixed.go wayland/wlcore/fixed_test.go
git commit -m "wlcore: add Fixed 24.8 fixed-point type"
```

---

## Task 2: `Encoder`

**Files:**
- Create: `wayland/wlcore/wire.go`
- Create: `wayland/wlcore/wire_test.go`

**Interfaces:**
- Consumes: `Fixed` (Task 1).
- Produces: `NewEncoder() *Encoder`, `(*Encoder).Uint32/ID/Int32/Fixed/String/StringOpt/Array(...) *Encoder`, `(*Encoder).Bytes() []byte` — used by every request method and by `Conn.Send` (Task 6).

- [ ] **Step 1: Write the failing test**

Create `wayland/wlcore/wire_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd wayland/wlcore && go test ./... -run TestEncoder -v`
Expected: FAIL (build error — `Encoder` undefined)

- [ ] **Step 3: Write minimal implementation**

Create `wayland/wlcore/wire.go`:

```go
package wlcore

import "encoding/binary"

// Encoder serializa argumentos Wayland al wire format. No sabe nada de
// mensajes (objectID, opcode, header) — solo primitivas del wire. El
// ensamblado del header es responsabilidad de Conn.Send.
//
// Invariante que sostiene todo el padding: e.buf mide un múltiplo de 4 al
// entrar a cada método. Uint32/ID/Int32/Fixed escriben siempre 4 bytes
// exactos, así que la mantienen sola; String/Array/StringOpt la restauran
// ellos mismos rellenando hasta el siguiente múltiplo de 4.
type Encoder struct {
	buf []byte
}

func NewEncoder() *Encoder { return &Encoder{} }

func (e *Encoder) Uint32(v uint32) *Encoder {
	e.buf = binary.NativeEndian.AppendUint32(e.buf, v)
	return e
}

func (e *Encoder) ID(id uint32) *Encoder  { return e.Uint32(id) }
func (e *Encoder) Int32(v int32) *Encoder { return e.Uint32(uint32(v)) }
func (e *Encoder) Fixed(v Fixed) *Encoder { return e.Uint32(uint32(v)) }

func (e *Encoder) String(s string) *Encoder {
	e.Uint32(uint32(len(s) + 1)) // la longitud incluye el nul
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
	return e
}

// StringOpt es el string con allow-null="true": el wire format del string
// nulo es longitud 0 y cero bytes de datos, ni nul ni padding.
func (e *Encoder) StringOpt(s *string) *Encoder {
	if s == nil {
		return e.Uint32(0)
	}
	return e.String(*s)
}

func (e *Encoder) Array(data []byte) *Encoder {
	e.Uint32(uint32(len(data)))
	e.buf = append(e.buf, data...)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
	return e
}

func (e *Encoder) Bytes() []byte { return e.buf }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd wayland/wlcore && go test ./... -run TestEncoder -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/wire.go wayland/wlcore/wire_test.go
git commit -m "wlcore: add Encoder for wire-format serialization"
```

---

## Task 3: `readBuf` — compacting read-side reassembly buffer

**Files:**
- Modify: `wayland/wlcore/wire.go`
- Modify: `wayland/wlcore/wire_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type readBuf struct{...}`, `(*readBuf).pending/free/filled/discard`, `const maxMessageSize = 0xFFFF`, `const readBufSize` — used by `Conn` (Task 5) and the dispatch loop (Task 7).

- [ ] **Step 1: Write the failing test**

Append to `wayland/wlcore/wire_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd wayland/wlcore && go test ./... -run TestReadBuf -v`
Expected: FAIL (build error — `readBuf` undefined)

- [ ] **Step 3: Write minimal implementation**

Append to `wayland/wlcore/wire.go`:

```go
const maxMessageSize = 0xFFFF
const readBufSize = maxMessageSize + 1 // 64 KiB

// readBuf es un buffer de capacidad fija que se compacta, no un slice que
// crece: con reensamblado continuo, append+reslice acaba realocando y
// copiando constantemente. 64 KiB basta porque cualquier mensaje legal
// (tope maxMessageSize) entra entero tras compactar.
type readBuf struct {
	data []byte
	r, w int // los bytes pendientes son data[r:w]
}

func (b *readBuf) pending() []byte { return b.data[b.r:b.w] }

// free devuelve el hueco donde leer del socket, compactando antes.
func (b *readBuf) free() []byte {
	if b.r > 0 {
		n := copy(b.data, b.data[b.r:b.w])
		b.r, b.w = 0, n
	}
	return b.data[b.w:]
}

func (b *readBuf) filled(n int) { b.w += n }

func (b *readBuf) discard(n int) {
	b.r += n
	if b.r == b.w {
		b.r, b.w = 0, 0
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd wayland/wlcore && go test ./... -run TestReadBuf -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/wire.go wayland/wlcore/wire_test.go
git commit -m "wlcore: add readBuf compacting reassembly buffer"
```

---

## Task 4: `fdQueue`, `DropFD`, `align4`

**Files:**
- Modify: `wayland/wlcore/wire.go`
- Modify: `wayland/wlcore/wire_test.go`
- Modify: `go.mod` / `go.sum` (new dependency)

**Interfaces:**
- Consumes: nothing.
- Produces: `type fdQueue struct{...}`, `(*fdQueue).push/pop/drain`, `func DropFD(fd int)`, `func align4(n int) int`, `const maxFDsPerRead = 28` — used by `Conn` (Task 5), `Decoder.FD` (Task 5), the dispatch loop (Task 7), and generated `Dispatch` methods for fd-bearing events.

- [ ] **Step 1: Add the dependency**

Run: `go get golang.org/x/sys/unix`

- [ ] **Step 2: Write the failing test**

Append to `wayland/wlcore/wire_test.go` (add `"os"` to the import block):

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd wayland/wlcore && go test ./... -run 'TestFdQueue|TestDropFD|TestAlign4' -v`
Expected: FAIL (build error — `fdQueue`/`DropFD`/`align4` undefined)

- [ ] **Step 4: Write minimal implementation**

Append to `wayland/wlcore/wire.go` (add `"golang.org/x/sys/unix"` to the import block):

```go
// 28 fds por recvmsg, el mismo tope que usa libwayland (MAX_FDS_OUT).
const maxFDsPerRead = 28

type fdQueue struct {
	fds  []int
	head int
}

func (q *fdQueue) push(fds []int) { q.fds = append(q.fds, fds...) }

func (q *fdQueue) pop() (int, bool) {
	if q.head == len(q.fds) {
		return 0, false
	}
	fd := q.fds[q.head]
	q.head++
	if q.head == len(q.fds) { // vacía: reusa el array
		q.fds, q.head = q.fds[:0], 0
	}
	return fd, true
}

// drain cierra los fds que nadie llegó a consumir (mensaje a medias, error
// del bombeo). La llama quien bombea al salir.
func (q *fdQueue) drain() {
	for {
		fd, ok := q.pop()
		if !ok {
			return
		}
		DropFD(fd)
	}
}

// DropFD cierra un fd recibido que no se va a entregar a nadie.
func DropFD(fd int) {
	if fd >= 0 {
		unix.Close(fd)
	}
}

func align4(n int) int { return (n + 3) &^ 3 }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd wayland/wlcore && go test ./... -run 'TestFdQueue|TestDropFD|TestAlign4' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add wayland/wlcore/wire.go wayland/wlcore/wire_test.go go.mod go.sum
git commit -m "wlcore: add fdQueue, DropFD, align4; depend on golang.org/x/sys"
```

---

## Task 5: Core scaffolding — `Proxy`, `ProxyBase`, `Conn`, `Decoder`

This is the one genuinely non-linear step: `wlcore.md` documents this circularity explicitly ("Bootstrap circular, asumido" in `waygenerator.md`) — `Proxy.Dispatch` needs `*Decoder`, `Decoder.FD` needs `*Conn`, `Conn.objects` needs `Proxy`, and `Conn.display` needs a `*Display` type to exist. All of it must land together for the package to compile at all. `Display` gets only a placeholder here; Task 10 gives it real behavior.

**Files:**
- Create: `wayland/wlcore/proxy.go`
- Create: `wayland/wlcore/conn.go`
- Create: `wayland/wlcore/conn_test.go`
- Create: `wayland/wlcore/display_bootstrap.go`
- Modify: `wayland/wlcore/wire.go` (add `Decoder`)
- Modify: `wayland/wlcore/wire_test.go` (add `Decoder` tests)

**Interfaces:**
- Consumes: `Encoder` (Task 2), `readBuf`/`fdQueue`/`align4` (Tasks 3–4), `Fixed` (Task 1).
- Produces: `type Proxy interface{ID() uint32; Dispatch(uint16,*Decoder) error; clearListener()}`, `type ProxyBase struct{...}`, `func NewProxyBase(id, version uint32, c *Conn) ProxyBase`, `(*ProxyBase).ID/Version/Conn`, `type Conn struct{...}`, `func newConn(sock *net.UnixConn) *Conn`, `(*Conn).Register/Lookup/NewID/Display`, `type Decoder struct{...}`, `(*Conn).newDecoder(body []byte) *Decoder`, `(*Decoder).Uint32/ID/Int32/Fixed/String/StringOpt/Array/FD/Err`, `var ErrShortMessage/ErrBadString/ErrNoFD`. Also a shared test helper `type fakeProxy struct{ProxyBase; dispatched []uint16; listenerCleared bool}` reused by later tasks' tests.

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/conn_test.go`:

```go
package wlcore

import "testing"

// fakeProxy es el Proxy mínimo para tests: registra los opcodes que recibe
// y si clearListener() se llamó. Se reutiliza en tareas posteriores.
type fakeProxy struct {
	ProxyBase
	dispatched      []uint16
	listenerCleared bool
}

func (p *fakeProxy) Dispatch(opcode uint16, d *Decoder) error {
	p.dispatched = append(p.dispatched, opcode)
	return nil
}

func (p *fakeProxy) clearListener() { p.listenerCleared = true }

func TestConnNewIDMonotonic(t *testing.T) {
	c := newConn(nil)
	if got := c.NewID(); got != 2 {
		t.Fatalf("primer NewID() = %d, want 2 (1 es displayID)", got)
	}
	if got := c.NewID(); got != 3 {
		t.Fatalf("segundo NewID() = %d, want 3", got)
	}
}

func TestConnNewIDReusesFreedBeforeGrowing(t *testing.T) {
	c := newConn(nil)
	c.NewID() // 2
	c.NewID() // 3
	c.freeIDs = append(c.freeIDs, 2)
	if got := c.NewID(); got != 2 {
		t.Fatalf("NewID() con freeIDs = %d, want 2 (reciclado)", got)
	}
	if got := c.NewID(); got != 4 {
		t.Fatalf("NewID() tras agotar freeIDs = %d, want 4", got)
	}
}

func TestConnRegisterAndLookup(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	if got := c.Lookup(5); got != Proxy(p) {
		t.Fatalf("Lookup(5) = %v, want %v", got, p)
	}
	if c.Lookup(999) != nil {
		t.Fatalf("Lookup de id no registrado debería ser nil")
	}
}
```

Append to `wayland/wlcore/wire_test.go` (add `"bytes"` and `"errors"` to the import block):

```go
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
	d.String()
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: FAIL (build error — `Conn`, `Proxy`, `ProxyBase`, `NewProxyBase`, `newConn`, `Decoder`, `Display` all undefined)

- [ ] **Step 3: Write minimal implementation**

Create `wayland/wlcore/proxy.go`:

```go
package wlcore

// Proxy es lo que satisface cualquier objeto del protocolo, a mano o
// generado.
type Proxy interface {
	ID() uint32
	// Dispatch devuelve error si el mensaje viene malformado. No es
	// recuperable: el stream queda desalineado, así que el llamante
	// cierra la conexión.
	Dispatch(opcode uint16, d *Decoder) error
	// clearListener quita el listener puesto por SetListener; cada tipo
	// generado lo implementa poniendo su campo listener a su cero.
	clearListener()
}

type ProxyBase struct {
	id      uint32
	version uint32
	conn    *Conn
}

func NewProxyBase(id, version uint32, c *Conn) ProxyBase {
	return ProxyBase{id: id, version: version, conn: c}
}

func (p *ProxyBase) ID() uint32      { return p.id }
func (p *ProxyBase) Version() uint32 { return p.version }

// Conn() es exportado a propósito: los paquetes de extensión no pueden
// tocar el campo no exportado.
func (p *ProxyBase) Conn() *Conn { return p.conn }
```

Create `wayland/wlcore/conn.go`:

```go
package wlcore

import (
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	// El id 1 es wl_display: no lo asigna NewID, lo construye Connect().
	displayID = 1
	// Reparto del espacio de ids: abajo el cliente, arriba el servidor.
	maxClientID  = 0xFEFFFFFF
	serverIDBase = 0xFF000000
)

type Conn struct {
	sock *net.UnixConn

	// objects, nextID y freeIDs, igual que in/fds/oob: sin lock, porque
	// toda la API de Conn se usa desde una única goroutine (ver "Quién
	// bombea" en wlcore.md).
	objects map[uint32]Proxy
	nextID  uint32
	freeIDs []uint32 // ids devueltos por wl_display.delete_id

	display *Display // objeto 1, construido en Connect()
	onError func(objectID, code uint32, msg string)

	errOnce sync.Once
	done    chan struct{}
	err     error

	in  readBuf // bytes leídos, sin procesar
	fds fdQueue // fds recibidos, aún no consumidos por Proxy.Dispatch
	oob []byte  // buffer de ancillary data, reutilizado en cada recvmsg
}

func newConn(sock *net.UnixConn) *Conn {
	return &Conn{
		sock:    sock,
		objects: make(map[uint32]Proxy),
		nextID:  displayID, // el primer NewID() devuelve 2
		in:      readBuf{data: make([]byte, readBufSize)},
		oob:     make([]byte, unix.CmsgSpace(4*maxFDsPerRead)),
		done:    make(chan struct{}),
	}
}

func (c *Conn) Register(p Proxy) {
	c.objects[p.ID()] = p
}

func (c *Conn) Lookup(id uint32) Proxy {
	return c.objects[id]
}

// NewID recicla antes de crecer: sin esto, una sesión larga con frame
// callbacks agota el espacio de ids en unas horas.
func (c *Conn) NewID() uint32 {
	if n := len(c.freeIDs); n > 0 {
		id := c.freeIDs[n-1]
		c.freeIDs = c.freeIDs[:n-1]
		return id
	}
	c.nextID++
	return c.nextID
}

// Display devuelve el objeto 1, ya registrado por Connect().
func (c *Conn) Display() *Display { return c.display }
```

Create `wayland/wlcore/display_bootstrap.go` (placeholder — real behavior lands in Task 10):

```go
package wlcore

// Display es el objeto 1 (wl_display). Fichero PROVISIONAL: cuando exista
// waygenerator, se sustituye por su salida generada para wl_display (mismo
// nombre de tipo, mismo Dispatch). Placeholder por ahora — Task 10 de
// docs/superpowers/plans/2026-08-19-wlcore-implementation.md lo completa.
type Display struct {
	ProxyBase
}
```

Append to `wayland/wlcore/wire.go` (add `"errors"` to the import block):

```go
var (
	ErrShortMessage = errors.New("wlcore: mensaje más corto que sus argumentos")
	ErrBadString    = errors.New("wlcore: string sin terminador nul")
	ErrNoFD         = errors.New("wlcore: se esperaba un fd y la cola está vacía")
)

// Decoder deserializa argumentos Wayland del wire format. Dos reglas: nunca
// hace panic (el body viene del otro lado del socket, input no confiable),
// y el error es pegajoso — se comprueba una vez con Err() tras leer todos
// los argumentos.
type Decoder struct {
	buf  []byte
	off  int
	conn *Conn
	err  error
}

func (c *Conn) newDecoder(body []byte) *Decoder {
	return &Decoder{buf: body, conn: c}
}

func (d *Decoder) Err() error { return d.err }

func (d *Decoder) fail(err error) {
	if d.err == nil { // el primer error es el informativo
		d.err = err
	}
}

// take es el único sitio que indexa buf.
func (d *Decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.buf)-d.off {
		d.fail(ErrShortMessage)
		return nil
	}
	b := d.buf[d.off : d.off+n]
	d.off += n
	return b
}

func (d *Decoder) Uint32() uint32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return binary.NativeEndian.Uint32(b)
}

func (d *Decoder) ID() uint32   { return d.Uint32() }
func (d *Decoder) Int32() int32 { return int32(d.Uint32()) }
func (d *Decoder) Fixed() Fixed { return Fixed(d.Uint32()) }

// lenPrefixed es la lógica común a string y array: longitud + payload con
// padding. La longitud se valida contra lo que queda ANTES de alinear.
func (d *Decoder) lenPrefixed() ([]byte, int) {
	n := int(d.Uint32())
	if n < 0 || n > len(d.buf)-d.off {
		d.fail(ErrShortMessage)
		return nil, 0
	}
	return d.take(align4(n)), n
}

func (d *Decoder) String() string {
	b, n := d.lenPrefixed()
	if b == nil {
		return ""
	}
	if n == 0 || b[n-1] != 0 {
		d.fail(ErrBadString)
		return ""
	}
	return string(b[:n-1]) // el -1 se come el nul
}

// StringOpt distingue el string nulo (longitud 0, sin nul ni datos) del
// caso que String() rechazaría.
func (d *Decoder) StringOpt() *string {
	b, n := d.lenPrefixed()
	if d.err != nil {
		return nil
	}
	if n == 0 {
		return nil
	}
	if b[n-1] != 0 {
		d.fail(ErrBadString)
		return nil
	}
	s := string(b[:n-1])
	return &s
}

// Array copia: el body es una vista sobre el buffer de lectura, que se
// reutiliza en cuanto se vuelve a leer del socket.
func (d *Decoder) Array() []byte {
	b, n := d.lenPrefixed()
	if b == nil {
		return nil
	}
	return append([]byte(nil), b[:n]...)
}

func (d *Decoder) FD() int {
	if d.err != nil {
		return -1
	}
	fd, ok := d.conn.fds.pop()
	if !ok {
		d.fail(ErrNoFD)
		return -1
	}
	return fd
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS (all tests from Tasks 1–5)

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/proxy.go wayland/wlcore/conn.go wayland/wlcore/conn_test.go \
        wayland/wlcore/display_bootstrap.go wayland/wlcore/wire.go wayland/wlcore/wire_test.go
git commit -m "wlcore: add Proxy/ProxyBase/Conn scaffolding and Decoder"
```

---

## Task 6: `Conn.Send`

**Files:**
- Create: `wayland/wlcore/testhelpers_test.go`
- Modify: `wayland/wlcore/wire.go`
- Modify: `wayland/wlcore/wire_test.go`

**Interfaces:**
- Consumes: `Encoder` (Task 2), `Conn`/`newConn` (Task 5).
- Produces: `(*Conn).Send(objectID uint32, opcode uint16, payload *Encoder, fds ...int) error`, test helper `newSocketpairConns(t) (client, server *net.UnixConn)` reused by all remaining integration tests.

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/testhelpers_test.go`:

```go
package wlcore

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// newSocketpairConns crea un par de *net.UnixConn conectados con un
// socketpair SOCK_STREAM, para simular al compositor sin un socket
// Wayland real.
func newSocketpairConns(t *testing.T) (client, server *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	toConn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "socketpair")
		nc, err := net.FileConn(f)
		f.Close()
		if err != nil {
			t.Fatalf("FileConn: %v", err)
		}
		uc, ok := nc.(*net.UnixConn)
		if !ok {
			t.Fatalf("FileConn no devolvió un *net.UnixConn")
		}
		return uc
	}
	client = toConn(fds[0])
	server = toConn(fds[1])
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

// rawMessage construye un mensaje Wayland crudo: header (objectID,
// size<<16|opcode) + body. Lo usan los tests que simulan al compositor
// escribiendo bytes directamente.
func rawMessage(objectID uint32, opcode uint16, body []byte) []byte {
	total := 8 + len(body)
	buf := make([]byte, 8, total)
	binary.NativeEndian.PutUint32(buf[0:4], objectID)
	binary.NativeEndian.PutUint32(buf[4:8], uint32(total)<<16|uint32(opcode))
	return append(buf, body...)
}
```

Append to `wayland/wlcore/wire_test.go` (add `"os"` if not already present, and `"golang.org/x/sys/unix"` — needed here for the first time in this file):

```go
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
	if err := c.Send(1, 0, e); err == nil {
		t.Fatal("Send con payload gigante debería fallar")
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run TestSend -v`
Expected: FAIL (build error — `Conn.Send` undefined)

- [ ] **Step 3: Write minimal implementation**

Append to `wayland/wlcore/wire.go` (add `"fmt"` to the import block):

```go
// maxMessageSize: el campo size del header ocupa 16 bits (size<<16|opcode
// en un uint32). Un mensaje que lo supere desborda esos bits en silencio y
// corrompe el opcode al otro lado; el guard vive aquí, no en Encoder.

// Send no lleva mutex: es parte de la misma API de un solo hilo que
// Register/SetListener/Dispatch (ver "Quién bombea" en wlcore.md).
func (c *Conn) Send(objectID uint32, opcode uint16, payload *Encoder, fds ...int) error {
	body := payload.Bytes()
	total := 8 + len(body)
	if total > maxMessageSize {
		return fmt.Errorf("wlcore: message too large (%d bytes, max %d)", total, maxMessageSize)
	}

	buf := make([]byte, 8, total)
	binary.NativeEndian.PutUint32(buf[0:4], objectID)
	binary.NativeEndian.PutUint32(buf[4:8], uint32(total)<<16|uint32(opcode))
	buf = append(buf, body...)

	if len(fds) == 0 {
		_, err := c.sock.Write(buf)
		return err
	}
	oob := unix.UnixRights(fds...)
	_, _, err := c.sock.WriteMsgUnix(buf, oob, nil)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/wire.go wayland/wlcore/wire_test.go wayland/wlcore/testhelpers_test.go
git commit -m "wlcore: add Conn.Send with size guard and fd passing"
```

---

## Task 7: Dispatch loop (`dispatch`/`processMessages`/`Dispatch`/`Run`) + `fatal`/`Done`/`Err`

**Files:**
- Modify: `wayland/wlcore/wire.go`
- Modify: `wayland/wlcore/wire_test.go`
- Modify: `wayland/wlcore/conn.go`

**Interfaces:**
- Consumes: `readBuf`/`fdQueue` (Tasks 3–4), `Conn`/`Proxy`/`Decoder` (Task 5).
- Produces: `(*Conn).Dispatch() error`, `(*Conn).Run() error`, `(*Conn).DrainFDs()`, `(*Conn).fatal(err error)`, `(*Conn).Done() <-chan struct{}`, `(*Conn).Err() error` — `fatal`/`Done`/`Err` are used by `Connect` (Task 11), `Close` (Task 12), `Roundtrip` (Task 13).

- [ ] **Step 1: Write the failing tests**

Append to `wayland/wlcore/wire_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run 'TestDispatch|TestRun' -v`
Expected: FAIL (build error — `Conn.Dispatch`/`Run` undefined)

- [ ] **Step 3: Write minimal implementation**

Append to `wayland/wlcore/wire.go`:

```go
// Dispatch lee una vez del socket y despacha todos los mensajes completos
// que hayan entrado. Bloquea si no hay nada que leer. Cualquier error que
// devuelva es terminal y ya ha quedado registrado en la conexión.
//
// Contrato: una sola goroutine puede estar dentro a la vez.
func (c *Conn) Dispatch() error {
	if err := c.dispatch(); err != nil {
		c.fatal(err)
		// c.err, no err: si esto viene de un Close(), el error real es
		// ErrClosed y no el "use of closed network connection" que
		// devuelve el read al encontrarse el socket cerrado debajo.
		return c.err
	}
	return nil
}

func (c *Conn) dispatch() error {
	n, oobn, flags, _, err := c.sock.ReadMsgUnix(c.in.free(), c.oob)
	if err != nil {
		return err
	}
	// Sin esto, el kernel tira fds en silencio si no caben en oob.
	if flags&unix.MSG_CTRUNC != 0 {
		return errors.New("wlcore: ancillary data truncada, fds perdidos")
	}
	c.in.filled(n)

	if oobn > 0 {
		scms, err := unix.ParseSocketControlMessage(c.oob[:oobn])
		if err != nil {
			return err
		}
		for _, scm := range scms {
			fds, err := unix.ParseUnixRights(&scm)
			if err != nil {
				return err
			}
			c.fds.push(fds)
		}
	}
	return c.processMessages()
}

// Run bombea hasta que la conexión muere. Es lo último que hace main.
func (c *Conn) Run() error {
	defer c.fds.drain()
	for {
		if err := c.Dispatch(); err != nil {
			return err
		}
	}
}

// DrainFDs cierra los fds pendientes. Solo hace falta si se bombea a mano
// con Dispatch() en vez de con Run(); llamarla desde la misma goroutine
// que bombeaba, y solo después del último Dispatch().
func (c *Conn) DrainFDs() { c.fds.drain() }

func (c *Conn) processMessages() error {
	for {
		in := c.in.pending()
		if len(in) < 8 {
			return nil
		}
		objectID := binary.NativeEndian.Uint32(in[0:4])
		sizeOp := binary.NativeEndian.Uint32(in[4:8])
		size := int(sizeOp >> 16)
		opcode := uint16(sizeOp & 0xffff)

		// maxMessageSize, no readBufSize: un header que declare 65536 es
		// ilegal por wire format aunque quepa en el buffer.
		if size < 8 || size > maxMessageSize {
			return fmt.Errorf("wlcore: header corrupto (size=%d)", size)
		}
		if len(in) < size {
			return nil // mensaje incompleto, esperamos más bytes
		}

		if obj := c.Lookup(objectID); obj != nil {
			if err := obj.Dispatch(opcode, c.newDecoder(in[8:size])); err != nil {
				return fmt.Errorf("wlcore: objeto %d, opcode %d: %w", objectID, opcode, err)
			}
		}
		// si no está el objeto, se ignora (puede pasar legítimamente)
		c.in.discard(size)
	}
}
```

Append to `wayland/wlcore/conn.go` (add `"errors"` to the import block):

```go
// fatal fija el error terminal la primera vez, cierra done y el socket —
// eso desbloquea a quien esté parado en ReadMsgUnix.
func (c *Conn) fatal(err error) {
	if err == nil {
		return
	}
	c.errOnce.Do(func() {
		c.err = err
		close(c.done)
		c.sock.Close()
	})
}

func (c *Conn) Done() <-chan struct{} { return c.done }
func (c *Conn) Err() error            { return c.err } // válido tras Done()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/wire.go wayland/wlcore/wire_test.go wayland/wlcore/conn.go
git commit -m "wlcore: add Conn dispatch loop, Run, and terminal-error plumbing"
```

---

## Task 8: Bootstrap `wl_callback`

**Files:**
- Create: `wayland/wlcore/callback_bootstrap.go`
- Create: `wayland/wlcore/callback_bootstrap_test.go`

**Interfaces:**
- Consumes: `ProxyBase`/`Proxy` (Task 5), `Decoder`/`Encoder` (Tasks 2, 5).
- Produces: `type Callback struct{...}`, `type CallbackListener struct{Done func(uint32)}`, `(*Callback).SetListener`, `(*Callback).Dispatch` — used by `Display.Sync` (Task 10) and `Roundtrip` (Task 13).

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/callback_bootstrap_test.go`:

```go
package wlcore

import "testing"

func TestCallbackDispatchDone(t *testing.T) {
	c := newConn(nil)
	cb := &Callback{ProxyBase: NewProxyBase(2, 1, c)}
	var got uint32
	called := false
	cb.SetListener(CallbackListener{Done: func(data uint32) {
		called = true
		got = data
	}})

	body := NewEncoder().Uint32(1234).Bytes()
	if err := cb.Dispatch(opEvtCallbackDone, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Fatal("el listener Done no se llamó")
	}
	if got != 1234 {
		t.Fatalf("callback_data = %d, want 1234", got)
	}
}

func TestCallbackDispatchWithoutListenerDoesNotPanic(t *testing.T) {
	c := newConn(nil)
	cb := &Callback{ProxyBase: NewProxyBase(2, 1, c)}
	body := NewEncoder().Uint32(1).Bytes()
	if err := cb.Dispatch(opEvtCallbackDone, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
}

func TestCallbackDispatchUnknownOpcode(t *testing.T) {
	c := newConn(nil)
	cb := &Callback{ProxyBase: NewProxyBase(2, 1, c)}
	if err := cb.Dispatch(99, c.newDecoder(nil)); err == nil {
		t.Fatal("opcode desconocido debería devolver error")
	}
}

func TestCallbackClearListener(t *testing.T) {
	c := newConn(nil)
	cb := &Callback{ProxyBase: NewProxyBase(2, 1, c)}
	called := false
	cb.SetListener(CallbackListener{Done: func(uint32) { called = true }})
	cb.clearListener()

	body := NewEncoder().Uint32(1).Bytes()
	cb.Dispatch(opEvtCallbackDone, c.newDecoder(body))
	if called {
		t.Fatal("tras clearListener, Done no debería llamarse")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run TestCallback -v`
Expected: FAIL (build error — `Callback` undefined)

- [ ] **Step 3: Write minimal implementation**

Create `wayland/wlcore/callback_bootstrap.go`:

```go
package wlcore

import "fmt"

// Callback (wl_callback). Fichero PROVISIONAL: se sustituye por salida de
// waygenerator cuando exista. No lleva requests, solo el evento done, y se
// autodestruye tras done (el servidor manda su delete_id igualmente).
const opEvtCallbackDone = 0

type CallbackListener struct {
	Done func(callbackData uint32)
}

type Callback struct {
	ProxyBase
	listener CallbackListener
}

func (cb *Callback) SetListener(l CallbackListener) { cb.listener = l }

func (cb *Callback) clearListener() { cb.listener = CallbackListener{} }

func (cb *Callback) Dispatch(opcode uint16, d *Decoder) error {
	switch opcode {
	case opEvtCallbackDone:
		data := d.Uint32()
		if err := d.Err(); err != nil {
			return err
		}
		if cb.listener.Done != nil {
			cb.listener.Done(data)
		}
	default:
		return fmt.Errorf("wlcore: opcode %d desconocido en wl_callback", opcode)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -run TestCallback -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/callback_bootstrap.go wayland/wlcore/callback_bootstrap_test.go
git commit -m "wlcore: add provisional wl_callback bootstrap"
```

---

## Task 9: Bootstrap `wl_registry`

**Files:**
- Create: `wayland/wlcore/registry_bootstrap.go`
- Create: `wayland/wlcore/registry_bootstrap_test.go`

**Interfaces:**
- Consumes: `ProxyBase`/`Proxy` (Task 5), `Decoder`/`Encoder` (Tasks 2, 5), `Conn.Send` (Task 6), `newSocketpairConns` (Task 6).
- Produces: `type Registry struct{...}`, `type RegistryListener struct{Global, GlobalRemove func(...)}`, `(*Registry).SetListener`, `(*Registry).bindRaw(name uint32, iface string, version, newID uint32) error`, `(*Registry).Dispatch` — `bindRaw` is used by `Bind[T]` (Task 14); `Registry` type is used by `Display.GetRegistry` (Task 10).

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/registry_bootstrap_test.go`:

```go
package wlcore

import "testing"

func TestRegistryBindRawWireFormat(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	c.Register(reg)

	if err := reg.bindRaw(1, "wl_compositor", 4, 6); err != nil {
		t.Fatalf("bindRaw: %v", err)
	}

	buf := make([]byte, 128)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	d := &Decoder{buf: buf[8:n]}
	name := d.Uint32()
	iface := d.String()
	version := d.Uint32()
	id := d.ID()
	if err := d.Err(); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != 1 || iface != "wl_compositor" || version != 4 || id != 6 {
		t.Fatalf("got name=%d iface=%q version=%d id=%d, want 1 wl_compositor 4 6",
			name, iface, version, id)
	}
}

func TestRegistryDispatchGlobal(t *testing.T) {
	c := newConn(nil)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	var gotName, gotVersion uint32
	var gotIface string
	reg.SetListener(RegistryListener{
		Global: func(name uint32, iface string, version uint32) {
			gotName, gotIface, gotVersion = name, iface, version
		},
	})

	body := NewEncoder().Uint32(3).String("wl_shm").Uint32(1).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobal, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotName != 3 || gotIface != "wl_shm" || gotVersion != 1 {
		t.Fatalf("got %d %q %d, want 3 wl_shm 1", gotName, gotIface, gotVersion)
	}
}

func TestRegistryDispatchGlobalRemove(t *testing.T) {
	c := newConn(nil)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	var got uint32
	reg.SetListener(RegistryListener{GlobalRemove: func(name uint32) { got = name }})

	body := NewEncoder().Uint32(3).Bytes()
	if err := reg.Dispatch(opEvtRegistryGlobalRemove, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run TestRegistry -v`
Expected: FAIL (build error — `Registry` undefined)

- [ ] **Step 3: Write minimal implementation**

Create `wayland/wlcore/registry_bootstrap.go`:

```go
package wlcore

import "fmt"

// Registry (wl_registry). Fichero PROVISIONAL: se sustituye por salida de
// waygenerator cuando exista.
const opReqRegistryBind = 0

const (
	opEvtRegistryGlobal       = 0
	opEvtRegistryGlobalRemove = 1
)

type RegistryListener struct {
	Global       func(name uint32, iface string, version uint32)
	GlobalRemove func(name uint32)
}

type Registry struct {
	ProxyBase
	listener RegistryListener
}

func (r *Registry) SetListener(l RegistryListener) { r.listener = l }

func (r *Registry) clearListener() { r.listener = RegistryListener{} }

// bindRaw manda el request bind. new_id sin atributo interface se
// serializa como tres valores — nombre de interfaz, versión, id — no un
// u32 suelto.
func (r *Registry) bindRaw(name uint32, iface string, version, newID uint32) error {
	e := NewEncoder().Uint32(name).String(iface).Uint32(version).ID(newID)
	return r.Conn().Send(r.ID(), opReqRegistryBind, e)
}

func (r *Registry) Dispatch(opcode uint16, d *Decoder) error {
	switch opcode {
	case opEvtRegistryGlobal:
		name := d.Uint32()
		iface := d.String()
		version := d.Uint32()
		if err := d.Err(); err != nil {
			return err
		}
		if r.listener.Global != nil {
			r.listener.Global(name, iface, version)
		}
	case opEvtRegistryGlobalRemove:
		name := d.Uint32()
		if err := d.Err(); err != nil {
			return err
		}
		if r.listener.GlobalRemove != nil {
			r.listener.GlobalRemove(name)
		}
	default:
		return fmt.Errorf("wlcore: opcode %d desconocido en wl_registry", opcode)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -run TestRegistry -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/registry_bootstrap.go wayland/wlcore/registry_bootstrap_test.go
git commit -m "wlcore: add provisional wl_registry bootstrap"
```

---

## Task 10: Bootstrap `wl_display` (real behavior, replacing the Task 5 stub)

**Files:**
- Modify: `wayland/wlcore/display_bootstrap.go` (full rewrite)
- Create: `wayland/wlcore/display_bootstrap_test.go`

**Interfaces:**
- Consumes: `Callback` (Task 8), `Registry` (Task 9), `Conn.Send`/`NewID`/`Register` (Tasks 5–6).
- Produces: `type DisplayListener struct{Error, DeleteID func(...)}`, `(*Display).SetListener`, `(*Display).Sync() (*Callback, error)`, `(*Display).GetRegistry() (*Registry, error)`, `(*Display).Dispatch` — `Sync`/`GetRegistry`/`DisplayListener` used by `Connect` (Task 11) and `Roundtrip` (Task 13).

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/display_bootstrap_test.go`:

```go
package wlcore

import "testing"

func TestDisplaySyncRegistersAndSendsRequest(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.display = disp
	c.Register(disp)

	cb, err := disp.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if cb.ID() != 2 {
		t.Fatalf("cb.ID() = %d, want 2", cb.ID())
	}
	if c.Lookup(2) != Proxy(cb) {
		t.Fatal("Sync() no registró el callback")
	}

	buf := make([]byte, 32)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	d := &Decoder{buf: buf[8:n]}
	if got := d.ID(); got != 2 {
		t.Fatalf("id enviado = %d, want 2", got)
	}
}

func TestDisplayGetRegistryRegistersAndSendsRequest(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.display = disp
	c.Register(disp)

	reg, err := disp.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	if c.Lookup(reg.ID()) != Proxy(reg) {
		t.Fatal("GetRegistry() no registró el registry")
	}

	buf := make([]byte, 32)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayDispatchError(t *testing.T) {
	c := newConn(nil)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	var gotObj, gotCode uint32
	var gotMsg string
	disp.SetListener(DisplayListener{Error: func(objectID, code uint32, msg string) {
		gotObj, gotCode, gotMsg = objectID, code, msg
	}})

	body := NewEncoder().ID(5).Uint32(1).String("boom").Bytes()
	if err := disp.Dispatch(opEvtDisplayError, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotObj != 5 || gotCode != 1 || gotMsg != "boom" {
		t.Fatalf("got %d %d %q, want 5 1 boom", gotObj, gotCode, gotMsg)
	}
}

func TestDisplayDispatchDeleteID(t *testing.T) {
	c := newConn(nil)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	var got uint32
	disp.SetListener(DisplayListener{DeleteID: func(id uint32) { got = id }})

	body := NewEncoder().ID(42).Bytes()
	if err := disp.Dispatch(opEvtDisplayDeleteID, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run TestDisplay -v`
Expected: FAIL (build error — `DisplayListener`, `Sync`, `GetRegistry` undefined; `Display` only has the empty stub)

- [ ] **Step 3: Write minimal implementation**

Replace the full content of `wayland/wlcore/display_bootstrap.go`:

```go
package wlcore

import "fmt"

// Display (wl_display), el objeto 1. Fichero PROVISIONAL: se sustituye por
// salida de waygenerator cuando exista.
const (
	opReqDisplaySync        = 0
	opReqDisplayGetRegistry = 1

	opEvtDisplayError    = 0
	opEvtDisplayDeleteID = 1
)

// DisplayListener no lo debe usar el usuario directamente: Connect() lo
// engancha internamente para el reciclado de ids y la detección de errores
// de protocolo. La API pública es Conn.OnError (Task 12).
type DisplayListener struct {
	Error    func(objectID, code uint32, msg string)
	DeleteID func(id uint32)
}

type Display struct {
	ProxyBase
	listener DisplayListener
}

func (d *Display) SetListener(l DisplayListener) { d.listener = l }

func (d *Display) clearListener() { d.listener = DisplayListener{} }

func (d *Display) Sync() (*Callback, error) {
	id := d.Conn().NewID()
	cb := &Callback{ProxyBase: NewProxyBase(id, d.Version(), d.Conn())}
	d.Conn().Register(cb)

	e := NewEncoder().ID(id)
	if err := d.Conn().Send(d.ID(), opReqDisplaySync, e); err != nil {
		return nil, err
	}
	return cb, nil
}

func (d *Display) GetRegistry() (*Registry, error) {
	id := d.Conn().NewID()
	reg := &Registry{ProxyBase: NewProxyBase(id, d.Version(), d.Conn())}
	d.Conn().Register(reg)

	e := NewEncoder().ID(id)
	if err := d.Conn().Send(d.ID(), opReqDisplayGetRegistry, e); err != nil {
		return nil, err
	}
	return reg, nil
}

func (d *Display) Dispatch(opcode uint16, dec *Decoder) error {
	switch opcode {
	case opEvtDisplayError:
		objectID := dec.ID()
		code := dec.Uint32()
		msg := dec.String()
		if err := dec.Err(); err != nil {
			return err
		}
		if d.listener.Error != nil {
			d.listener.Error(objectID, code, msg)
		}
	case opEvtDisplayDeleteID:
		id := dec.ID()
		if err := dec.Err(); err != nil {
			return err
		}
		if d.listener.DeleteID != nil {
			d.listener.DeleteID(id)
		}
	default:
		return fmt.Errorf("wlcore: opcode %d desconocido en wl_display", opcode)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/display_bootstrap.go wayland/wlcore/display_bootstrap_test.go
git commit -m "wlcore: flesh out provisional wl_display (Sync, GetRegistry, error/delete_id)"
```

---

## Task 11: `Connect`, `dial`, `ProtocolError`, id lifecycle (`destroy`/`release`)

**Files:**
- Modify: `wayland/wlcore/conn.go`
- Create: `wayland/wlcore/conn_lifecycle_test.go`
- Create: `wayland/wlcore/connect_test.go`

**Interfaces:**
- Consumes: `Conn`/`Display`/`DisplayListener` (Tasks 5, 10), `fakeProxy` (Task 5).
- Produces: `func Connect() (*Conn, error)`, `func dial() (*net.UnixConn, error)`, `type ProtocolError struct{...}`, `(*Conn).destroy(p Proxy)`, `(*Conn).release(id uint32)` — `Connect` used by application code and by `Roundtrip` tests (Task 13); `destroy` is the runtime behind the generated `Destroy()` (out of scope for this plan, documented for future extension packages).

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/conn_lifecycle_test.go`:

```go
package wlcore

import "testing"

func TestDestroyClientIDStaysZombieUntilRelease(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)} // 5 < serverIDBase
	c.Register(p)

	c.destroy(p)
	if c.Lookup(5) == nil {
		t.Fatal("id de cliente destruido debería seguir en objects (zombie) hasta delete_id")
	}

	c.release(5)
	if c.Lookup(5) != nil {
		t.Fatal("tras release, el id debería haber desaparecido de objects")
	}
	if len(c.freeIDs) != 1 || c.freeIDs[0] != 5 {
		t.Fatalf("freeIDs = %v, want [5]", c.freeIDs)
	}
}

func TestDestroyServerIDRemovedImmediately(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(serverIDBase+1, 1, c)}
	c.Register(p)

	c.destroy(p)
	if c.Lookup(serverIDBase+1) != nil {
		t.Fatal("id de servidor debería desaparecer de objects en el acto")
	}
	if len(c.freeIDs) != 0 {
		t.Fatalf("freeIDs = %v, want vacío (los ids de servidor no se reciclan)", c.freeIDs)
	}
}

func TestDestroyClearsListener(t *testing.T) {
	c := newConn(nil)
	p := &fakeProxy{ProxyBase: NewProxyBase(5, 1, c)}
	c.Register(p)
	c.destroy(p)
	if !p.listenerCleared {
		t.Fatal("destroy() debería llamar a clearListener()")
	}
}
```

Create `wayland/wlcore/connect_test.go`:

```go
package wlcore

import (
	"errors"
	"net"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDialWaylandDisplayRelativeToXDGRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-test"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	os.Unsetenv("WAYLAND_SOCKET")

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()

	uc, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	uc.Close()
	<-accepted
}

func TestDialWaylandDisplayAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-abs"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("WAYLAND_DISPLAY", sockPath)
	os.Unsetenv("XDG_RUNTIME_DIR")
	os.Unsetenv("WAYLAND_SOCKET")

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()

	uc, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	uc.Close()
	<-accepted
}

func TestDialMissingXDGRuntimeDir(t *testing.T) {
	os.Unsetenv("WAYLAND_SOCKET")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	os.Unsetenv("XDG_RUNTIME_DIR")

	if _, err := dial(); err == nil {
		t.Fatal("dial() sin XDG_RUNTIME_DIR debería fallar")
	}
}

func TestDialWaylandSocket(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	other := os.NewFile(uintptr(fds[1]), "other")
	defer other.Close()

	t.Setenv("WAYLAND_SOCKET", strconv.Itoa(fds[0]))

	uc, err := dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer uc.Close()

	if _, ok := os.LookupEnv("WAYLAND_SOCKET"); ok {
		t.Fatal("dial() debería limpiar WAYLAND_SOCKET del entorno")
	}
}

func TestConnectWiresDeleteIDToRelease(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-connect"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-connect")
	os.Unsetenv("WAYLAND_SOCKET")

	serverDone := make(chan *net.UnixConn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serverDone <- conn.(*net.UnixConn)
	}()

	c, err := Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.sock.Close()

	server := <-serverDone
	defer server.Close()

	if c.Display().ID() != displayID {
		t.Fatalf("Display().ID() = %d, want %d", c.Display().ID(), displayID)
	}

	c.NewID() // consume el 2, para comprobar que delete_id lo libera

	body := NewEncoder().ID(2).Bytes()
	if _, err := server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body)); err != nil {
		t.Fatal(err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if got := c.NewID(); got != 2 {
		t.Fatalf("NewID() tras delete_id = %d, want 2 (reciclado)", got)
	}
}

func TestConnectWiresErrorToFatal(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/wayland-connect-err"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-connect-err")
	os.Unsetenv("WAYLAND_SOCKET")

	serverDone := make(chan *net.UnixConn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serverDone <- conn.(*net.UnixConn)
	}()

	c, err := Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.sock.Close()

	var gotObj uint32
	c.onError = func(objectID, code uint32, msg string) { gotObj = objectID }

	server := <-serverDone
	defer server.Close()

	body := NewEncoder().ID(1).Uint32(2).String("bad").Bytes()
	if _, err := server.Write(rawMessage(displayID, opEvtDisplayError, body)); err != nil {
		t.Fatal(err)
	}
	c.Dispatch()

	if gotObj != 1 {
		t.Fatalf("onError no se llamó con objectID=1, got %d", gotObj)
	}
	var protoErr *ProtocolError
	if !errors.As(c.Err(), &protoErr) {
		t.Fatalf("Err() = %v, want *ProtocolError", c.Err())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run 'TestDestroy|TestDial|TestConnect' -v`
Expected: FAIL (build error — `Connect`, `dial`, `ProtocolError`, `destroy`, `release` undefined)

- [ ] **Step 3: Write minimal implementation**

Append to `wayland/wlcore/conn.go` (add `"fmt"`, `"os"`, `"path/filepath"`, `"strconv"`, `"syscall"` to the import block):

```go
// destroy es el runtime que hay detrás del Destroy() generado: siempre
// limpia el listener, y libera el id ya mismo si era del servidor — el de
// cliente no se libera hasta que llegue delete_id.
func (c *Conn) destroy(p Proxy) {
	p.clearListener()
	if id := p.ID(); id >= serverIDBase {
		delete(c.objects, id)
	}
}

// release libera un id de cliente. Solo lo llama el handler interno de
// wl_display.delete_id.
func (c *Conn) release(id uint32) {
	delete(c.objects, id)
	c.freeIDs = append(c.freeIDs, id)
}

type ProtocolError struct {
	ObjectID uint32
	Code     uint32
	Message  string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wayland: objeto %d: %s (code %d)", e.ObjectID, e.Message, e.Code)
}

func dial() (*net.UnixConn, error) {
	if s, ok := os.LookupEnv("WAYLAND_SOCKET"); ok {
		fd, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("wlcore: WAYLAND_SOCKET inválido: %q", s)
		}
		// Quitarla del entorno SIEMPRE: si no, cualquier proceso hijo que
		// lancemos hereda la variable y comparte el stream con nosotros.
		os.Unsetenv("WAYLAND_SOCKET")
		syscall.CloseOnExec(fd)

		f := os.NewFile(uintptr(fd), "wayland")
		defer f.Close() // FileConn duplica el fd y pone el suyo en no-bloqueante
		nc, err := net.FileConn(f)
		if err != nil {
			return nil, err
		}
		uc, ok := nc.(*net.UnixConn)
		if !ok {
			nc.Close()
			return nil, fmt.Errorf("wlcore: WAYLAND_SOCKET no es un socket unix")
		}
		return uc, nil
	}

	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	path := name
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, errors.New("wlcore: ni WAYLAND_SOCKET ni XDG_RUNTIME_DIR")
		}
		path = filepath.Join(dir, name)
	}
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
}

// Connect monta el objeto 1 a mano — es el único que no pasa por NewID() —
// y engancha el listener interno antes de arrancar el loop, para no perder
// un error temprano.
func Connect() (*Conn, error) {
	sock, err := dial()
	if err != nil {
		return nil, err
	}
	c := newConn(sock)

	c.display = &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.Register(c.display)
	c.display.SetListener(DisplayListener{
		Error: func(objectID, code uint32, msg string) {
			if c.onError != nil {
				c.onError(objectID, code, msg)
			}
			c.fatal(&ProtocolError{ObjectID: objectID, Code: code, Message: msg})
		},
		DeleteID: c.release,
	})

	return c, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/conn.go wayland/wlcore/conn_lifecycle_test.go wayland/wlcore/connect_test.go
git commit -m "wlcore: add Connect, dial, ProtocolError, and object lifecycle"
```

---

## Task 12: `Close`, `ErrClosed`, `OnError`

**Files:**
- Modify: `wayland/wlcore/conn.go`
- Create: `wayland/wlcore/close_test.go`

**Interfaces:**
- Consumes: `fatal`/`Done`/`Err` (Task 7).
- Produces: `var ErrClosed error`, `(*Conn).Close() error`, `(*Conn).OnError(f func(objectID, code uint32, msg string))`.

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/close_test.go`:

```go
package wlcore

import (
	"errors"
	"testing"
)

func TestCloseIsIdempotentAndSetsErrClosed(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	c.Close()
	c.Close() // no debe hacer panic ni sobreescribir el error

	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err() = %v, want ErrClosed", c.Err())
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done() debería estar cerrado tras Close()")
	}
}

func TestCloseDoesNotMaskEarlierFatalError(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	sentinel := errors.New("boom")
	c.fatal(sentinel)
	c.Close()

	if !errors.Is(c.Err(), sentinel) {
		t.Fatalf("Err() = %v, want el primer error (%v), no ErrClosed", c.Err(), sentinel)
	}
}

func TestOnErrorSetsCallback(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	called := false
	c.OnError(func(objectID, code uint32, msg string) { called = true })
	if c.onError == nil {
		t.Fatal("OnError() no fijó c.onError")
	}
	c.onError(1, 2, "x")
	if !called {
		t.Fatal("el callback fijado por OnError no se invocó")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run 'TestClose|TestOnError' -v`
Expected: FAIL (build error — `Conn.Close`/`OnError`/`ErrClosed` undefined)

- [ ] **Step 3: Write minimal implementation**

Append to `wayland/wlcore/conn.go`:

```go
var ErrClosed = errors.New("wlcore: conexión cerrada por el cliente")

// Close cierra la conexión. Idempotente, y seguro llamarlo con la conexión
// ya caída: el errOnce se queda con el primer error, así que un Close() de
// defer no enmascara el fallo real.
func (c *Conn) Close() error {
	c.fatal(ErrClosed)
	return nil
}

// OnError registra el callback que se invoca cuando el compositor manda
// wl_display.error. Sustituye por completo al listener de Display, que el
// usuario no debe tocar. Como cualquier SetListener, hay que llamarlo
// antes del primer Dispatch()/Run() para no perderse un error temprano.
func (c *Conn) OnError(f func(objectID, code uint32, msg string)) { c.onError = f }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/conn.go wayland/wlcore/close_test.go
git commit -m "wlcore: add Conn.Close, ErrClosed, and public OnError"
```

---

## Task 13: `Roundtrip`

**Files:**
- Modify: `wayland/wlcore/conn.go`
- Create: `wayland/wlcore/roundtrip_test.go`

**Interfaces:**
- Consumes: `Display.Sync`/`Callback` (Tasks 8, 10), `Conn.Dispatch` (Task 7).
- Produces: `(*Conn).Roundtrip() error`.

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/roundtrip_test.go`:

```go
package wlcore

import "testing"

func TestRoundtripBlocksUntilCallbackDone(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.display = disp
	c.Register(disp)

	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		n, err := server.Read(buf) // consume el wl_display.sync
		if err != nil {
			serverErr <- err
			return
		}
		d := &Decoder{buf: buf[8:n]}
		cbID := d.ID()
		body := NewEncoder().Uint32(0).Bytes()
		_, err = server.Write(rawMessage(cbID, opEvtCallbackDone, body))
		serverErr <- err
	}()

	if err := c.Roundtrip(); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("simulación de servidor: %v", err)
	}
}

func TestRoundtripPropagatesDispatchError(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	disp := &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.display = disp
	c.Register(disp)

	go func() {
		buf := make([]byte, 32)
		server.Read(buf) // consume el sync y no responde
		server.Close()   // fuerza el error de lectura en el siguiente Dispatch
	}()

	if err := c.Roundtrip(); err == nil {
		t.Fatal("Roundtrip debería propagar el error si la conexión se cae antes del done")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run TestRoundtrip -v`
Expected: FAIL (build error — `Conn.Roundtrip` undefined)

- [ ] **Step 3: Write minimal implementation**

Append to `wayland/wlcore/conn.go`:

```go
// Roundtrip crea el sync y bombea hasta que llegue su done. No se puede
// llamar desde dentro de un listener (dispatch reentrante) ni desde otra
// goroutine que la que bombea.
func (c *Conn) Roundtrip() error {
	cb, err := c.display.Sync()
	if err != nil {
		return err
	}
	done := false
	cb.SetListener(CallbackListener{Done: func(uint32) { done = true }})
	for !done {
		if err := c.Dispatch(); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/conn.go wayland/wlcore/roundtrip_test.go
git commit -m "wlcore: add Conn.Roundtrip"
```

---

## Task 14: `registry.go` — `Interface[T]` and `Bind[T]`

**Files:**
- Create: `wayland/wlcore/registry.go`
- Create: `wayland/wlcore/registry_test.go`

**Interfaces:**
- Consumes: `Registry.bindRaw` (Task 9), `Proxy`/`ProxyBase`/`NewProxyBase` (Task 5), `Conn.NewID`/`Register` (Task 5).
- Produces: `type Interface[T Proxy] struct{Name string; MaxVersion uint32; New func(ProxyBase) T}`, `func Bind[T Proxy](r *Registry, name, version uint32, iface Interface[T]) (T, error)` — the public entry point extension packages (`xdgshell`, `wlrlayershell`) and future generated code use to bind globals.

- [ ] **Step 1: Write the failing tests**

Create `wayland/wlcore/registry_test.go`:

```go
package wlcore

import "testing"

type fakeBoundProxy struct {
	ProxyBase
}

func (p *fakeBoundProxy) Dispatch(uint16, *Decoder) error { return nil }
func (p *fakeBoundProxy) clearListener()                  {}

var fakeInterface = Interface[*fakeBoundProxy]{
	Name:       "wl_fake",
	MaxVersion: 3,
	New:        func(b ProxyBase) *fakeBoundProxy { return &fakeBoundProxy{ProxyBase: b} },
}

func TestBindNegotiatesMinVersion(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	c.Register(reg)

	// el global anuncia v10, el binding solo soporta hasta v3
	obj, err := Bind(reg, 7, 10, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if obj.Version() != 3 {
		t.Fatalf("Version() = %d, want 3 (min(10,3))", obj.Version())
	}

	buf := make([]byte, 64)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	d := &Decoder{buf: buf[8:n]}
	name := d.Uint32()
	iface := d.String()
	version := d.Uint32()
	id := d.ID()
	if err := d.Err(); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != 7 || iface != "wl_fake" || version != 3 {
		t.Fatalf("got name=%d iface=%q version=%d, want 7 wl_fake 3", name, iface, version)
	}
	if id != obj.ID() {
		t.Fatalf("id enviado = %d, want %d (obj.ID())", id, obj.ID())
	}
}

func TestBindRegistersObjectBeforeSending(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	reg := &Registry{ProxyBase: NewProxyBase(2, 1, c)}
	c.Register(reg)

	obj, err := Bind(reg, 1, 1, fakeInterface)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Lookup(obj.ID()) != Proxy(obj) {
		t.Fatal("Bind() debería registrar el objeto")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd wayland/wlcore && go test ./... -run TestBind -v`
Expected: FAIL (build error — `Interface`, `Bind` undefined)

- [ ] **Step 3: Write minimal implementation**

Create `wayland/wlcore/registry.go`:

```go
package wlcore

// Interface describe una interfaz del protocolo para efectos de Bind: su
// nombre en el wire, la versión máxima que soporta este binding, y la
// factory que construye el tipo concreto a partir de un ProxyBase.
type Interface[T Proxy] struct {
	Name       string
	MaxVersion uint32
	New        func(ProxyBase) T
}

// Bind negocia min(version, iface.MaxVersion), registra el objeto ANTES de
// mandar el request (el servidor puede empezar a mandarle eventos en
// cuanto lo procese), y manda el bind crudo por Registry.bindRaw.
//
// Función libre, no método: Go no admite métodos genéricos, y Registry no
// puede llevar el parámetro de tipo porque es un objeto del protocolo, no
// un contenedor tipado.
func Bind[T Proxy](r *Registry, name, version uint32, iface Interface[T]) (T, error) {
	v := version
	if v > iface.MaxVersion {
		v = iface.MaxVersion
	}
	id := r.Conn().NewID()
	obj := iface.New(NewProxyBase(id, v, r.Conn()))
	r.Conn().Register(obj)

	if err := r.bindRaw(name, iface.Name, v, id); err != nil {
		var zero T
		return zero, err
	}
	return obj, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd wayland/wlcore && go test ./... -v`
Expected: PASS (full package, all tasks)

- [ ] **Step 5: Commit**

```bash
git add wayland/wlcore/registry.go wayland/wlcore/registry_test.go
git commit -m "wlcore: add generic Interface[T]/Bind[T]"
```

---

## Final Verification

- [ ] Run the full suite once more from the module root: `go build ./... && go vet ./... && go test ./... -v`
- [ ] Confirm every file listed in the Global Constraints' hand-written list exists and matches `docs/wlcore.md`'s code exactly where the doc gives literal code.
- [ ] Confirm the three bootstrap files carry the "PROVISIONAL — replace when waygenerator exists" comment and no `DO NOT EDIT` header.
