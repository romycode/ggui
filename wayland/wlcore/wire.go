package wlcore

import (
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// Encoder serializes Wayland arguments to the wire format. It knows nothing
// about messages (objectID, opcode, header) — only wire primitives.
// Assembling the header is Conn.Send's responsibility.
//
// The invariant that holds up all the padding: e.buf's length is a
// multiple of 4 on entry to every method. Uint32/ID/Int32/Fixed always
// write exactly 4 bytes, so they maintain it on their own;
// String/Array/StringOpt restore it themselves by padding to the next
// multiple of 4.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder. Generated requests chain onto it in
// XML argument order and hand the result to [Conn.Send], which prepends the
// header.
func NewEncoder() *Encoder { return &Encoder{} }

// Uint32 appends a 32-bit argument in native byte order -- the Wayland wire
// format is defined in the host's endianness, since both ends of the socket
// are on the same machine.
func (e *Encoder) Uint32(v uint32) *Encoder {
	e.buf = binary.NativeEndian.AppendUint32(e.buf, v)
	return e
}

// ID appends an object id. Same 32 bits as [Encoder.Uint32]; the separate
// method is what makes the generated call sites say which argument is an
// object reference.
func (e *Encoder) ID(id uint32) *Encoder { return e.Uint32(id) }

// Int32 appends a signed 32-bit argument.
func (e *Encoder) Int32(v int32) *Encoder { return e.Uint32(uint32(v)) }

// Fixed appends a 24.8 fixed-point argument, already packed into its int32
// by [Fixed].
func (e *Encoder) Fixed(v Fixed) *Encoder { return e.Uint32(uint32(v)) }

// String appends a length-prefixed string: the length counts the nul
// terminator, and the whole thing is padded to a multiple of 4. For an
// argument declared allow-null use [Encoder.StringOpt] -- a null string is
// not the same as an empty one on the wire.
func (e *Encoder) String(s string) *Encoder {
	e.Uint32(uint32(len(s) + 1)) // the length includes the nul
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
	return e
}

// StringOpt is the string with allow-null="true": the wire format of the
// null string is length 0 and zero data bytes, no nul and no padding.
func (e *Encoder) StringOpt(s *string) *Encoder {
	if s == nil {
		return e.Uint32(0)
	}
	return e.String(*s)
}

// Array appends a length-prefixed byte array padded to a multiple of 4.
// Unlike [Encoder.String] the length is the payload exactly, with no
// terminator.
func (e *Encoder) Array(data []byte) *Encoder {
	e.Uint32(uint32(len(data)))
	e.buf = append(e.buf, data...)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
	return e
}

// Bytes returns the encoded arguments, without a header. The slice is the
// Encoder's own buffer, not a copy: [Conn.Send] writes it out immediately
// and nobody keeps it.
func (e *Encoder) Bytes() []byte { return e.buf }

const maxMessageSize = 0xFFFF
const readBufSize = maxMessageSize + 1 // 64 KiB

// readBuf is a fixed-capacity buffer that compacts, not a slice that
// grows: with continuous reassembly, append+reslice ends up constantly
// reallocating and copying. 64 KiB is enough because any legal message
// (capped at maxMessageSize) fits whole after compacting.
type readBuf struct {
	data []byte
	r, w int // pending bytes are data[r:w]
}

func (b *readBuf) pending() []byte { return b.data[b.r:b.w] }

// free returns the room to read from the socket into, compacting first.
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

// 28 fds per recvmsg, the same cap libwayland uses (MAX_FDS_OUT).
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
	if q.head == len(q.fds) { // empty: reuse the array
		q.fds, q.head = q.fds[:0], 0
	}
	return fd, true
}

// drain closes the fds nobody got around to consuming (a half-read
// message, a pump error). Called by whoever is pumping, on the way out.
func (q *fdQueue) drain() {
	for {
		fd, ok := q.pop()
		if !ok {
			return
		}
		DropFD(fd)
	}
}

// DropFD closes a received fd that isn't going to be handed to anyone.
func DropFD(fd int) {
	if fd >= 0 {
		unix.Close(fd)
	}
}

func align4(n int) int { return (n + 3) &^ 3 }

// The four ways a message can be malformed. All are fatal rather than
// recoverable: they mean the reader and the compositor disagree about where
// the message ends, so the stream is misaligned from that point on and the
// only correct move is to close the connection.
var (
	// ErrShortMessage: an argument asked for more bytes than the message
	// body has left.
	ErrShortMessage = errors.New("wlcore: message shorter than its arguments")
	// ErrBadString: a string argument did not end in a nul.
	ErrBadString = errors.New("wlcore: string without nul terminator")
	// ErrNoFD: an event declared a file descriptor and none arrived in the
	// ancillary data.
	ErrNoFD = errors.New("wlcore: expected an fd and the queue is empty")
	// ErrMessageTooLarge: a message announced a size past the 16-bit
	// maximum the header can express.
	ErrMessageTooLarge = errors.New("wlcore: message larger than the wire format maximum")
)

// Decoder deserializes Wayland arguments from the wire format. Two rules:
// it never panics (the body comes from the other side of the socket,
// untrusted input), and the error is sticky — checked once with Err()
// after reading all the arguments.
type Decoder struct {
	buf  []byte
	off  int
	conn *Conn
	err  error
}

func (c *Conn) newDecoder(body []byte) *Decoder {
	return &Decoder{buf: body, conn: c}
}

// Err returns the first decoding error, or nil. Read the arguments first
// and check once at the end: every reader returns a zero value after a
// failure instead of panicking, so a bad message costs one check, not one
// per argument.
func (d *Decoder) Err() error { return d.err }

func (d *Decoder) fail(err error) {
	if d.err == nil { // the first error is the informative one
		d.err = err
	}
}

// take is the only place that indexes buf.
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

// Uint32 reads a 32-bit argument, or returns 0 once the Decoder has
// failed. Check [Decoder.Err] before trusting the result.
func (d *Decoder) Uint32() uint32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return binary.NativeEndian.Uint32(b)
}

// ID reads an object id. Resolve it with [Conn.Lookup]: the compositor can
// name an id this client never created, so a nil result is untrusted input,
// not an internal error.
func (d *Decoder) ID() uint32 { return d.Uint32() }

// Int32 reads a signed 32-bit argument.
func (d *Decoder) Int32() int32 { return int32(d.Uint32()) }

// Fixed reads a 24.8 fixed-point argument; convert it with
// [Fixed.Float64].
func (d *Decoder) Fixed() Fixed { return Fixed(d.Uint32()) }

// lenPrefixed is the logic shared by string and array: length + payload
// with padding. The length is validated against what's left BEFORE
// aligning.
func (d *Decoder) lenPrefixed() ([]byte, int) {
	n := int(d.Uint32())
	if n < 0 || n > len(d.buf)-d.off {
		d.fail(ErrShortMessage)
		return nil, 0
	}
	return d.take(align4(n)), n
}

// String reads a length-prefixed string and drops its nul terminator,
// failing with [ErrBadString] if there is none. It returns "" on failure,
// which an argument declared allow-null cannot distinguish from a null
// string -- use [Decoder.StringOpt] there.
func (d *Decoder) String() string {
	b, n := d.lenPrefixed()
	if b == nil {
		return ""
	}
	if n == 0 || b[n-1] != 0 {
		d.fail(ErrBadString)
		return ""
	}
	return string(b[:n-1]) // the -1 eats the nul
}

// StringOpt distinguishes the null string (length 0, no nul and no data)
// from the case String() would reject.
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

// Array copies: the body is a view over the read buffer, which gets
// reused as soon as we read from the socket again.
func (d *Decoder) Array() []byte {
	b, n := d.lenPrefixed()
	if b == nil {
		return nil
	}
	return append([]byte(nil), b[:n]...)
}

// FD takes the next file descriptor off the queue the socket read filled,
// failing with [ErrNoFD] if the event promised one and none arrived. It
// returns -1 on failure.
//
// Ownership passes to the caller: whoever receives it closes it. A listener
// that ignores the event still has to, which is what [DropFD] is for.
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

// maxMessageSize: the header's size field occupies 16 bits (size<<16|opcode
// in a uint32). A message that exceeds it silently overflows those bits
// and corrupts the opcode on the other side; the guard lives here, not in
// Encoder.

// Send carries no mutex: it's part of the same single-threaded API as
// Register/SetListener/Dispatch (see "Who pumps" in wlcore.md).
func (c *Conn) Send(objectID uint32, opcode uint16, payload *Encoder, fds ...int) error {
	// A nil payload is a request with no arguments: the generator can emit
	// Send(id, op, nil) instead of an empty NewEncoder().
	var body []byte
	if payload != nil {
		body = payload.Bytes()
	}
	total := 8 + len(body)
	if total > maxMessageSize {
		return fmt.Errorf("%w (%d bytes, max %d)", ErrMessageTooLarge, total, maxMessageSize)
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

// Dispatch reads once from the socket and dispatches every complete
// message that came in. It blocks if there's nothing to read. Any error
// it returns is terminal and has already been recorded on the connection.
//
// Contract: only one goroutine may be inside at a time.
func (c *Conn) Dispatch() error {
	if err := c.dispatch(); err != nil {
		c.fatal(err)
		// c.err, not err: if this comes from a Close(), the real error is
		// ErrClosed and not the "use of closed network connection" the
		// read returns when it finds the socket closed underneath it.
		return c.err
	}
	// dispatch() can have gone fine and still leave the connection dead: a
	// listener it called registered a terminal error on its own
	// (wl_display.error is exactly that). Without this check, Dispatch —
	// and with it Roundtrip — would return nil after a protocol error.
	return c.err
}

func (c *Conn) dispatch() error {
	n, oobn, flags, _, err := c.sock.ReadMsgUnix(c.in.free(), c.oob)
	if err != nil {
		return err
	}
	// Without this, the kernel silently drops fds that don't fit in oob.
	if flags&unix.MSG_CTRUNC != 0 {
		return errors.New("wlcore: ancillary data truncated, fds lost")
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

// Run pumps until the connection dies. It's the last thing main does.
func (c *Conn) Run() error {
	defer c.fds.drain()
	for {
		if err := c.Dispatch(); err != nil {
			return err
		}
	}
}

// DrainFDs closes the pending fds. Only needed when pumping by hand with
// Dispatch() instead of Run(); call it from the same goroutine that was
// pumping, and only after the last Dispatch().
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

		// maxMessageSize, not readBufSize: a header declaring 65536 is
		// illegal by wire format even if it fits in the buffer.
		if size < 8 || size > maxMessageSize {
			return fmt.Errorf("wlcore: corrupt header (size=%d)", size)
		}
		if len(in) < size {
			return nil // incomplete message, wait for more bytes
		}

		if obj := c.Lookup(objectID); obj != nil {
			if err := obj.Dispatch(opcode, c.newDecoder(in[8:size])); err != nil {
				return fmt.Errorf("wlcore: object %d, opcode %d: %w", objectID, opcode, err)
			}
		}
		// if the object isn't there, it's ignored (can legitimately happen)
		c.in.discard(size)
	}
}
