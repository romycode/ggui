// Command scaling opens a Wayland window that renders at the right pixel
// density on whatever compositor it finds, instead of requiring a specific
// extension the way the hidpi example does.
//
// Both extensions the hidpi example depends on are optional, so a real
// client cannot just exit when they are missing. This one negotiates, at
// startup, the best of three mechanisms the compositor actually offers:
//
//  1. wp_fractional_scale_v1 + wp_viewport — the only path that expresses a
//     non-integer scale exactly. The buffer is rendered at the output's real
//     pixel density and wp_viewport.set_destination states the logical size.
//  2. wl_surface.preferred_buffer_scale (wl_surface 6+) — the compositor
//     names an integer factor directly. No extension needed, but 1.5x
//     rounds to 2x.
//  3. wl_surface.enter/leave + wl_output.scale — the same integer factor,
//     derived by hand from the outputs the surface currently sits on. This
//     is what every client did before the other two existed.
//
// The window logs which one it picked. It draws the same grid as the hidpi
// example — spacing in logical pixels, lines one physical pixel wide — so
// the difference between the paths is visible: on a 1.5x output path 1
// stays hairline while paths 2 and 3 render at 2x and get resampled down.
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
	// The integer paths use the same unit, storing factor * 120, so the rest
	// of the code never has to care which path produced the number.
	scaleDenominator = 120

	// gridStep is the grid spacing in logical pixels — it has to grow with
	// the scale while the lines do not.
	gridStep = 16

	bytesPerPixel = 4
)

// scaleMode is which of the three mechanisms this run settled on. It is
// decided once, after the initial roundtrip has revealed every global, and
// never changes afterwards.
type scaleMode int

const (
	// modeUnset is the value before the roundtrip has finished. It matters:
	// wl_output.scale events arrive during that roundtrip, and the handlers
	// use this to know the surface does not exist yet.
	modeUnset scaleMode = iota
	modeFractional
	modePreferred
	modeOutputs
)

func (m scaleMode) String() string {
	switch m {
	case modeFractional:
		return "fractional (wp_fractional_scale_v1 + wp_viewport)"
	case modePreferred:
		return "integer (wl_surface.preferred_buffer_scale)"
	case modeOutputs:
		return "integer (wl_surface.enter + wl_output.scale)"
	}
	return "unset"
}

type window struct {
	shm     *wlcore.Shm
	surface *wlcore.Surface

	mode scaleMode
	// viewport is set only in modeFractional.
	viewport *viewporter.Viewport

	// width and height are logical (surface-local) pixels; scale is in
	// scaleDenominator-ths, whichever path produced it.
	width, height int32
	scale         uint32

	// outputScales and enteredOutputs back modeOutputs only: the scale of
	// every wl_output that has announced one, and the ids of the outputs the
	// surface is currently displayed on. A surface can span two monitors, so
	// the effective factor is the largest of them.
	outputScales   map[uint32]int32
	enteredOutputs map[uint32]bool

	// configured gates the first draw: nothing may be attached before the
	// first xdg_surface.configure has been acked.
	configured bool
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
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

	// Built before the roundtrip because wl_output.scale events arrive
	// during it, and their handler writes into outputScales.
	w := &window{
		width:          defaultWidth,
		height:         defaultHeight,
		scale:          scaleDenominator,
		outputScales:   make(map[uint32]int32),
		enteredOutputs: make(map[uint32]bool),
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
			case wlcore.OutputInterface.Name:
				// Bound whichever path wins: wl_surface.enter resolves its
				// argument against the objects this client has registered,
				// so an unbound output would arrive as a nil *Output.
				var output *wlcore.Output
				output, err = reg.Bind(name, version, wlcore.OutputInterface)
				if output != nil {
					w.trackOutput(output)
				}
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
	// Every global that exists has been announced and dispatched by the time
	// this returns: the server processed get_registry, and therefore queued
	// its global events, before the sync this waits on. A nil below really
	// means absent, not "not yet".
	if err := conn.Roundtrip(); err != nil {
		return fmt.Errorf("roundtrip: %w", err)
	}
	if bindErr != nil {
		return fmt.Errorf("bind global: %w", bindErr)
	}
	if compositor == nil || shm == nil || wmBase == nil {
		return errors.New("compositor is missing wl_compositor, wl_shm or xdg_wm_base")
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
	w.shm, w.surface = shm, surface

	if err := w.selectMode(scaleMgr, vpter); err != nil {
		return err
	}
	log.Printf("scaling path: %s", w.mode)

	xdgSurface, err := wmBase.GetXdgSurface(surface)
	if err != nil {
		return fmt.Errorf("get_xdg_surface: %w", err)
	}

	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return fmt.Errorf("get_toplevel: %w", err)
	}
	if err := toplevel.SetTitle("ggui scaling window"); err != nil {
		return fmt.Errorf("set_title: %w", err)
	}
	if err := toplevel.SetAppID("ggui.example.scaling"); err != nil {
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

// selectMode picks the best scaling mechanism the compositor supports and
// wires up whichever listener feeds it. Exactly one of the three is armed:
// wl_surface has a single listener, so the two integer paths cannot both be
// live, and the fractional path uses its own object entirely.
func (w *window) selectMode(scaleMgr *fractionalscale.FractionalScaleManager, vpter *viewporter.Viewporter) error {
	switch {
	case scaleMgr != nil && vpter != nil:
		w.mode = modeFractional

		viewport, err := vpter.GetViewport(w.surface)
		if err != nil {
			return fmt.Errorf("get_viewport: %w", err)
		}
		w.viewport = viewport

		// Created before the first commit so the scale can arrive in the
		// same batch as the first configure, instead of forcing a redraw.
		fracScale, err := scaleMgr.GetFractionalScale(w.surface)
		if err != nil {
			return fmt.Errorf("get_fractional_scale: %w", err)
		}
		fracScale.SetListener(fractionalscale.FractionalScaleListener{
			PreferredScale: func(scale uint32) { w.setScale(scale) },
		})

	case w.surface.Version() >= 6:
		// wl_surface.preferred_buffer_scale, added in wl_surface 6: the
		// compositor has already done the "which output is this on" work
		// that modeOutputs below has to do by hand.
		w.mode = modePreferred
		w.surface.SetListener(wlcore.SurfaceListener{
			PreferredBufferScale: func(factor int32) {
				if factor < 1 {
					return
				}
				w.setScale(uint32(factor) * scaleDenominator)
			},
		})

	default:
		w.mode = modeOutputs
		w.surface.SetListener(wlcore.SurfaceListener{
			Enter: func(output *wlcore.Output) {
				if output == nil {
					return
				}
				w.enteredOutputs[output.ID()] = true
				w.refreshOutputScale()
			},
			Leave: func(output *wlcore.Output) {
				if output == nil {
					return
				}
				delete(w.enteredOutputs, output.ID())
				w.refreshOutputScale()
			},
		})
	}
	return nil
}

// trackOutput records an output's scale as the compositor reports it. Called
// for every wl_output at bind time, before the mode is known, because the
// scale event for an output arrives right after binding it — attaching this
// later would miss it.
func (w *window) trackOutput(output *wlcore.Output) {
	id := output.ID()
	w.outputScales[id] = 1
	output.SetListener(wlcore.OutputListener{
		Scale: func(factor int32) {
			if factor < 1 {
				return
			}
			w.outputScales[id] = factor
			w.refreshOutputScale()
		},
	})
}

// refreshOutputScale recomputes the factor from the outputs the surface is
// on. A no-op outside modeOutputs, including during the initial roundtrip,
// when the mode is still modeUnset and there is no surface yet.
func (w *window) refreshOutputScale() {
	if w.mode != modeOutputs {
		return
	}
	// A surface straddling a 1x and a 2x monitor is rendered at 2x and
	// downscaled on the 1x one: sharp on both, rather than blurry on one.
	best := int32(1)
	for id := range w.enteredOutputs {
		if s := w.outputScales[id]; s > best {
			best = s
		}
	}
	w.setScale(uint32(best) * scaleDenominator)
}

// setScale adopts a new scale and repaints, ignoring the no-op updates every
// path produces (an output re-announcing the scale it already had, a
// preferred_scale identical to the current one).
func (w *window) setScale(scale uint32) {
	if scale == 0 || scale == w.scale {
		return
	}
	w.scale = scale
	log.Printf("scale: %d/%d (%.2fx)", scale, scaleDenominator, float64(scale)/scaleDenominator)
	w.redraw()
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

	if err := w.present(bufWidth, bufHeight); err != nil {
		log.Printf("present: %v", err)
		return
	}
	if err := w.surface.Attach(buffer, 0, 0); err != nil {
		log.Printf("attach: %v", err)
		return
	}
	if err := w.surface.Commit(); err != nil {
		log.Printf("commit: %v", err)
	}
}

// present tells the compositor how to map the buffer onto the surface, which
// is the one thing that genuinely differs between the paths, and damages it.
func (w *window) present(bufWidth, bufHeight int32) error {
	if w.mode == modeFractional {
		// The destination is the logical size. This is what decouples the
		// surface size from the buffer size, and so the only way to state a
		// scale that is not a whole number.
		if err := w.viewport.SetDestination(w.width, w.height); err != nil {
			return fmt.Errorf("set_destination: %w", err)
		}
		// Buffer coordinates: with a viewport the two spaces differ by the
		// scale, and damage_buffer is the one measured in buffer pixels.
		return w.surface.DamageBuffer(0, 0, bufWidth, bufHeight)
	}

	factor := int32(w.scale / scaleDenominator)
	// Factor 1 is already the default, and skipping the request keeps this
	// path working on wl_compositor < 3, where set_buffer_scale does not
	// exist at all.
	if factor > 1 {
		if err := w.surface.SetBufferScale(factor); err != nil {
			return fmt.Errorf("set_buffer_scale: %w", err)
		}
	}
	// Surface coordinates, which under set_buffer_scale are the logical
	// ones. Unlike damage_buffer this exists at every wl_surface version,
	// which matters precisely on the compositors that land here.
	return w.surface.Damage(0, 0, w.width, w.height)
}

// newFrame allocates a width x height buffer in shared memory and draws the
// grid into it. width and height are physical pixels.
func (w *window) newFrame(width, height int32) (*wlcore.Buffer, error) {
	stride := width * bytesPerPixel
	size := int64(stride) * int64(height)

	fd, err := unix.MemfdCreate("ggui-scaling", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
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
		for y := int32(0); y < height; y++ {
			line(x, y)
		}
	}
	for i := int32(0); ; i++ {
		y := scaled(i*gridStep, scale)
		if y >= height {
			break
		}
		for x := int32(0); x < width; x++ {
			line(x, y)
		}
	}
}

// scaled converts a logical length to physical pixels, rounding up. Exact
// for the integer paths, where scale is always a multiple of the
// denominator; the rounding only ever matters in modeFractional.
func scaled(logical int32, scale uint32) int32 {
	return int32((int64(logical)*int64(scale) + scaleDenominator - 1) / scaleDenominator)
}
