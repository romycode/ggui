package wltest

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"golang.org/x/sys/unix"
)

// The opcodes the fake needs. They mirror the unexported opReq*/opEvt*
// constants the generator writes into wayland/wlcore and wayland/xdgshell:
// those are the normative copy, and a change there has to be mirrored here.
const (
	displayID = 1

	reqDisplaySync        = 0
	reqDisplayGetRegistry = 1
	evtDisplayError       = 0
	evtDisplayDeleteID    = 1

	reqRegistryBind   = 0
	evtRegistryGlobal = 0

	evtCallbackDone = 0

	reqCompositorCreateSurface = 0
	reqCompositorCreateRegion  = 1

	reqShmCreatePool = 0
	reqShmRelease    = 1
	evtShmFormat     = 0

	reqShmPoolCreateBuffer = 0
	reqShmPoolDestroy      = 1
	reqShmPoolResize       = 2

	reqBufferDestroy = 0
	evtBufferRelease = 0

	reqSurfaceDestroy      = 0
	reqSurfaceAttach       = 1
	reqSurfaceDamage       = 2
	reqSurfaceFrame        = 3
	reqSurfaceCommit       = 6
	reqSurfaceDamageBuffer = 9

	reqSeatGetPointer   = 0
	reqSeatGetKeyboard  = 1
	reqSeatGetTouch     = 2
	reqSeatRelease      = 3
	evtSeatCapabilities = 0
	evtSeatName         = 1

	reqKeyboardRelease    = 0
	evtKeyboardKeymap     = 0
	evtKeyboardEnter      = 1
	evtKeyboardLeave      = 2
	evtKeyboardKey        = 3
	evtKeyboardModifiers  = 4
	evtKeyboardRepeatInfo = 5

	reqPointerRelease = 1
	evtPointerEnter   = 0
	evtPointerLeave   = 1
	evtPointerMotion  = 2
	evtPointerButton  = 3
	evtPointerFrame   = 5

	reqWmBaseDestroy       = 0
	reqWmBaseGetXdgSurface = 2
	reqWmBasePong          = 3
	evtWmBasePing          = 0

	reqXdgSurfaceDestroy           = 0
	reqXdgSurfaceGetToplevel       = 1
	reqXdgSurfaceSetWindowGeometry = 3
	reqXdgSurfaceAckConfigure      = 4
	evtXdgSurfaceConfigure         = 0

	reqToplevelDestroy   = 0
	reqToplevelSetTitle  = 2
	reqToplevelSetAppID  = 3
	evtToplevelConfigure = 0
	evtToplevelClose     = 1
)

// Keymap formats and key/button states, as wl_keyboard and wl_pointer
// number them.
const (
	keymapFormatXkbV1 = 1
	keyStateReleased  = 0
	keyStatePressed   = 1
)

// seatCapabilityPointer and seatCapabilityKeyboard are wl_seat.capability
// bits.
const (
	seatCapabilityPointer  = 1
	seatCapabilityKeyboard = 2
)

// shmFormatArgb8888 and shmFormatXrgb8888 are the only two wl_shm formats
// the fake advertises: the two every compositor supports.
const (
	shmFormatArgb8888 = 0
	shmFormatXrgb8888 = 1
)

// requestNames maps an interface to its request names in opcode order, so
// Requests() can report "wl_surface.commit" instead of a number. Only the
// interfaces the fake tracks are listed; anything else shows up as "opN".
var requestNames = map[string][]string{
	"wl_display":    {"sync", "get_registry"},
	"wl_registry":   {"bind"},
	"wl_compositor": {"create_surface", "create_region"},
	"wl_region":     {"destroy", "add", "subtract"},
	"wl_shm":        {"create_pool", "release"},
	"wl_shm_pool":   {"create_buffer", "destroy", "resize"},
	"wl_buffer":     {"destroy"},
	"wl_surface": {
		"destroy", "attach", "damage", "frame", "set_opaque_region",
		"set_input_region", "commit", "set_buffer_transform",
		"set_buffer_scale", "damage_buffer", "offset",
	},
	"wl_seat":     {"get_pointer", "get_keyboard", "get_touch", "release"},
	"wl_keyboard": {"release"},
	"wl_pointer":  {"set_cursor", "release"},
	"xdg_wm_base": {"destroy", "create_positioner", "get_xdg_surface", "pong"},
	"xdg_surface": {"destroy", "get_toplevel", "get_popup", "set_window_geometry", "ack_configure"},
	"xdg_toplevel": {
		"destroy", "set_parent", "set_title", "set_app_id", "show_window_menu",
		"move", "resize", "set_max_size", "set_min_size", "set_maximized",
		"unset_maximized", "set_fullscreen", "unset_fullscreen", "set_minimized",
	},
}

// requestName renders one request as the protocol names it.
func requestName(iface string, opcode uint16) string {
	if names, ok := requestNames[iface]; ok && int(opcode) < len(names) {
		return names[opcode]
	}
	return fmt.Sprintf("op%d", opcode)
}

// args reads a request body. Like wlcore's Decoder it never panics — the
// body comes off a socket — and the error is sticky.
type args struct {
	buf []byte
	off int
	err error
}

func newArgs(body []byte) *args { return &args{buf: body} }

func (a *args) take(n int) []byte {
	if a.err != nil {
		return nil
	}
	if a.off+n > len(a.buf) {
		a.err = io.ErrUnexpectedEOF
		return nil
	}
	b := a.buf[a.off : a.off+n]
	a.off += n
	return b
}

func (a *args) uint32() uint32 {
	b := a.take(4)
	if b == nil {
		return 0
	}
	return binary.NativeEndian.Uint32(b)
}

func (a *args) int32() int32 { return int32(a.uint32()) }

func (a *args) string() string {
	n := int(a.uint32())
	if n == 0 {
		return ""
	}
	b := a.take((n + 3) &^ 3)
	if b == nil {
		return ""
	}
	if b[n-1] != 0 {
		a.err = fmt.Errorf("wltest: string argument without a nul terminator")
		return ""
	}
	return string(b[:n-1])
}

// wireReader reassembles requests from the socket and keeps the file
// descriptors that came with them, in arrival order, the way libwayland
// does: an fd argument takes no room in the body, so a request that
// declares one pops the next queued descriptor.
type wireReader struct {
	sock    *net.UnixConn
	buf     []byte
	scratch []byte
	oob     []byte
	fds     []int
}

func newWireReader(sock *net.UnixConn) *wireReader {
	return &wireReader{
		sock:    sock,
		scratch: make([]byte, 64*1024),
		// The same cap libwayland uses for a single recvmsg.
		oob: make([]byte, unix.CmsgSpace(4*28)),
	}
}

// next returns the next complete request, blocking until one arrives. It
// sets no read deadline and inherits none: the loop that calls it stops by
// having the socket closed underneath it.
func (r *wireReader) next() (objectID uint32, opcode uint16, body []byte, err error) {
	for {
		if len(r.buf) >= 8 {
			size := int(binary.NativeEndian.Uint32(r.buf[4:8]) >> 16)
			if size < 8 {
				return 0, 0, nil, fmt.Errorf("wltest: request of %d bytes, shorter than a header", size)
			}
			if len(r.buf) >= size {
				objectID = binary.NativeEndian.Uint32(r.buf[0:4])
				opcode = uint16(binary.NativeEndian.Uint32(r.buf[4:8]) & 0xffff)
				body = make([]byte, size-8)
				copy(body, r.buf[8:size])
				r.buf = append(r.buf[:0], r.buf[size:]...)
				return objectID, opcode, body, nil
			}
		}
		if err := r.fill(); err != nil {
			return 0, 0, nil, err
		}
	}
}

func (r *wireReader) fill() error {
	n, oobn, _, _, err := r.sock.ReadMsgUnix(r.scratch, r.oob)
	if oobn > 0 {
		if scms, perr := unix.ParseSocketControlMessage(r.oob[:oobn]); perr == nil {
			for i := range scms {
				if fds, ferr := unix.ParseUnixRights(&scms[i]); ferr == nil {
					r.fds = append(r.fds, fds...)
				}
			}
		}
	}
	if n > 0 {
		r.buf = append(r.buf, r.scratch[:n]...)
	}
	if err != nil {
		return err
	}
	if n == 0 && oobn == 0 {
		return io.EOF
	}
	return nil
}

// popFD hands out the descriptor the next fd argument refers to.
func (r *wireReader) popFD() (int, bool) {
	if len(r.fds) == 0 {
		return 0, false
	}
	fd := r.fds[0]
	r.fds = r.fds[1:]
	return fd, true
}

// closeFDs closes whatever descriptors nobody claimed, on the way out.
func (r *wireReader) closeFDs() {
	for _, fd := range r.fds {
		unix.Close(fd)
	}
	r.fds = nil
}
