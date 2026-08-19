package wlcore

import (
	"encoding/binary"
	"errors"
	"fmt"

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

var (
	ErrShortMessage    = errors.New("wlcore: mensaje más corto que sus argumentos")
	ErrBadString       = errors.New("wlcore: string sin terminador nul")
	ErrNoFD            = errors.New("wlcore: se esperaba un fd y la cola está vacía")
	ErrMessageTooLarge = errors.New("wlcore: mensaje mayor que el máximo del wire format")
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

// maxMessageSize: el campo size del header ocupa 16 bits (size<<16|opcode
// en un uint32). Un mensaje que lo supere desborda esos bits en silencio y
// corrompe el opcode al otro lado; el guard vive aquí, no en Encoder.

// Send no lleva mutex: es parte de la misma API de un solo hilo que
// Register/SetListener/Dispatch (ver "Quién bombea" en wlcore.md).
func (c *Conn) Send(objectID uint32, opcode uint16, payload *Encoder, fds ...int) error {
	// payload nil es un request sin argumentos: el generador puede emitir
	// Send(id, op, nil) en vez de un NewEncoder() vacío.
	var body []byte
	if payload != nil {
		body = payload.Bytes()
	}
	total := 8 + len(body)
	if total > maxMessageSize {
		return fmt.Errorf("%w (%d bytes, máximo %d)", ErrMessageTooLarge, total, maxMessageSize)
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
	// dispatch() puede haber ido bien y aun así dejar la conexión muerta: un
	// listener al que llamó registró un error terminal por su cuenta
	// (wl_display.error es justo eso). Sin esta comprobación, Dispatch —y con
	// él Roundtrip— devolvería nil después de un error de protocolo.
	return c.err
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
