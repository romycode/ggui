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

// expectNoCommitUntil fails the test if a frame is committed before deadline.
// The deadline is a moment the test knows for itself — the fake's own vsync
// timer, which fires a known time after a commit — and not a guess at how
// long another goroutine needs.
func (tw *testWindow) expectNoCommitUntil(deadline time.Time) {
	tw.t.Helper()
	wait := time.Until(deadline)
	if wait <= 0 {
		return
	}
	select {
	case c := <-tw.srv.Commits():
		tw.t.Fatalf("a window with nothing to say committed another frame at %v", c.At)
	case err := <-tw.err:
		tw.err <- err
		tw.t.Fatalf("run returned while the window should have been idle: %v", err)
	case <-time.After(wait):
	}
}

// expectNoCommit fails the test if a frame has been committed and nobody took
// it. It is only worth anything after a round trip that proves everything the
// window could have painted has been painted.
func (tw *testWindow) expectNoCommit() {
	tw.t.Helper()
	select {
	case c := <-tw.srv.Commits():
		tw.t.Fatalf("a window with nothing to say committed another frame at %v", c.At)
	default:
	}
}

// pingPong sends xdg_wm_base.ping and waits for the pong. It is a round trip
// through the Wayland goroutine: the pong proves that everything the fake sent
// before the ping — a frame callback included — has been dispatched, because
// the client reads its socket in order.
func (tw *testWindow) pingPong(serial uint32) {
	tw.t.Helper()
	tw.srv.Ping(serial)
	tw.waitForRequest("xdg_wm_base.pong")
}

// onUI runs fn on the UI goroutine and waits for it. It is the other half of
// the round trip: after it, everything that was queued for the UI has run.
func (tw *testWindow) onUI(w *Window, fn func()) {
	tw.t.Helper()
	done := make(chan struct{})
	w.Do(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(settle):
		tw.t.Fatal("the UI goroutine did not run the closure")
	}
}

// receiveWindow takes the Window an init function handed the test.
func receiveWindow(t *testing.T, windows <-chan *Window) *Window {
	t.Helper()
	select {
	case w := <-windows:
		return w
	case <-time.After(settle):
		t.Fatal("init was never called")
		return nil
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
// and a UI that does not animate paints exactly once and then says nothing at
// all.
func TestAfterInitTheApplicationPaintsExactlyOneFrameAndGoesQuiet(t *testing.T) {
	// A short vsync so the one thing that could still produce a frame — the
	// callback answering the application's commit — is answered quickly.
	const vsync = 5 * time.Millisecond

	paints := 0 // UI goroutine only: Paint and the closures given to Do
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{Vsync: vsync}, Config{Title: "app", AppID: "ggui.test.window"},
		func(w *Window) (Content, error) {
			windows <- w
			return Content{Paint: func(cv *canvas.Canvas, _ uint32) bool {
				paints++
				cv.Clear(appColor)
				return false
			}}, nil
		})
	w := receiveWindow(t, windows)

	first := tw.waitForAppFrame()

	// The fake answers the callback for that commit one vsync later, and that
	// answer is the only thing left that could make the window paint again.
	tw.expectNoCommitUntil(first.At.Add(3 * vsync))

	// Now close the pipeline: the pong proves the Wayland goroutine has
	// dispatched the callback, and the closure given to Do proves the UI
	// goroutine has run everything that was queued behind it. Whatever the
	// window was going to paint, it has painted by now.
	tw.pingPong(1)
	painted := 0
	tw.onUI(w, func() { painted = paints })

	if painted != 1 {
		t.Errorf("the application painted %d frames, want exactly 1", painted)
	}
	tw.expectNoCommit()

	if title := tw.srv.Title(); title != "app" {
		t.Errorf("the compositor has title %q, want %q", title, "app")
	}
	if appID := tw.srv.AppID(); appID != "ggui.test.window" {
		t.Errorf("the compositor has app id %q, want %q", appID, "ggui.test.window")
	}
	tw.noProtocolErrors()
}

// A resize is painted at the new size. It is the path that pays for the frame
// clock keeping the wish to paint while it has no buffer: the configure
// invalidates, the pool is replaced and has nothing to paint into, and the
// frame comes out when a buffer of the new size arrives.
func TestAResizeIsPaintedAtTheNewSize(t *testing.T) {
	tw := openWindow(t, wltest.Options{}, Config{}, appInit)

	if c := tw.waitForAppFrame(); c.Width != 640 || c.Height != 480 {
		t.Fatalf("the first frame is %dx%d, want 640x480", c.Width, c.Height)
	}

	tw.srv.Configure(800, 600)

	want := appWord(t)
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if c.Width == 800 && c.Height == 600 {
			if c.Pixels[0] != want {
				t.Errorf("the frame after the resize is %#08x, want the application's %#08x", c.Pixels[0], want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame at the new size ever reached the compositor")
		}
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

	w := receiveWindow(t, windows)

	// The application's frame proves the configure has been applied, so Size
	// must report what the compositor asked for and not the config's default.
	tw.waitForAppFrame()

	var got [2]int
	tw.onUI(w, func() {
		uiTouches++
		got[0], got[1] = w.Size()
	})
	if got != [2]int{800, 600} {
		t.Errorf("Size reports %v, want [800 600]", got)
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
