package wlcore

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
)

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
