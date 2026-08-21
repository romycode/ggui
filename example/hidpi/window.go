// Command hidpi opens a Wayland window that renders at the compositor's
// preferred *fractional* scale. It exercises the same xdg-shell handshake as
// the plain window example, plus the two extensions that make non-integer
// scaling work: wp_fractional_scale_v1 to learn the scale the output wants,
// and wp_viewport to hand the compositor a buffer rendered at that scale.
//
// The pair is needed because wl_surface.set_buffer_scale only takes an
// integer: on a 1.5x output a client limited to it must either render at 1x
// and let the compositor upscale (blurry) or render at 2x and let it
// downscale (wasteful). wp_viewport.set_destination takes the logical size
// directly, so the buffer can be any size at all — here, exactly the
// physical pixels the output has.
//
// The window draws a grid whose spacing is a fixed number of *logical*
// pixels but whose lines are always one *physical* pixel: at any scale the
// lines stay hairline instead of smearing, which is what correct fractional
// scaling looks like.
package main

import (
	"errors"
	"fmt"
	"log"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/fractionalscale"
	"github.com/romycode/ggui/wayland/viewporter"
	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

const (
	defaultWidth  = 800
	defaultHeight = 600

	// scaleDenominator is the denominator wp_fractional_scale_v1 fixes for
	// its preferred_scale numerator: 120 is 1.0, 180 is 1.5, 240 is 2.0.
	scaleDenominator = 120

	// gridStep is the grid spacing in logical pixels. It is deliberately
	// logical, not physical: the spacing has to grow with the scale (so the
	// grid looks the same size on any output) while the lines do not.
	gridStep = 16

	bytesPerPixel = 4
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// window is the mutable state a redraw needs. The plain window example gets
// away with closures over locals; here the scale, the logical size and the
// "has the surface been configured yet" flag are written by three different
// listeners and read by one redraw, so they live together.
type window struct {
	shm      *wlcore.Shm
	surface  *wlcore.Surface
	viewport *viewporter.Viewport

	// width and height are logical (surface-local) pixels — what xdg-shell
	// configures and what the viewport destination is set to. The buffer is
	// bigger than this whenever scale > 120.
	width, height int32

	// scale is the preferred_scale numerator, over scaleDenominator. It
	// starts at 1.0 because the compositor is not obliged to send
	// preferred_scale before the first configure.
	scale uint32

	// configured gates the first draw: nothing may be attached before the
	// first xdg_surface.configure has been acked.
	configured bool
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

	var (
		compositor *wlcore.Compositor
		shm        *wlcore.Shm
		wmBase     *xdgshell.WmBase
		scaleMgr   *fractionalscale.FractionalScaleManager
		vpter      *viewporter.Viewporter
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
			case fractionalscale.FractionalScaleManagerInterface.Name:
				scaleMgr, err = reg.Bind(name, version, fractionalscale.FractionalScaleManagerInterface)
			case viewporter.ViewporterInterface.Name:
				vpter, err = reg.Bind(name, version, viewporter.ViewporterInterface)
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
	if compositor == nil || shm == nil || wmBase == nil {
		return errors.New("compositor is missing wl_compositor, wl_shm or xdg_wm_base")
	}
	// Both extensions are optional in the protocol but mandatory here: the
	// example has nothing to show without them, so say so instead of
	// silently falling back to integer scaling.
	if scaleMgr == nil || vpter == nil {
		return errors.New("compositor is missing wp_fractional_scale_manager_v1 or wp_viewporter")
	}

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

	viewport, err := vpter.GetViewport(surface)
	if err != nil {
		return fmt.Errorf("get_viewport: %w", err)
	}

	w := &window{
		shm:      shm,
		surface:  surface,
		viewport: viewport,
		width:    defaultWidth,
		height:   defaultHeight,
		scale:    scaleDenominator,
	}

	// Created before the first commit so the compositor can report the
	// scale in the same batch as the first configure — otherwise the window
	// would draw once at 1.0 and immediately redraw.
	fracScale, err := scaleMgr.GetFractionalScale(surface)
	if err != nil {
		return fmt.Errorf("get_fractional_scale: %w", err)
	}
	fracScale.SetListener(fractionalscale.FractionalScaleListener{
		PreferredScale: func(scale uint32) {
			if scale == 0 || scale == w.scale {
				return
			}
			w.scale = scale
			log.Printf("preferred scale: %d/%d (%.2fx)", scale, scaleDenominator, float64(scale)/scaleDenominator)
			w.redraw()
		},
	})

	xdgSurface, err := wmBase.GetXdgSurface(surface)
	if err != nil {
		return fmt.Errorf("get_xdg_surface: %w", err)
	}

	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return fmt.Errorf("get_toplevel: %w", err)
	}
	if err := toplevel.SetTitle("ggui hidpi window"); err != nil {
		return fmt.Errorf("set_title: %w", err)
	}
	if err := toplevel.SetAppID("ggui.example.hidpi"); err != nil {
		return fmt.Errorf("set_app_id: %w", err)
	}

	toplevel.SetListener(xdgshell.ToplevelListener{
		Configure: func(width, height int32, _ []byte) {
			// A zero width/height means "you decide" — keep whatever we have.
			// These are logical pixels, unaffected by the scale.
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
			w.configured = true
			w.redraw()
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

// redraw renders one frame at the current scale and commits it. Called from
// listeners, so it logs its errors instead of returning them: a failed
// redraw is not a reason to tear the connection down, and by the time the
// connection really is broken conn.Run() reports it anyway.
func (w *window) redraw() {
	if !w.configured {
		return
	}

	bufWidth, bufHeight := scaled(w.width, w.scale), scaled(w.height, w.scale)
	buffer, err := w.newFrame(bufWidth, bufHeight)
	if err != nil {
		log.Printf("frame: %v", err)
		return
	}

	// The destination is the *logical* size, and it is what makes the whole
	// thing work: it decouples the surface size from the buffer size, so the
	// buffer above could be any resolution and the window still occupies the
	// size xdg-shell asked for.
	if err := w.viewport.SetDestination(w.width, w.height); err != nil {
		log.Printf("set_destination: %v", err)
		return
	}
	if err := w.surface.Attach(buffer, 0, 0); err != nil {
		log.Printf("attach: %v", err)
		return
	}
	// Buffer coordinates, not surface coordinates: with a viewport in play
	// the two differ by the scale, and damage_buffer is the one that takes
	// the buffer's own pixels.
	if err := w.surface.DamageBuffer(0, 0, bufWidth, bufHeight); err != nil {
		log.Printf("damage_buffer: %v", err)
		return
	}
	if err := w.surface.Commit(); err != nil {
		log.Printf("commit: %v", err)
	}
}

// newFrame allocates a width x height buffer in shared memory and draws the
// grid into it. width and height are physical pixels.
func (w *window) newFrame(width, height int32) (*wlcore.Buffer, error) {
	stride := width * bytesPerPixel
	size := int64(stride) * int64(height)

	fd, err := unix.MemfdCreate("ggui-hidpi", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
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
	drawGrid(data, width, height, w.scale)
	if err := unix.Munmap(data); err != nil {
		return nil, fmt.Errorf("munmap: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		return nil, fmt.Errorf("fcntl seal: %w", err)
	}

	pool, err := w.shm.CreatePool(fd, int32(size))
	if err != nil {
		return nil, fmt.Errorf("create_pool: %w", err)
	}
	defer pool.Destroy()

	buffer, err := pool.CreateBuffer(0, width, height, stride, wlcore.ShmFormatXrgb8888)
	if err != nil {
		return nil, fmt.Errorf("create_buffer: %w", err)
	}
	// Every redraw allocates a fresh buffer, and the scale can change at any
	// time, so the old one has to go — but only once the compositor says it
	// is done reading it. Destroying it any earlier pulls the pixels out
	// from under the frame currently on screen.
	buffer.SetListener(wlcore.BufferListener{
		Release: func() {
			// The only way this fails is a dead socket, which conn.Run()
			// is already reporting.
			_ = buffer.Destroy()
		},
	})
	return buffer, nil
}

// drawGrid fills the buffer with a background and rules a grid over it. The
// lines are one physical pixel wide at every scale; only their spacing grows.
func drawGrid(data []byte, width, height int32, scale uint32) {
	// xrgb8888, little-endian: byte order per pixel is B, G, R, x.
	for i := 0; i < len(data); i += bytesPerPixel {
		data[i+0] = 0xea
		data[i+1] = 0xef
		data[i+2] = 0xf2
		data[i+3] = 0x00
	}

	line := func(x, y int32) {
		i := (y*width + x) * bytesPerPixel
		data[i+0] = 0x50
		data[i+1] = 0x40
		data[i+2] = 0x30
		data[i+3] = 0x00
	}

	for i := int32(0); ; i++ {
		x := scaled(i*gridStep, scale)
		if x >= width {
			break
		}
		for y := range height {
			line(x, y)
		}
	}
	for i := int32(0); ; i++ {
		y := scaled(i*gridStep, scale)
		if y >= height {
			break
		}
		for x := range width {
			line(x, y)
		}
	}
}

// scaled converts a logical length to physical pixels, rounding up. The
// viewport stretches whatever it is given to the logical destination size,
// so rounding down would just mean rendering slightly fewer pixels than the
// output can actually show.
func scaled(logical int32, scale uint32) int32 {
	return int32((int64(logical)*int64(scale) + scaleDenominator - 1) / scaleDenominator)
}
