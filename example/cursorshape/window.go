// Command cursorshape opens a Wayland window split into three vertical
// bands and swaps the pointer's cursor shape as it crosses band boundaries,
// using wp_cursor_shape_manager_v1 instead of wl_pointer.set_cursor. Unlike
// the surface-based request, wp_cursor_shape_device_v1.set_shape takes an
// enumerated shape — no cursor theme, no image, no hotspot bookkeeping.
//
// The catch is the serial: set_shape only takes effect when its serial
// argument matches the wl_pointer.enter serial most recently sent to this
// client, and that serial does not expire on its own — it stays valid for
// every set_shape call until the pointer leaves and re-enters. That is what
// lets this example change shape on every wl_pointer.motion instead of only
// once per enter.
package main

import (
	"errors"
	"fmt"
	"log"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/cursorshape"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

const (
	defaultWidth  = 800
	defaultHeight = 600
	bytesPerPixel = 4
)

// band pairs the shape a pointer gets in a region of the window with the
// fill color that marks the region, so the switch is visible even though
// most cursor themes don't render every shape distinctly.
type band struct {
	name  string
	shape cursorshape.CursorShapeDeviceShape
	// r, g, b are xrgb8888 channel values for the band's fill.
	r, g, b byte
}

// bands divides the window into three equal vertical stripes, left to
// right. Text and resize cursors are the two shapes an app is most likely
// to switch to dynamically as the pointer crosses content, so those anchor
// the demo instead of two arbitrary picks.
var bands = []band{
	{name: "default", shape: cursorshape.CursorShapeDeviceShapeDefault, r: 0xe8, g: 0xe8, b: 0xe8},
	{name: "text", shape: cursorshape.CursorShapeDeviceShapeText, r: 0xcf, g: 0xe3, b: 0xff},
	{name: "ew-resize", shape: cursorshape.CursorShapeDeviceShapeEwResize, r: 0xff, g: 0xe3, b: 0xcf},
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// window is the state the pointer listener and the redraw both need. The
// redraw only depends on size, since the bands are a fraction of it; the
// pointer listener needs the device to call set_shape on, the last enter
// serial to call it with, and the current band so it only calls set_shape
// on an actual crossing instead of on every motion event.
type window struct {
	width, height int32

	capabilities wlcore.SeatCapability
	device       *cursorshape.CursorShapeDevice
	enterSerial  uint32
	haveSerial   bool
	currentBand  int
}

func run() error {
	conn, err := wlcore.Connect()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	conn.OnError(func(objectID, code uint32, msg string) {
		log.Printf("wayland: protocol error on object %d (code %d): %s", objectID, code, msg)
	})

	reg, err := conn.Display().GetRegistry()
	if err != nil {
		return fmt.Errorf("get_registry: %w", err)
	}

	w := &window{width: defaultWidth, height: defaultHeight, currentBand: -1}

	var (
		compositor *wlcore.Compositor
		shm        *wlcore.Shm
		wmBase     *xdgshell.WmBase
		seat       *wlcore.Seat
		shapeMgr   *cursorshape.CursorShapeManager
		bindErr    error
	)
	reg.SetListener(wlcore.RegistryListener{
		Global: func(name uint32, iface string, version uint32) {
			// err is per-event on purpose: assigning straight into bindErr
			// from every branch lets a later successful bind overwrite an
			// earlier failure, and the first failure is the interesting one.
			var err error
			switch iface {
			case wlcore.CompositorInterface.Name:
				compositor, err = reg.Bind(name, version, wlcore.CompositorInterface)
			case wlcore.ShmInterface.Name:
				shm, err = reg.Bind(name, version, wlcore.ShmInterface)
			case xdgshell.WmBaseInterface.Name:
				wmBase, err = reg.Bind(name, version, xdgshell.WmBaseInterface)
			case wlcore.SeatInterface.Name:
				seat, err = reg.Bind(name, version, wlcore.SeatInterface)
				if seat != nil {
					// wl_seat.capabilities arrives right after bind, in the
					// same batch this Global callback is running in — the
					// listener has to be live now, not after the registry
					// roundtrip returns, or the first event is lost. Only
					// the capability bitmask is recorded here; armPointer,
					// called again once shapeMgr is known, does the rest.
					seat.SetListener(wlcore.SeatListener{
						Capabilities: func(capabilities wlcore.SeatCapability) {
							w.capabilities = capabilities
							w.armPointer(seat, shapeMgr)
						},
					})
				}
			case cursorshape.CursorShapeManagerInterface.Name:
				shapeMgr, err = reg.Bind(name, version, cursorshape.CursorShapeManagerInterface)
			}
			if err != nil && bindErr == nil {
				bindErr = err
			}
		},
	})
	if err := conn.Roundtrip(); err != nil {
		return fmt.Errorf("roundtrip: %w", err)
	}
	if bindErr != nil {
		return fmt.Errorf("bind global: %w", bindErr)
	}
	if compositor == nil || shm == nil || wmBase == nil || seat == nil {
		return errors.New("compositor is missing wl_compositor, wl_shm, xdg_wm_base or wl_seat")
	}
	if shapeMgr == nil {
		return errors.New("compositor is missing wp_cursor_shape_manager_v1")
	}
	// Covers the case where wl_seat.capabilities already arrived above but
	// shapeMgr was still nil at that point, because wl_seat happened to be
	// announced before wp_cursor_shape_manager_v1: shapeMgr is guaranteed
	// non-nil from here on, so this retry is the one that actually arms it.
	w.armPointer(seat, shapeMgr)

	wmBase.SetListener(xdgshell.WmBaseListener{
		Ping: func(serial uint32) {
			// A close event dispatched earlier in the same Dispatch() batch
			// may have already closed the connection — nothing to pong to.
			if conn.Err() != nil {
				return
			}
			if err := wmBase.Pong(serial); err != nil {
				log.Printf("pong: %v", err)
			}
		},
	})

	surface, err := compositor.CreateSurface()
	if err != nil {
		return fmt.Errorf("create_surface: %w", err)
	}

	xdgSurface, err := wmBase.GetXdgSurface(surface)
	if err != nil {
		return fmt.Errorf("get_xdg_surface: %w", err)
	}

	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return fmt.Errorf("get_toplevel: %w", err)
	}
	if err := toplevel.SetTitle("ggui cursor shape"); err != nil {
		return fmt.Errorf("set_title: %w", err)
	}
	if err := toplevel.SetAppID("ggui.example.cursorshape"); err != nil {
		return fmt.Errorf("set_app_id: %w", err)
	}

	toplevel.SetListener(xdgshell.ToplevelListener{
		Configure: func(width, height int32, _ []byte) {
			// A zero width/height means "you decide" — keep whatever we have.
			if width > 0 && height > 0 {
				w.width, w.height = width, height
			}
		},
		Close: func() {
			conn.Close()
		},
	})

	xdgSurface.SetListener(xdgshell.SurfaceListener{
		Configure: func(serial uint32) {
			// Nothing may be attached before ack_configure — see wlcore.md.
			if err := xdgSurface.AckConfigure(serial); err != nil {
				log.Printf("ack_configure: %v", err)
				return
			}
			if err := w.redraw(shm, surface); err != nil {
				log.Printf("redraw: %v", err)
			}
		},
	})

	// Initial commit without a buffer: this is what makes the compositor
	// send the first xdg_surface.configure.
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}

	if err := conn.Run(); err != nil && !errors.Is(err, wlcore.ErrClosed) {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}

// armPointer obtains a wl_pointer and, from it, a cursor shape device, the
// first time the seat reports a pointer capability and shapeMgr is known.
// It is safe to call repeatedly: everything after the first successful call
// is a no-op, which is what lets both call sites above share it.
func (w *window) armPointer(seat *wlcore.Seat, shapeMgr *cursorshape.CursorShapeManager) {
	if w.device != nil || shapeMgr == nil || !w.capabilities.Has(wlcore.SeatCapabilityPointer) {
		return
	}

	pointer, err := seat.GetPointer()
	if err != nil {
		log.Printf("get_pointer: %v", err)
		return
	}
	device, err := shapeMgr.GetPointer(pointer)
	if err != nil {
		log.Printf("cursor shape get_pointer: %v", err)
		return
	}
	w.device = device

	pointer.SetListener(wlcore.PointerListener{
		Enter: func(serial uint32, _ *wlcore.Surface, surfaceX, _ wlcore.Fixed) {
			w.enterSerial, w.haveSerial = serial, true
			w.currentBand = -1
			w.setShapeForX(surfaceX.Float64())
		},
		Leave: func(uint32, *wlcore.Surface) {
			// The serial only stays valid while this client has focus; the
			// next set_shape has to wait for the enter that follows.
			w.haveSerial = false
			w.currentBand = -1
		},
		Motion: func(_ uint32, surfaceX, _ wlcore.Fixed) {
			w.setShapeForX(surfaceX.Float64())
		},
	})
}

// setShapeForX sets the cursor to whichever band's shape the surface-local
// x coordinate falls into, but only when that band actually changed — the
// protocol allows redundant set_shape calls, but there is no reason to send
// one on every motion event when the pointer hasn't left its band.
func (w *window) setShapeForX(x float64) {
	if !w.haveSerial || w.device == nil || w.width <= 0 {
		return
	}
	i := max(int(x)*len(bands)/int(w.width), 0)
	if i >= len(bands) {
		i = len(bands) - 1
	}
	if i == w.currentBand {
		return
	}
	w.currentBand = i
	if err := w.device.SetShape(w.enterSerial, bands[i].shape); err != nil {
		log.Printf("set_shape(%s): %v", bands[i].name, err)
		return
	}
	log.Printf("cursor shape: %s", bands[i].name)
}

// redraw renders the current band layout and commits it. width and height
// are read from w, which xdg-shell's Configure keeps up to date.
func (w *window) redraw(shm *wlcore.Shm, surface *wlcore.Surface) error {
	buffer, err := w.newFrame(shm)
	if err != nil {
		return fmt.Errorf("frame: %w", err)
	}
	if err := surface.Attach(buffer, 0, 0); err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	if err := surface.Damage(0, 0, w.width, w.height); err != nil {
		return fmt.Errorf("damage: %w", err)
	}
	return surface.Commit()
}

// newFrame allocates a width x height buffer in shared memory and paints
// the band layout into it.
func (w *window) newFrame(shm *wlcore.Shm) (*wlcore.Buffer, error) {
	stride := w.width * bytesPerPixel
	size := int64(stride) * int64(w.height)

	fd, err := unix.MemfdCreate("ggui-cursorshape", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("memfd_create: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Ftruncate(fd, size); err != nil {
		return nil, fmt.Errorf("ftruncate: %w", err)
	}

	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	drawBands(data, w.width, w.height)
	if err := unix.Munmap(data); err != nil {
		return nil, fmt.Errorf("munmap: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		return nil, fmt.Errorf("fcntl seal: %w", err)
	}

	pool, err := shm.CreatePool(fd, int32(size))
	if err != nil {
		return nil, fmt.Errorf("create_pool: %w", err)
	}
	defer pool.Destroy()

	buffer, err := pool.CreateBuffer(0, w.width, w.height, stride, wlcore.ShmFormatXrgb8888)
	if err != nil {
		return nil, fmt.Errorf("create_buffer: %w", err)
	}
	buffer.SetListener(wlcore.BufferListener{
		Release: func() {
			// The only way this fails is a dead socket, which conn.Run()
			// is already reporting.
			_ = buffer.Destroy()
		},
	})
	return buffer, nil
}

// drawBands fills each vertical stripe of the buffer with its band's color,
// using the same width-based split setShapeForX uses to pick a shape — the
// two have to agree, or the color under the pointer would not match the
// shape it just got.
func drawBands(data []byte, width, height int32) {
	for x := range width {
		i := int(x) * len(bands) / int(width)
		if i >= len(bands) {
			i = len(bands) - 1
		}
		b := bands[i]
		for y := range height {
			// xrgb8888, little-endian: byte order per pixel is B, G, R, x.
			o := (y*width + x) * bytesPerPixel
			data[o+0] = b.b
			data[o+1] = b.g
			data[o+2] = b.r
			data[o+3] = 0x00
		}
	}
}
