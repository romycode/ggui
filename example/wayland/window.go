// Command window opens a plain Wayland window with no content: just a
// solid-color surface that can be moved, resized and closed. It exercises
// the full xdg-shell handshake (registry -> surface -> xdg_surface ->
// xdg_toplevel -> configure/ack_configure -> attach/commit) against the
// generated wlcore/xdgshell bindings.
package main

import (
	"errors"
	"fmt"
	"log"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/wayland/wlcore"
	"github.com/romycode/ggui/wayland/xdgshell"
)

const (
	defaultWidth  = 800
	defaultHeight = 600
)

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

	var (
		compositor *wlcore.Compositor
		shm        *wlcore.Shm
		wmBase     *xdgshell.WmBase
		bindErr    error
	)
	reg.SetListener(wlcore.RegistryListener{
		Global: func(name uint32, iface string, version uint32) {
			switch iface {
			case wlcore.CompositorInterface.Name:
				compositor, bindErr = reg.Bind(name, version, wlcore.CompositorInterface)
			case wlcore.ShmInterface.Name:
				shm, bindErr = reg.Bind(name, version, wlcore.ShmInterface)
			case xdgshell.WmBaseInterface.Name:
				wmBase, bindErr = reg.Bind(name, version, xdgshell.WmBaseInterface)
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
	if err := toplevel.SetTitle("ggui window"); err != nil {
		return fmt.Errorf("set_title: %w", err)
	}
	if err := toplevel.SetAppID("ggui.example.window"); err != nil {
		return fmt.Errorf("set_app_id: %w", err)
	}

	width, height := int32(defaultWidth), int32(defaultHeight)
	toplevel.SetListener(xdgshell.ToplevelListener{
		Configure: func(w, h int32, _ []byte) {
			// A zero width/height means "you decide" — keep whatever we have.
			if w > 0 && h > 0 {
				width, height = w, h
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
			buffer, err := blankBuffer(shm, width, height)
			if err != nil {
				log.Printf("blank buffer: %v", err)
				return
			}
			if err := surface.Attach(buffer, 0, 0); err != nil {
				log.Printf("attach: %v", err)
				return
			}
			if err := surface.Damage(0, 0, width, height); err != nil {
				log.Printf("damage: %v", err)
				return
			}
			if err := surface.Commit(); err != nil {
				log.Printf("commit: %v", err)
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

// blankBuffer allocates a shared-memory buffer of width x height and fills
// it with a solid, opaque color — there is no window content to draw yet,
// but xdg-shell requires a committed buffer before a toplevel can be mapped.
func blankBuffer(shm *wlcore.Shm, width, height int32) (*wlcore.Buffer, error) {
	const bytesPerPixel = 4
	stride := width * bytesPerPixel
	size := int64(stride) * int64(height)

	fd, err := unix.MemfdCreate("ggui-window", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
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
	// xrgb8888, little-endian: byte order per pixel is B, G, R, x.
	for i := 0; i < len(data); i += bytesPerPixel {
		data[i+0] = 0xe8
		data[i+1] = 0xe8
		data[i+2] = 0xe8
		data[i+3] = 0x00
	}
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

	return pool.CreateBuffer(0, width, height, stride, wlcore.ShmFormatXrgb8888)
}
