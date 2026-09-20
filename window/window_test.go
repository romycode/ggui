package window

import (
	"strings"
	"testing"
	"time"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/internal/wltest"
)

// appColor is what the test application paints. It is nothing the loader or
// the failure screen draws, so one pixel says whose frame a commit carries.
var appColor = canvas.Color{R: 0x20, G: 0x50, B: 0xc0, A: 0xff}

// appContent is an application that fills the window with appColor and never
// animates: its frames are unmistakable, and a window showing it must go
// quiet.
func appContent() Content {
	return Content{Paint: func(cv *canvas.Canvas, _ uint32) (animating bool) {
		cv.Clear(appColor)
		return false
	}}
}

// appInit is an init that is ready at once.
func appInit(*Window) (Content, error) { return appContent(), nil }

// appWord is appColor as the word canvas writes into a buffer, computed the
// way the window does it rather than hardcoded, so the test does not depend
// on the pixel layout.
func appWord(t *testing.T) uint32 {
	t.Helper()
	px := make([]uint32, 1)
	cv, err := canvas.New(canvas.Buffer{Pixels: px, Width: 1, Height: 1, Stride: 1}, 1, 1, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	cv.Clear(appColor)
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas: %v", err)
	}
	return px[0]
}

// testWindow is a window opened by run on its own goroutine, against the fake
// compositor. The test goroutine drives the compositor and reads what the
// window committed; it never touches the window's internals.
type testWindow struct {
	t   *testing.T
	srv *wltest.Server
	// err carries run's return value. Everything that takes it puts it back,
	// so the value stays available to whoever looks next.
	err chan error
}

// openWindow starts run against a fake compositor with opts, and stops it at
// the end of the test the way a compositor would: xdg_toplevel.close.
func openWindow(t *testing.T, opts wltest.Options, cfg Config, init func(*Window) (Content, error)) *testWindow {
	t.Helper()
	tw := &testWindow{t: t, srv: wltest.NewServer(t, opts), err: make(chan error, 1)}
	go func() { tw.err <- run(tw.srv.Conn(), cfg, init) }()
	t.Cleanup(tw.stop)
	return tw
}

// stop closes the window from the compositor's side and waits for run.
func (tw *testWindow) stop() {
	select {
	case err := <-tw.err:
		tw.err <- err
		return
	default:
	}
	tw.srv.CloseToplevel()
	tw.wait()
}

// wait returns what run returned, once it has.
func (tw *testWindow) wait() error {
	tw.t.Helper()
	select {
	case err := <-tw.err:
		tw.err <- err
		return err
	case <-time.After(settle):
		tw.t.Fatal("run did not return")
		return nil
	}
}

// nextCommit takes the next frame the compositor was given.
func (tw *testWindow) nextCommit() wltest.Commit {
	tw.t.Helper()
	select {
	case c := <-tw.srv.Commits():
		return c
	case err := <-tw.err:
		tw.err <- err
		tw.t.Fatalf("run returned before a frame was committed: %v", err)
	case <-time.After(settle):
		tw.t.Fatal("timed out waiting for a committed frame")
	}
	return wltest.Commit{}
}

// waitForAppFrame drains frames until the application's own pixels show up.
// Everything before it is the loader's.
func (tw *testWindow) waitForAppFrame() wltest.Commit {
	tw.t.Helper()
	want := appWord(tw.t)
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if len(c.Pixels) > 0 && c.Pixels[0] == want {
			return c
		}
		if time.Now().After(deadline) {
			tw.t.Fatal("the application's frame never reached the compositor")
		}
	}
}

// waitUntilQuiet drains frames until none has been committed for quiet, and
// fails the test if that never happens. The startup has frames of its own —
// the loader's, and the repaint each buffer's arrival asks for — so what a
// window that does not animate promises is not "no more frames from here" but
// that they stop coming; a window that animates never stops, since the fake
// answers a frame callback every 16 ms.
func (tw *testWindow) waitUntilQuiet(quiet time.Duration) {
	tw.t.Helper()
	deadline := time.Now().Add(settle)
	for {
		select {
		case c := <-tw.srv.Commits():
			if time.Now().After(deadline) {
				tw.t.Fatalf("the window never stopped committing frames (the last one at %v)", c.At)
			}
		case err := <-tw.err:
			tw.err <- err
			tw.t.Fatalf("run returned while waiting for the window to go quiet: %v", err)
		case <-time.After(quiet):
			return
		}
	}
}

// waitForRequest blocks until the fake has seen the named request. It polls,
// because that is what the fake reports by accessor rather than by channel;
// everything else in this file waits on a channel.
func (tw *testWindow) waitForRequest(name string) {
	tw.t.Helper()
	deadline := time.Now().Add(settle)
	for {
		for _, r := range tw.srv.Requests() {
			if r == name {
				return
			}
		}
		if time.Now().After(deadline) {
			tw.t.Fatalf("the compositor never saw %s", name)
		}
		time.Sleep(time.Millisecond)
	}
}

// noProtocolErrors fails the test if the fake caught the client breaking the
// protocol. A window layer that does is broken however green the rest is.
func (tw *testWindow) noProtocolErrors() {
	tw.t.Helper()
	noProtocolErrors(tw.t, tw.srv)
}

// noProtocolErrors fails the test if srv caught a protocol violation. The
// events the fake could not write are not violations: a client that gives up
// closes the socket at once, and what the fake records then is our own close
// on its way, not anything the client did wrong.
func noProtocolErrors(t *testing.T, srv *wltest.Server) {
	t.Helper()
	for _, e := range srv.Errors() {
		if strings.HasPrefix(e, "writing event") {
			continue
		}
		t.Errorf("the compositor saw a protocol error: %s", e)
	}
}

// Acceptance 1: the window is painted with the loader while init runs, and
// the first frame does not wait for it.
func TestTheLoaderIsPresentedWhileInitIsStillRunning(t *testing.T) {
	// init blocks until the test lets it go, which is the deterministic form
	// of the spec's one-second init: nothing below waits for it, so the only
	// thing the frames can be answering is the configure.
	release := make(chan struct{})
	defer close(release)

	tw := openWindow(t, wltest.Options{ManualConfigure: true}, Config{Title: "loader"},
		func(w *Window) (Content, error) {
			select {
			case <-release:
			case <-w.Context().Done():
			}
			return appContent(), nil
		})

	// The empty commit is what opens the window, so waiting for it puts the
	// client at a known point: everything but the configure has happened.
	tw.waitForRequest("wl_surface.commit")

	// The spec's 100 ms are measured from here. Commit.At is when the fake
	// processed the frame, so the difference covers the whole way from the
	// configure to the pixels.
	start := time.Now()
	tw.srv.Configure(0, 0)

	first := tw.nextCommit()
	if d := first.At.Sub(start); d > 100*time.Millisecond {
		t.Errorf("the first frame took %v from the configure, want under 100ms", d)
	}
	if first.Width != 640 || first.Height != 480 {
		t.Errorf("the first frame is %dx%d, want 640x480", first.Width, first.Height)
	}
	if got, app := first.Pixels[0], appWord(t); got == app {
		t.Errorf("the first frame is the application's (%#08x); want the loader's", got)
	}

	// The loader animates: over the next frames the pixels change, which a
	// spinner does and a still image does not.
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if !equalPixels(c.Pixels, first.Pixels) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("every frame while loading was identical: the loader is not animating")
		}
	}

	tw.noProtocolErrors()
}

// Acceptance 2: once init is done the application's own pixels are on screen,
// and a UI that does not animate stops committing.
func TestAfterInitTheApplicationPaintsAndAQuietWindowGoesSilent(t *testing.T) {
	tw := openWindow(t, wltest.Options{}, Config{Title: "app", AppID: "ggui.test.window"}, appInit)

	tw.waitForAppFrame()

	// And then the window goes quiet: the frames of the startup stop coming
	// and nothing takes their place, although the compositor keeps answering
	// every frame callback. An animating window would never reach this.
	tw.waitUntilQuiet(300 * time.Millisecond)

	if title := tw.srv.Title(); title != "app" {
		t.Errorf("the compositor has title %q, want %q", title, "app")
	}
	if appID := tw.srv.AppID(); appID != "ggui.test.window" {
		t.Errorf("the compositor has app id %q, want %q", appID, "ggui.test.window")
	}
	tw.noProtocolErrors()
}

// A compositor without one of the globals the window needs is a failure the
// application is told about, and nothing is opened on the way out.
func TestAMissingRequiredGlobalFailsBeforeAnySurfaceExists(t *testing.T) {
	for _, iface := range []string{"wl_compositor", "wl_shm", "xdg_wm_base"} {
		t.Run(iface, func(t *testing.T) {
			srv := wltest.NewServer(t, wltest.Options{Omit: []string{iface}})
			err := run(srv.Conn(), Config{}, func(*Window) (Content, error) {
				t.Error("init ran although the window could not be opened")
				return appContent(), nil
			})
			if err == nil {
				t.Fatal("run succeeded without " + iface)
			}
			if !strings.Contains(err.Error(), iface) {
				t.Errorf("run failed with %q, which does not name %s", err, iface)
			}
			// run gave up and closed the socket, so everything the client
			// ever sent is already on its way to the fake; the loop gives it
			// a bounded moment to record it, because "the fake saw no
			// surface" is only worth anything once the fake has read
			// everything there was.
			deadline := time.Now().Add(100 * time.Millisecond)
			for time.Now().Before(deadline) {
				for _, r := range srv.Requests() {
					if strings.HasSuffix(r, ".create_surface") {
						t.Fatalf("a surface was created before the missing global was noticed: %v", srv.Requests())
					}
				}
				time.Sleep(time.Millisecond)
			}
			noProtocolErrors(t, srv)
		})
	}
}

// wl_seat is optional: without one the window has no keyboard and no pointer,
// and is still a window.
func TestAWindowOpensWithAndWithoutASeat(t *testing.T) {
	for _, tc := range []struct {
		name string
		seat bool
	}{
		{"with a seat", true},
		{"without a seat", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tw := openWindow(t, wltest.Options{Seat: tc.seat}, Config{Title: "seat"}, appInit)
			tw.waitForAppFrame()
			tw.noProtocolErrors()
		})
	}
}

// The logical size is the config's, and a configure with a zero dimension
// means "you decide", which keeps the size the window already had.
func TestTheSizeComesFromTheConfigAndAZeroConfigureKeepsIt(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cfg           Config
		width, height int32
	}{
		{"a zero config is 640x480", Config{}, 640, 480},
		{"a size given in the config", Config{Width: 300, Height: 200}, 300, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tw := openWindow(t, wltest.Options{ManualConfigure: true}, tc.cfg, appInit)
			tw.waitForRequest("wl_surface.commit")
			tw.srv.Configure(0, 0)

			c := tw.nextCommit()
			if c.Width != tc.width || c.Height != tc.height {
				t.Errorf("the first frame is %dx%d, want %dx%d", c.Width, c.Height, tc.width, tc.height)
			}
			tw.noProtocolErrors()
		})
	}
}

// Do runs on the UI goroutine, Size reports the size the compositor
// configured, and Context ends when the window does.
func TestDoRunsOnTheUIGoroutineAndContextEndsWithTheWindow(t *testing.T) {
	// uiTouches is written with no synchronization by both Paint and the
	// closure given to Do. Both must run on the UI goroutine, and that is
	// exactly what -race checks here: a Do that ran anywhere else would be a
	// data race and fail the test.
	uiTouches := 0

	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{Width: 800, Height: 600}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		return Content{Paint: func(cv *canvas.Canvas, _ uint32) bool {
			uiTouches++
			cv.Clear(appColor)
			return false
		}}, nil
	})

	var w *Window
	select {
	case w = <-windows:
	case <-time.After(settle):
		t.Fatal("init was never called")
	}

	// The application's frame proves the configure has been applied, so Size
	// must report what the compositor asked for and not the config's default.
	tw.waitForAppFrame()

	done := make(chan [2]int, 1)
	w.Do(func() {
		uiTouches++
		width, height := w.Size()
		done <- [2]int{width, height}
	})
	select {
	case got := <-done:
		if got != [2]int{800, 600} {
			t.Errorf("Size reports %v, want [800 600]", got)
		}
	case <-time.After(settle):
		t.Fatal("the closure given to Do never ran")
	}

	ctx := w.Context()
	select {
	case <-ctx.Done():
		t.Fatal("the context was cancelled while the window was open")
	default:
	}

	tw.srv.CloseToplevel()
	select {
	case <-ctx.Done():
	case <-time.After(settle):
		t.Fatal("closing the window did not cancel the context")
	}
	if err := tw.wait(); err != nil {
		t.Errorf("run returned %v, want nil for an orderly close", err)
	}
	tw.noProtocolErrors()
}
