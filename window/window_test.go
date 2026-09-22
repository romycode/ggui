package window

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/internal/wltest"
	"github.com/romycode/ggui/keyboard"
	"github.com/romycode/ggui/pointer"
	"github.com/romycode/ggui/wayland/wlcore"
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

// pingPong sends xdg_wm_base.ping and waits for its pong. It is a round trip
// through the Wayland goroutine: the pong proves that everything the fake sent
// before the ping — a frame callback, an input event — has been dispatched,
// because the client reads its socket in order. It counts pongs, so it can be
// used any number of times in one test.
func (tw *testWindow) pingPong(serial uint32) {
	tw.t.Helper()
	n := tw.countRequests("xdg_wm_base.pong")
	tw.srv.Ping(serial)
	tw.waitForRequestCount("xdg_wm_base.pong", n+1)
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

// countRequests is how many times the fake has seen the named request.
func (tw *testWindow) countRequests(name string) int {
	n := 0
	for _, r := range tw.srv.Requests() {
		if r == name {
			n++
		}
	}
	return n
}

// waitForRequest blocks until the fake has seen the named request.
func (tw *testWindow) waitForRequest(name string) {
	tw.t.Helper()
	tw.waitForRequestCount(name, 1)
}

// waitForRequestCount blocks until the fake has seen the named request at
// least n times. It polls, because that is what the fake reports by accessor
// rather than by channel; everything else in this file waits on a channel.
func (tw *testWindow) waitForRequestCount(name string, n int) {
	tw.t.Helper()
	deadline := time.Now().Add(settle)
	for tw.countRequests(name) < n {
		if time.Now().After(deadline) {
			tw.t.Fatalf("the compositor saw %s %d times, want %d", name, tw.countRequests(name), n)
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

	// The criterion is that the first frame does not depend on init, and that is
	// what this test proves without a clock: init is parked on a channel until
	// the test ends, and the loader's frame still arrives. How long it takes is
	// reported, not asserted. The spec's 100 ms is what a healthy machine does,
	// and a wall-clock bound fails on a loaded one for reasons that have nothing
	// to do with the window; only a frame that takes longer than the generous
	// bound every wait in this package has is an error. Commit.At is when the
	// fake processed the frame, so the difference covers the whole way from the
	// configure to the pixels.
	start := time.Now()
	tw.srv.Configure(0, 0)

	first := tw.nextCommit()
	d := first.At.Sub(start)
	t.Logf("the first frame took %v from the configure (the spec's target is 100ms)", d)
	if d > settle {
		t.Errorf("the first frame took %v from the configure, want it well under %v", d, settle)
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

// Without wp_viewporter and wp_fractional_scale_manager_v1, the window still
// gets HiDPI through the core protocol: wl_surface.preferred_buffer_scale,
// acted on with wl_surface.set_buffer_scale. No viewport is ever created.
func TestIntegerScaleFallbackRepaintsAtTheNewPhysicalSize(t *testing.T) {
	tw := openWindow(t, wltest.Options{}, Config{}, appInit)
	tw.waitForAppFrame()

	tw.srv.SendPreferredBufferScale(2)

	want := appWord(t)
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if c.Width == 1280 && c.Height == 960 {
			if c.Pixels[0] != want {
				t.Errorf("the frame after the rescale is %#08x, want the application's %#08x", c.Pixels[0], want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame at the new physical size ever reached the compositor")
		}
	}
	if got := tw.srv.BufferScale(); got != 2 {
		t.Errorf("wl_surface.set_buffer_scale = %d, want 2", got)
	}
	if n := tw.countRequests("wp_viewporter.get_viewport"); n != 0 {
		t.Errorf("a viewport was created although wp_viewporter was never advertised")
	}
	tw.noProtocolErrors()
}

// With both extensions, the window uses wp_viewport.set_destination instead:
// the buffer is rendered at the fractional physical size and the surface
// stays at its logical size. wl_surface.set_buffer_scale is never touched.
func TestFractionalScaleUsesTheViewportDestination(t *testing.T) {
	tw := openWindow(t, wltest.Options{Viewporter: true, FractionalScale: true}, Config{}, appInit)
	tw.waitForAppFrame()

	tw.srv.SendPreferredScale(180) // 1.5x

	want := appWord(t)
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if c.Width == 960 && c.Height == 720 { // ceil(640*1.5), ceil(480*1.5)
			if c.Pixels[0] != want {
				t.Errorf("the frame after the rescale is %#08x, want the application's %#08x", c.Pixels[0], want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame at the new physical size ever reached the compositor")
		}
	}
	if w, h, ok := tw.srv.ViewportDestination(); !ok || w != 640 || h != 480 {
		t.Errorf("viewport destination = (%d, %d, ok=%v), want (640, 480, true)", w, h, ok)
	}
	if got := tw.srv.BufferScale(); got != 1 {
		t.Errorf("wl_surface.set_buffer_scale = %d, want the untouched default 1", got)
	}
	tw.noProtocolErrors()
}

// Fractional scale needs both extensions together: with only wp_viewporter
// advertised the window has nothing to learn a fractional scale from, so it
// falls back to the same integer path as if neither were there.
func TestOnlyOneOfTheTwoExtensionsFallsBackToInteger(t *testing.T) {
	tw := openWindow(t, wltest.Options{Viewporter: true}, Config{}, appInit)
	tw.waitForAppFrame()

	tw.srv.SendPreferredBufferScale(2)

	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if c.Width == 1280 && c.Height == 960 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame at the new physical size ever reached the compositor")
		}
	}
	if got := tw.srv.BufferScale(); got != 2 {
		t.Errorf("wl_surface.set_buffer_scale = %d, want 2", got)
	}
	if n := tw.countRequests("wp_viewporter.get_viewport"); n != 0 {
		t.Errorf("a viewport was created with only one of the two extensions present")
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

// finish closes the window the way a compositor does and checks what the fake
// saw only after run has returned, so a violation during shutdown is not
// missed. It also insists on an orderly close: run returns nil.
func (tw *testWindow) finish() {
	tw.t.Helper()
	tw.srv.CloseToplevel()
	if err := tw.wait(); err != nil {
		tw.t.Errorf("run returned %v, want nil for an orderly close", err)
	}
	tw.noProtocolErrors()
}

// barrier returns once the UI goroutine has handled everything the compositor
// sent before the call, whatever phase the window is in.
//
// Two round trips make it, and neither is enough alone:
//
//   - The ping/pong proves the Wayland goroutine has dispatched every event the
//     fake wrote before the ping, and so has pushed it to the UI's inbox.
//   - A UI pass drains the inbox and only then takes the closures given to Do,
//     so a closure can run in a pass whose drain happened just before the last
//     Push. One Do therefore does not prove the event was handled. A second Do
//     is queued after the first has run, which makes it a later pass, and that
//     pass drained the inbox after the Push and handled what it found before
//     running the closure.
//
// The point is the loading phase: an event the UI has not yet handled when
// init returns would be handled after the content is installed, so a test that
// says "this was sent during loading" has to know it was handled during
// loading.
func (tw *testWindow) barrier(w *Window) {
	tw.t.Helper()
	tw.pingPong(1)
	tw.onUI(w, func() {})
	tw.onUI(w, func() {})
}

// recorder is the application of the input tests: it writes down, in order,
// everything the window layer calls, as one line each. Only the UI goroutine
// touches it, so it needs no lock; a test reads it through [testWindow.log].
type recorder struct {
	entries []string
}

func (r *recorder) add(format string, args ...any) {
	r.entries = append(r.entries, fmt.Sprintf(format, args...))
}

// content is a Content that fills the window with appColor and records every
// callback it receives.
func (r *recorder) content() Content {
	return Content{
		Paint: func(cv *canvas.Canvas, _ uint32) bool {
			r.add("paint %dx%d", cv.Width(), cv.Height())
			cv.Clear(appColor)
			return false
		},
		OnKey: func(ev keyboard.Event) { r.add("key %d %v", ev.Evdev, ev.State) },
		OnPointer: func(ev pointer.Event) {
			r.add("pointer %v %.0f,%.0f", ev.Kind, ev.X, ev.Y)
		},
		OnKeyboardFocus: func(focused bool) { r.add("keyboard-focus %v", focused) },
		OnPointerFocus:  func(focused bool) { r.add("pointer-focus %v", focused) },
		OnResize:        func(width, height int) { r.add("resize %dx%d", width, height) },
	}
}

// log returns what r has recorded so far, read on the UI goroutine.
func (tw *testWindow) log(w *Window, r *recorder) []string {
	tw.t.Helper()
	var out []string
	tw.onUI(w, func() { out = slices.Clone(r.entries) })
	return out
}

// withoutPaints is entries with the paint lines removed. Frames come and go
// on the frame clock's schedule, so a test that is about the order of the
// callbacks among themselves leaves them out and asserts the paints separately.
func withoutPaints(entries []string) []string {
	return slices.DeleteFunc(slices.Clone(entries), func(e string) bool {
		return strings.HasPrefix(e, "paint ")
	})
}

// onlyPrefix is the entries that start with prefix.
func onlyPrefix(entries []string, prefix string) []string {
	return slices.DeleteFunc(slices.Clone(entries), func(e string) bool {
		return !strings.HasPrefix(e, prefix)
	})
}

// checkResizeBeforePaint fails the test unless every paint in entries was
// preceded by an OnResize for the size it painted. It is the order the spec
// promises, read off the log and not off any timing.
func checkResizeBeforePaint(t *testing.T, entries []string) {
	t.Helper()
	last := ""
	for i, e := range entries {
		switch {
		case strings.HasPrefix(e, "resize "):
			last = strings.TrimPrefix(e, "resize ")
		case strings.HasPrefix(e, "paint "):
			got := strings.TrimPrefix(e, "paint ")
			if last != got {
				t.Fatalf("entry %d is %q, but the last OnResize before it was for %q\nlog: %q", i, e, last, entries)
			}
		}
	}
}

// waitForCommitAtSize drains frames until one has the given pixel size, and
// returns the last frame taken, which is that one.
func (tw *testWindow) waitForCommitAtSize(width, height int32) wltest.Commit {
	tw.t.Helper()
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if c.Width == width && c.Height == height {
			return c
		}
		if time.Now().After(deadline) {
			tw.t.Fatalf("no frame at %dx%d ever reached the compositor", width, height)
		}
	}
}

// waitForAppFrameAtSize drains frames until one carries the application's own
// pixels at the given size. waitForCommitAtSize alone is not that: while the
// application is still being installed a frame of the right size may be the
// loader's, and a window that is resized again before the application paints
// never paints at the size in between.
func (tw *testWindow) waitForAppFrameAtSize(width, height int32) wltest.Commit {
	tw.t.Helper()
	want := appWord(tw.t)
	deadline := time.Now().Add(settle)
	for {
		c := tw.waitForCommitAtSize(width, height)
		if c.Pixels[0] == want {
			return c
		}
		if time.Now().After(deadline) {
			tw.t.Fatalf("the application never painted a frame at %dx%d", width, height)
		}
	}
}

// seatWindow opens a window with a seat whose init blocks until the returned
// release is called, and waits until the fake has seen both input devices
// requested and the surface created, which is when it can inject events
// without erroring. The window is on screen, loading.
func seatWindow(t *testing.T, opts wltest.Options, r *recorder) (tw *testWindow, w *Window, release func()) {
	t.Helper()
	opts.Seat = true
	gate := make(chan struct{})
	windows := make(chan *Window, 1)
	tw = openWindow(t, opts, Config{}, func(w *Window) (Content, error) {
		windows <- w
		select {
		case <-gate:
		case <-w.Context().Done():
		}
		return r.content(), nil
	})
	var once bool
	release = func() {
		if !once {
			once = true
			close(gate)
		}
	}
	t.Cleanup(release)
	w = receiveWindow(t, windows)
	tw.waitForRequest("wl_seat.get_keyboard")
	tw.waitForRequest("wl_seat.get_pointer")
	// The seat is bound before the surface exists, so the two requests above
	// say nothing about it: the fake refuses to focus a surface it has not
	// seen created. The initial commit is the first request after the
	// create_surface it follows, and the fake handles them in order.
	tw.waitForRequest("wl_surface.commit")
	return tw, w, release
}

// Acceptance 3: keys and pointer motion reach the application in the order
// they were sent, once init is done, and what was sent while the window was
// loading never does.
func TestInputAfterInitIsDeliveredInOrderAndInputDuringLoadingIsNot(t *testing.T) {
	rec := &recorder{}
	tw, w, release := seatWindow(t, wltest.Options{}, rec)

	// Sent while init is still running. KEY_B and the positions at 11,12 and
	// 21,22 are recognisable, so a leak shows up as an entry nobody sent
	// afterwards.
	const keyB, keyA = 48, 30
	tw.srv.Key(keyB, true)
	tw.srv.PointerMotion(11, 12)
	tw.srv.Key(keyB, false)
	tw.srv.PointerMotion(21, 22)

	// Every one of those has been handled by the UI, in the loading phase,
	// before init is allowed to finish.
	tw.barrier(w)
	release()
	tw.waitForAppFrame() // the application is installed and has painted

	tw.srv.Key(keyA, true)
	tw.srv.PointerMotion(100, 50)
	tw.srv.Key(keyA, false)
	tw.srv.PointerMotion(120, 60)
	tw.barrier(w)

	got := withoutPaints(tw.log(w, rec))
	want := []string{
		// What the layer replays on install: the size, then the focus the
		// input above gained the devices while loading.
		"resize 640x480", "keyboard-focus true", "pointer-focus true",
		// Then exactly the second batch, in order.
		"key 30 pressed", "pointer position 100,50", "key 30 released", "pointer position 120,60",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the application saw\n  %q\nwant\n  %q", got, want)
	}
	tw.finish()
}

// A focus gained while the application was loading is not lost: the content
// that arrives afterwards is told at once, after the size and before anything
// is painted. Losing it later is reported as any other change, and the device
// that never had focus is never mentioned.
func TestFocusGainedWhileLoadingIsDeliveredWhenTheContentIsInstalled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gain, lose func(*wltest.Server)
		event      string
		// enterEvents is what a gain brings with it once the application is
		// there to receive it. The pointer's enter carries its position, which
		// is delivered as the first motion; the same enter during loading was
		// dropped with everything else the pointer does.
		enterEvents []string
	}{
		{
			name:  "keyboard",
			gain:  func(s *wltest.Server) { s.FocusKeyboard(true) },
			lose:  func(s *wltest.Server) { s.FocusKeyboard(false) },
			event: "keyboard-focus",
		},
		{
			name:  "pointer",
			gain:  func(s *wltest.Server) { s.FocusPointer(true) },
			lose:  func(s *wltest.Server) { s.FocusPointer(false) },
			event: "pointer-focus",

			enterEvents: []string{"pointer position 0,0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			tw, w, release := seatWindow(t, wltest.Options{ManualConfigure: true}, rec)

			// The compositor decides the size while the application loads,
			// twice, and the second is the one that counts.
			tw.waitForRequest("wl_surface.commit")
			tw.srv.Configure(500, 400)
			tw.srv.Configure(800, 600)
			tc.gain(tw.srv)
			tw.barrier(w)

			release()
			first := tw.waitForAppFrame()
			if first.Width != 800 || first.Height != 600 {
				t.Errorf("the first application frame is %dx%d, want 800x600", first.Width, first.Height)
			}
			tw.barrier(w)

			raw := tw.log(w, rec)
			want := []string{"resize 800x600", tc.event + " true"}
			// Before the first paint, and in this order: nothing else may have
			// been called by then.
			if len(raw) < len(want) || !slices.Equal(raw[:len(want)], want) {
				t.Errorf("the application was called with\n  %q\nwant it to start with\n  %q", raw, want)
			}
			checkResizeBeforePaint(t, raw)

			tc.lose(tw.srv)
			tw.barrier(w)
			tc.gain(tw.srv)
			tw.barrier(w)
			got := withoutPaints(tw.log(w, rec))
			want = append(want, tc.event+" false", tc.event+" true")
			want = append(want, tc.enterEvents...)
			if !slices.Equal(got, want) {
				t.Errorf("the application saw\n  %q\nwant\n  %q", got, want)
			}
			tw.finish()
		})
	}
}

// A focus that came and went while loading has nothing to report: the content
// is told about the size and nothing else, and it is told about the next
// change as any other.
func TestFocusLostWhileLoadingDeliversNothingWhenTheContentIsInstalled(t *testing.T) {
	rec := &recorder{}
	tw, w, release := seatWindow(t, wltest.Options{}, rec)

	tw.srv.FocusKeyboard(true)
	tw.srv.FocusKeyboard(false)
	tw.srv.FocusPointer(true)
	tw.srv.FocusPointer(false)
	tw.barrier(w)

	release()
	tw.waitForAppFrame()
	tw.barrier(w)

	if got, want := withoutPaints(tw.log(w, rec)), []string{"resize 640x480"}; !slices.Equal(got, want) {
		t.Errorf("the application saw\n  %q\nwant only\n  %q", got, want)
	}

	// The mechanism is alive: a later gain is delivered.
	tw.srv.FocusKeyboard(true)
	tw.srv.FocusPointer(true)
	tw.barrier(w)
	raw := tw.log(w, rec)
	got := append(onlyPrefix(raw, "keyboard-focus"), onlyPrefix(raw, "pointer-focus")...)
	if want := []string{"keyboard-focus true", "pointer-focus true"}; !slices.Equal(got, want) {
		t.Errorf("after install the focus events were %q, want %q", got, want)
	}
	tw.finish()
}

// Acceptance 4: a compositor that drags the window edge sends a configure for
// every step. The last frame has the final size, the application heard of
// every size once and in order, and the buffers of the sizes left behind are
// destroyed instead of piling up in the compositor.
func TestManyConfiguresEndAtTheFinalSizeAndTheOldBuffersAreDestroyed(t *testing.T) {
	rec := &recorder{}
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		return rec.content(), nil
	})
	w := receiveWindow(t, windows)
	tw.waitForAppFrame()

	type size struct{ w, h int32 }
	var sizes []size
	for i := int32(0); i < 30; i++ {
		sizes = append(sizes, size{400 + 7*i, 300 + 5*i}) // all different, none 640x480
	}
	for _, s := range sizes {
		tw.srv.Configure(s.w, s.h)
	}
	final := sizes[len(sizes)-1]

	last := tw.waitForCommitAtSize(final.w, final.h)
	if last.Pixels[0] != appWord(t) {
		t.Errorf("the frame at the final size is %#08x, want the application's %#08x", last.Pixels[0], appWord(t))
	}
	tw.barrier(w)

	// Whatever else was committed after that is still the final size: the
	// window never goes back to a size it has left.
	for done := false; !done; {
		select {
		case c := <-tw.srv.Commits():
			if c.Width != final.w || c.Height != final.h {
				t.Errorf("a frame at %dx%d was committed after the final size", c.Width, c.Height)
			}
		default:
			done = true
		}
	}

	raw := tw.log(w, rec)
	want := []string{"resize 640x480"}
	for _, s := range sizes {
		want = append(want, fmt.Sprintf("resize %dx%d", s.w, s.h))
	}
	if got := onlyPrefix(raw, "resize "); !slices.Equal(got, want) {
		t.Errorf("OnResize saw\n  %q\nwant\n  %q", got, want)
	}
	checkResizeBeforePaint(t, raw)

	var gotSize [2]int
	tw.onUI(w, func() { gotSize[0], gotSize[1] = w.Size() })
	if gotSize != [2]int{int(final.w), int(final.h)} {
		t.Errorf("Size reports %v, want the last OnResize's %dx%d", gotSize, final.w, final.h)
	}

	// The buffers are destroyed by requests the Wayland goroutine sends after
	// the UI decides, so the fake learns of them a moment later: poll, bounded,
	// for the count to come down to the pool.
	deadline := time.Now().Add(settle)
	for tw.srv.LiveBuffers() != frameCount {
		if time.Now().After(deadline) {
			t.Fatalf("the compositor still holds %d buffers, want the pool's %d", tw.srv.LiveBuffers(), frameCount)
		}
		time.Sleep(time.Millisecond)
	}
	for _, b := range tw.srv.Buffers() {
		if b.Live && (b.Width != final.w || b.Height != final.h) {
			t.Errorf("a live buffer of %dx%d survives the resize to %dx%d", b.Width, b.Height, final.w, final.h)
		}
	}
	tw.finish()
}

// A zero dimension in a configure means "you decide", so it keeps the size the
// window has in that dimension, and a configure that leaves the size as it was
// is not a resize.
func TestAConfigureWithAZeroDimensionKeepsTheCurrentSize(t *testing.T) {
	rec := &recorder{}
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		return rec.content(), nil
	})
	w := receiveWindow(t, windows)
	if c := tw.waitForAppFrame(); c.Width != 640 || c.Height != 480 {
		t.Fatalf("the first frame is %dx%d, want 640x480", c.Width, c.Height)
	}

	// None of these changes the size the window has.
	tw.srv.Configure(0, 0)
	tw.srv.Configure(0, 480)
	tw.srv.Configure(640, 0)
	tw.srv.Configure(640, 480)
	// The next one does, and is what tells the test the others were handled.
	tw.srv.Configure(700, 500)
	tw.waitForCommitAtSize(700, 500)
	tw.barrier(w)

	// A zero dimension is kept alone: the other one still counts.
	tw.srv.Configure(0, 300)
	tw.waitForCommitAtSize(700, 300)
	tw.srv.Configure(350, 0)
	tw.waitForCommitAtSize(350, 300)
	tw.barrier(w)

	raw := tw.log(w, rec)
	want := []string{"resize 640x480", "resize 700x500", "resize 700x300", "resize 350x300"}
	if got := onlyPrefix(raw, "resize "); !slices.Equal(got, want) {
		t.Errorf("OnResize saw\n  %q\nwant\n  %q", got, want)
	}
	checkResizeBeforePaint(t, raw)

	// No frame was ever at a size nobody configured.
	for done := false; !done; {
		select {
		case c := <-tw.srv.Commits():
			if s := [2]int32{c.Width, c.Height}; s != [2]int32{350, 300} {
				t.Errorf("a frame at %v was committed after the last configure", s)
			}
		default:
			done = true
		}
	}
	tw.finish()
}

// OnResize is called before the first Paint at each size, the first one and
// every one after it, and never for a size that was not configured.
func TestOnResizeIsCalledBeforeThePaintAtEachSize(t *testing.T) {
	rec := &recorder{}
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{Width: 500, Height: 400}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		return rec.content(), nil
	})
	w := receiveWindow(t, windows)
	tw.waitForAppFrameAtSize(500, 400)
	tw.srv.Configure(800, 600)
	tw.waitForAppFrameAtSize(800, 600)
	tw.srv.Configure(320, 240)
	tw.waitForAppFrameAtSize(320, 240)
	tw.barrier(w)

	raw := tw.log(w, rec)
	checkResizeBeforePaint(t, raw)
	for _, s := range []string{"500x400", "800x600", "320x240"} {
		if !slices.Contains(raw, "paint "+s) {
			t.Errorf("the application never painted at %s: %q", s, raw)
		}
	}
	// The window starts at the config's 640x480, which the application is told
	// about when it is installed, and 500x400 is the compositor's first word.
	// Neither the size in between nor any other is invented.
	for _, e := range onlyPrefix(raw, "resize ") {
		switch e {
		case "resize 640x480", "resize 500x400", "resize 800x600", "resize 320x240":
		default:
			t.Errorf("OnResize was called with %q, a size nobody configured", e)
		}
	}
	tw.finish()
}

// A Content with only Paint is a valid application: the callbacks it leaves
// nil are ignored, whatever the compositor sends.
func TestNilCallbacksAreIgnored(t *testing.T) {
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{Seat: true}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		return appContent(), nil // Paint and nothing else
	})
	w := receiveWindow(t, windows)
	tw.waitForAppFrame()
	tw.waitForRequest("wl_seat.get_keyboard")
	tw.waitForRequest("wl_seat.get_pointer")

	tw.srv.FocusKeyboard(true)
	tw.srv.FocusPointer(true)
	tw.srv.Key(30, true)
	tw.srv.PointerMotion(5, 5)
	tw.srv.PointerButton(0x110, true)
	tw.srv.Configure(700, 500)
	tw.srv.FocusKeyboard(false)
	tw.srv.FocusPointer(false)
	tw.barrier(w)
	tw.waitForCommitAtSize(700, 500)

	// The UI goroutine is alive after all of it: a panic on a nil call would
	// have taken the test binary down before this closure ran.
	tw.onUI(w, func() {})
	tw.finish()
}

// The focus callbacks report changes, not events: a compositor that says again
// what the window already knows has nothing new for the application. The fake
// never does, so this drives the handler directly. It is on the UI goroutine's
// side alone and touches no connection, so a window with none is enough.
func TestARepeatedFocusStateIsNotReportedAgain(t *testing.T) {
	rec := &recorder{}
	w := newWindow(nil, Config{})
	w.content = rec.content()

	surface := new(wlcore.Surface) // identity only, as in production
	for _, tc := range []struct {
		kind    eventloop.EventKind
		surface *wlcore.Surface
	}{
		{eventloop.EvKeyboardFocus, surface},
		{eventloop.EvKeyboardFocus, surface},
		{eventloop.EvKeyboardFocus, nil},
		{eventloop.EvKeyboardFocus, nil},
		{eventloop.EvPointerFocus, surface},
		{eventloop.EvPointerFocus, surface},
		{eventloop.EvPointerFocus, nil},
		{eventloop.EvPointerFocus, nil},
	} {
		w.onEvent(eventloop.Event{Kind: tc.kind, Surface: tc.surface})
	}

	want := []string{"keyboard-focus true", "keyboard-focus false", "pointer-focus true", "pointer-focus false"}
	if !slices.Equal(rec.entries, want) {
		t.Errorf("the application saw\n  %q\nwant\n  %q", rec.entries, want)
	}
}

// A missing init is the caller's bug, and is reported before anything is asked
// of the compositor: no connection is made and no round trip spent on a window
// that could not open anyway. The environment is emptied so that a Run that did
// connect first would fail with a connect error and not with this one, and so
// that nothing here can reach a real session.
func TestRunWithoutAnInitFunctionFailsBeforeConnecting(t *testing.T) {
	t.Setenv("WAYLAND_SOCKET", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	err := Run(Config{}, nil)
	if !errors.Is(err, errNoInit) {
		t.Errorf("Run(cfg, nil) returned %v, want the missing-init error and not a connection one", err)
	}
}
