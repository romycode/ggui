package window

import (
	"bytes"
	"errors"
	"log"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/internal/wltest"
	"github.com/romycode/ggui/wayland/wlcore"
)

// openWindowHooked is openWindow with a hook on the window, run on the Wayland
// goroutine before the loop and the other goroutines start.
func openWindowHooked(t *testing.T, opts wltest.Options, cfg Config, init func(*Window) (Content, error), hook func(*Window)) *testWindow {
	t.Helper()
	tw := &testWindow{t: t, srv: wltest.NewServer(t, opts), err: make(chan error, 1)}
	go func() { tw.err <- runWith(tw.srv.Conn(), cfg, init, hook) }()
	t.Cleanup(tw.stop)
	return tw
}

// openWindowWith is openWindowHooked with a hook on the pool, which is how a
// test makes the buffers fail: nothing the fake compositor can do makes a
// request that is well formed fail.
func openWindowWith(t *testing.T, opts wltest.Options, cfg Config, init func(*Window) (Content, error), tweak func(*pool)) *testWindow {
	t.Helper()
	return openWindowHooked(t, opts, cfg, init, func(w *Window) { tweak(w.pool) })
}

// closeAndWait closes the window the way a compositor does, waits for run and
// returns what it returned. What the fake saw is checked afterwards, once
// nothing can still be on its way.
func (tw *testWindow) closeAndWait() error {
	tw.t.Helper()
	tw.srv.CloseToplevel()
	err := tw.wait()
	tw.noProtocolErrors()
	return err
}

// windowFrom is an init that hands its Window to the test and then returns
// whatever the test wants: content is what init returns when it is let go.
func windowFrom(windows chan<- *Window, gate <-chan struct{}, content Content, err error) func(*Window) (Content, error) {
	return func(w *Window) (Content, error) {
		windows <- w
		if gate != nil {
			select {
			case <-gate:
			case <-w.Context().Done():
			}
		}
		return content, err
	}
}

// assertNothingRunning is the leak check: after run has returned, no goroutine
// this package started is alive. The UI goroutine is counted, and init must
// have finished; a test whose init is parked on purpose does not use it for
// init and checks that goroutine on its own.
func assertNothingRunning(t *testing.T, w *Window) {
	t.Helper()
	if n := w.owned.Load(); n != 0 {
		t.Errorf("%d goroutines the window started are still running after Run returned", n)
	}
	select {
	case <-w.initDone:
	case <-time.After(settle):
		t.Error("the init goroutine is still running")
	}
}

// waitInitDone blocks until the init goroutine has ended. Whatever it hands
// the UI is queued by then.
func waitInitDone(t *testing.T, w *Window) {
	t.Helper()
	select {
	case <-w.initDone:
	case <-time.After(settle):
		t.Fatal("init did not finish")
	}
}

// assertContextDone fails the test unless the window's context is cancelled.
func assertContextDone(t *testing.T, w *Window) {
	t.Helper()
	select {
	case <-w.Context().Done():
	default:
		t.Error("the window's context is not cancelled after Run returned")
	}
}

// failedFrame is what PaintFailed draws at width x height, computed the way
// the window does it, so a commit can be compared with it exactly.
func failedFrame(t *testing.T, width, height int) []uint32 {
	t.Helper()
	px := make([]uint32, width*height)
	cv, err := canvas.New(canvas.Buffer{Pixels: px, Width: width, Height: height, Stride: width}, width, height, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	eventloop.PaintFailed(cv, float32(width), float32(height))
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas: %v", err)
	}
	return px
}

// waitForFailedFrame drains frames until one is exactly the failure screen at
// width x height, and returns it.
func (tw *testWindow) waitForFailedFrame(width, height int) (failed wltest.Commit) {
	tw.t.Helper()
	want := failedFrame(tw.t, width, height)
	deadline := time.Now().Add(settle)
	for {
		c := tw.nextCommit()
		if int(c.Width) == width && int(c.Height) == height && equalPixels(c.Pixels, want) {
			return c
		}
		if time.Now().After(deadline) {
			tw.t.Fatalf("the failure screen never reached the compositor at %dx%d", width, height)
		}
	}
}

// goid is the number of the calling goroutine. Go has no API for it, and a
// test that wants to say "this ran on that goroutine" cannot do without.
func goid() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n])) // "goroutine 123 [running]:"
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		panic("cannot read the goroutine id from " + string(buf[:n]))
	}
	return id
}

// lockedBuffer is a bytes.Buffer that other goroutines may write while the
// test reads: the fake compositor and windows of earlier tests log too.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLog sends the standard logger to a buffer for the rest of the test
// and puts it back afterwards.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return buf
}

// hangUp ends the connection the way a compositor that dies does: the socket
// is shut down under the client, which is not a close the client asked for.
func hangUp(t *testing.T, conn *wlcore.Conn) {
	t.Helper()
	rc, err := conn.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var shutErr error
	if err := rc.Control(func(fd uintptr) { shutErr = unix.Shutdown(int(fd), unix.SHUT_RDWR) }); err != nil {
		t.Fatalf("Control: %v", err)
	}
	if shutErr != nil {
		t.Fatalf("shutdown: %v", shutErr)
	}
}

// failCreates returns a pool hook that makes the buffer creation fail when
// fail(n) says so, n counting the calls from 1. The failure is a real one:
// the descriptor handed to wl_shm.create_pool is invalid, so the request is
// refused by the socket, while the connection stays up.
func failCreates(fail func(n int) bool) func(*pool) {
	return func(p *pool) {
		real := p.createBuffer
		n := 0 // the Wayland goroutine only, where create runs
		p.create = func(f *frame, fd, size int, width, height int32) {
			n++
			if fail(n) {
				unix.Close(fd)
				fd = -1
			}
			real(f, fd, size, width, height)
		}
	}
}

// Acceptance 5, the compositor's half: xdg_toplevel.close ends Run with nil,
// cancels the context, and leaves nothing of the window's running.
func TestACloseFromTheCompositorEndsRunOrderlyAndLeavesNothingRunning(t *testing.T) {
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, appContent(), nil))
	w := receiveWindow(t, windows)
	tw.waitForAppFrame()

	select {
	case <-w.Context().Done():
		t.Fatal("the context was cancelled while the window was open")
	default:
	}

	if err := tw.closeAndWait(); err != nil {
		t.Errorf("Run returned %v, want nil for an orderly close", err)
	}
	assertContextDone(t, w)
	assertNothingRunning(t, w)
}

// Acceptance 5, the application's half: Close from another goroutine ends Run
// with nil exactly as the compositor's close does, and it may be called again,
// and after the window is gone, without panicking or blocking.
func TestCloseFromAnotherGoroutineEndsRunOrderlyAndMayBeRepeated(t *testing.T) {
	const vsync = 5 * time.Millisecond
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{Vsync: vsync}, Config{}, windowFrom(windows, nil, appContent(), nil))
	w := receiveWindow(t, windows)
	first := tw.waitForAppFrame()

	// Let the window go completely quiet first. An idle Wayland goroutine is
	// parked in poll(2), and closing the socket does not wake it: only
	// Loop.Close does. A window that is still exchanging frame callbacks would
	// notice a bare close on the next one and hide the difference.
	tw.expectNoCommitUntil(first.At.Add(3 * vsync))
	tw.pingPong(1)

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		w.Close()
	}()
	select {
	case <-closed:
	case <-time.After(settle):
		t.Fatal("Close did not return")
	}

	if err := tw.wait(); err != nil {
		t.Errorf("Run returned %v, want nil for an orderly close", err)
	}
	tw.noProtocolErrors()
	assertContextDone(t, w)
	assertNothingRunning(t, w)

	// Twice, after Run returned, and the other methods too: the window is gone
	// and all of them are safe, from this goroutine and from others.
	w.Close()
	w.Close()
	w.SetTitle("too late")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Close()
			w.SetTitle("too late")
			w.Do(func() { t.Error("a closure given to Do after the window closed was run") })
		}()
	}
	wg.Wait()
}

// Close is called from wherever the application decides to quit: from a
// callback on the UI goroutine, and from init itself while the window is
// still loading. Neither may deadlock, and both end Run with nil.
func TestCloseFromTheUIGoroutineAndFromInit(t *testing.T) {
	t.Run("the UI goroutine", func(t *testing.T) {
		windows := make(chan *Window, 1)
		tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, appContent(), nil))
		w := receiveWindow(t, windows)
		tw.waitForAppFrame()

		w.Do(func() { w.Close() })
		if err := tw.wait(); err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
		tw.noProtocolErrors()
		assertContextDone(t, w)
		assertNothingRunning(t, w)
	})
	t.Run("init", func(t *testing.T) {
		windows := make(chan *Window, 1)
		tw := openWindow(t, wltest.Options{}, Config{}, func(w *Window) (Content, error) {
			windows <- w
			w.Close()
			return appContent(), nil
		})
		w := receiveWindow(t, windows)
		if err := tw.wait(); err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
		tw.noProtocolErrors()
		assertContextDone(t, w)
		assertNothingRunning(t, w)
	})
}

// Close may be called before the event loop has started: init starts beside it
// and can be quick enough to quit at once. That must still be an orderly close,
// and not Loop.Run refusing to run. The hook is the one moment the test can make
// that certain: it runs before the loop and every other goroutine.
func TestCloseBeforeTheLoopHasStartedIsStillAnOrderlyClose(t *testing.T) {
	windows := make(chan *Window, 1)
	// The hook has a window with a loop already made and its post in place, but
	// nothing running yet.
	tw := openWindowHooked(t, wltest.Options{}, Config{}, windowFrom(windows, nil, appContent(), nil),
		func(w *Window) { w.Close() })
	w := receiveWindow(t, windows)

	if err := tw.wait(); err != nil {
		t.Errorf("Run returned %v, want nil for an orderly close", err)
	}
	tw.noProtocolErrors()
	assertContextDone(t, w)
	assertNothingRunning(t, w)
}

// Acceptance 6: an init that fails leaves the window open showing the failure
// screen, and Run returns that error, unchanged, once the window is closed.
func TestAFailedInitLeavesTheWindowOpenOnTheFailureScreenAndRunReturnsTheError(t *testing.T) {
	errInit := errors.New("the font is missing")
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, Content{}, errInit))
	w := receiveWindow(t, windows)

	failed := tw.waitForFailedFrame(640, 480)
	if want := appWord(t); failed.Pixels[0] == want {
		t.Error("the failure screen is the application's")
	}

	// Still open: nothing ended Run, and the context is alive.
	select {
	case err := <-tw.err:
		tw.err <- err
		t.Fatalf("Run returned %v while the failure screen should be up", err)
	default:
	}
	select {
	case <-w.Context().Done():
		t.Fatal("the context was cancelled while the failure screen was up")
	default:
	}

	err := tw.closeAndWait()
	if !errors.Is(err, errInit) {
		t.Errorf("Run returned %v, want init's error", err)
	}
	assertContextDone(t, w)
	assertNothingRunning(t, w)
}

// The failure screen is not the loader's: the first frame of the window, drawn
// while init runs, differs from it, and it does not move.
func TestTheFailureScreenIsNotTheLoaderAndStopsAnimating(t *testing.T) {
	errInit := errors.New("no configuration")
	gate := make(chan struct{})
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{Vsync: 5 * time.Millisecond}, Config{},
		windowFrom(windows, gate, Content{}, errInit))
	receiveWindow(t, windows)

	loader := tw.nextCommit()
	close(gate) // init fails now
	failed := tw.waitForFailedFrame(640, 480)
	if equalPixels(loader.Pixels, failed.Pixels) {
		t.Error("the failure screen is the loader's")
	}
	// A failure screen that keeps repainting would be a window that never
	// goes quiet on an error. The callback of that frame is answered one vsync
	// later and is the only thing that could make one more.
	tw.expectNoCommitUntil(failed.At.Add(5 * 5 * time.Millisecond))

	if err := tw.closeAndWait(); !errors.Is(err, errInit) {
		t.Errorf("Run returned %v, want init's error", err)
	}
}

// Ruling R6: a panic in init is the same as init failing. The process
// survives, the stack is logged, the window shows the failure screen, and Run
// returns an error that carries the panic value.
func TestAPanicInInitIsAFailureNotACrash(t *testing.T) {
	errBoom := errors.New("boom")
	for _, tc := range []struct {
		name  string
		value any
		want  string
		is    error
	}{
		{"a string", "kaboom", "kaboom", nil},
		{"an error", errBoom, "boom", errBoom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureLog(t)
			windows := make(chan *Window, 1)
			tw := openWindow(t, wltest.Options{}, Config{}, func(w *Window) (Content, error) {
				windows <- w
				panic(tc.value)
			})
			w := receiveWindow(t, windows)

			tw.waitForFailedFrame(640, 480)

			err := tw.closeAndWait()
			if err == nil {
				t.Fatal("Run returned nil after a panic in init")
			}
			if !strings.Contains(err.Error(), "init panicked") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Run returned %q, want it to say init panicked with %q", err, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("Run returned %q, which does not wrap the panic value", err)
			}
			assertContextDone(t, w)
			assertNothingRunning(t, w)

			// The stack of the panic, not only its message: what is logged has to
			// say where in the application it happened.
			out := logged.String()
			if !strings.Contains(out, "init panicked") || !strings.Contains(out, tc.want) {
				t.Errorf("the log does not report the panic:\n%s", out)
			}
			if !strings.Contains(out, "goroutine ") || !strings.Contains(out, "lifecycle_test.go") {
				t.Errorf("the log has no stack pointing at the panicking init:\n%s", out)
			}
		})
	}
}

// An init that ends its goroutine without returning, as a test's t.FailNow
// or an application's runtime.Goexit does, is a failure as well: nothing would
// ever install a content, and the window would sit on its spinner forever.
func TestAnInitThatExitsItsGoroutineIsAFailure(t *testing.T) {
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		runtime.Goexit()
		return Content{}, nil
	})
	w := receiveWindow(t, windows)

	tw.waitForFailedFrame(640, 480)
	err := tw.closeAndWait()
	if err == nil || !strings.Contains(err.Error(), "init") {
		t.Errorf("Run returned %v, want an error about init", err)
	}
	assertNothingRunning(t, w)
}

// A content without Paint cannot be shown, and is a failure of init's, not a
// spinner nobody will ever end.
func TestAContentWithoutPaintFailsTheWindow(t *testing.T) {
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, Content{}, nil))
	w := receiveWindow(t, windows)

	tw.waitForFailedFrame(640, 480)
	err := tw.closeAndWait()
	if err == nil || !strings.Contains(err.Error(), "Paint") {
		t.Errorf("Run returned %v, want an error that names Paint", err)
	}
	assertNothingRunning(t, w)
}

// Run never waits for init: the application's code may be parked on
// something that never comes. Closing the window ends Run while init is still
// parked; when it finally returns, its result is dropped and nothing panics.
func TestABlockedInitDoesNotHangRunAndItsLateResultIsDropped(t *testing.T) {
	release := make(chan struct{})
	unpark := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unpark)

	var painted atomic.Int32
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, func(w *Window) (Content, error) {
		windows <- w
		<-release // deliberately deaf to w.Context: hostile application code
		return Content{Paint: func(cv *canvas.Canvas, _ uint32) bool {
			painted.Add(1)
			cv.Clear(appColor)
			return false
		}}, nil
	})
	w := receiveWindow(t, windows)
	tw.nextCommit() // the loader: the window is up while init is parked

	if err := tw.closeAndWait(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	assertContextDone(t, w)
	if n := w.owned.Load(); n != 0 {
		t.Errorf("%d goroutines the window owns are still running", n)
	}
	select {
	case <-w.initDone:
		t.Fatal("init finished before it was released: the test does not hold what it claims")
	default:
	}

	// Now let it go. Its result has nowhere to land.
	unpark()
	select {
	case <-w.initDone:
	case <-time.After(settle):
		t.Fatal("init did not finish after it was released")
	}
	if n := painted.Load(); n != 0 {
		t.Errorf("the late content painted %d frames into a closed window", n)
	}
	assertNothingRunning(t, w)
}

// A connection that dies under the window is not an orderly close: Run returns
// the error. And an init error still comes first, whatever ended the window.
func TestAConnectionErrorIsReturnedAndAnInitErrorTakesPriority(t *testing.T) {
	t.Run("no init error", func(t *testing.T) {
		windows := make(chan *Window, 1)
		tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, appContent(), nil))
		w := receiveWindow(t, windows)
		tw.waitForAppFrame()

		hangUp(t, tw.srv.Conn())
		err := tw.wait()
		if err == nil {
			t.Fatal("Run returned nil after the connection died")
		}
		if errors.Is(err, wlcore.ErrClosed) {
			t.Errorf("Run returned %v, which is the orderly close", err)
		}
		tw.noProtocolErrors()
		assertContextDone(t, w)
		assertNothingRunning(t, w)
	})
	t.Run("init error", func(t *testing.T) {
		errInit := errors.New("no network")
		windows := make(chan *Window, 1)
		tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, Content{}, errInit))
		w := receiveWindow(t, windows)
		tw.waitForFailedFrame(640, 480)

		hangUp(t, tw.srv.Conn())
		if err := tw.wait(); !errors.Is(err, errInit) {
			t.Errorf("Run returned %v, want init's error", err)
		}
		tw.noProtocolErrors()
		assertNothingRunning(t, w)
	})
}

// animation is what the compositor observed of a run of the animated
// scenario, and what the client says it did.
type animation struct {
	frames       [][]uint32 // the commits that carried the application's frames, in order
	paints       int        // calls to Paint
	buffersMade  int        // wl_buffers ever created
	buffersAlive int        // wl_buffers alive at the end
	usedBuffer   []int      // for each Paint, which buffer's canvas it drew into
}

const animatedFrames = 20

// animColor is frame n of the animation: a whole-buffer colour no other frame,
// and nothing the loader draws, shares.
func animColor(n int) canvas.Color {
	return canvas.Color{R: uint8(8 * n), G: 0x50, B: 0xc0, A: 0xff}
}

// runAnimation plays the animated scenario against a compositor that releases
// buffers as mode says: an application that paints animatedFrames frames, one
// per frame callback, and then stops asking for more.
func runAnimation(t *testing.T, mode wltest.ReleaseMode) animation {
	t.Helper()

	// Painting numbers its frames from 1, and expected[n] is the word a whole
	// buffer of frame n holds.
	expected := make([]uint32, animatedFrames+1)
	for n := 1; n <= animatedFrames; n++ {
		px := make([]uint32, 1)
		cv, err := canvas.New(canvas.Buffer{Pixels: px, Width: 1, Height: 1, Stride: 1}, 1, 1, 1)
		if err != nil {
			t.Fatalf("canvas.New: %v", err)
		}
		cv.Clear(animColor(n))
		expected[n] = px[0]
	}

	var (
		n       int                        // UI goroutine only
		seen    = map[*canvas.Canvas]int{} // which buffer a canvas is, in order of first use
		used    []int
		windows = make(chan *Window, 1)
	)
	tw := openWindow(t, wltest.Options{ReleaseMode: mode, Vsync: 2 * time.Millisecond}, Config{},
		func(w *Window) (Content, error) {
			windows <- w
			return Content{Paint: func(cv *canvas.Canvas, _ uint32) bool {
				n++
				id, ok := seen[cv]
				if !ok {
					id = len(seen)
					seen[cv] = id
				}
				used = append(used, id)
				cv.Clear(animColor(n))
				return n < animatedFrames
			}}, nil
		})
	w := receiveWindow(t, windows)

	var res animation
	for len(res.frames) < animatedFrames {
		c := tw.nextCommit()
		if len(c.Pixels) == 0 {
			continue
		}
		word := c.Pixels[0]
		uniform := true
		for _, p := range c.Pixels {
			if p != word {
				uniform = false
				break
			}
		}
		if !uniform {
			continue // the loader's
		}
		want := expected[len(res.frames)+1]
		if word != want {
			t.Fatalf("%v: application frame %d arrived as %#08x, want %#08x: a frame was lost, repeated or reordered",
				mode, len(res.frames)+1, word, want)
		}
		res.frames = append(res.frames, c.Pixels)
		if c.Width != 640 || c.Height != 480 {
			t.Fatalf("%v: frame %d is %dx%d, want 640x480", mode, len(res.frames), c.Width, c.Height)
		}
	}

	// Everything the window was going to paint has been: the UI has run what
	// was queued and there is no commit waiting.
	tw.barrier(w)
	tw.onUI(w, func() {
		res.paints = n
		res.usedBuffer = append([]int(nil), used...)
	})
	tw.expectNoCommit()
	res.buffersMade = len(tw.srv.Buffers())
	res.buffersAlive = tw.srv.LiveBuffers()

	if err := tw.closeAndWait(); err != nil {
		t.Errorf("%v: Run returned %v, want nil", mode, err)
	}
	assertNothingRunning(t, w)
	return res
}

// Acceptance 7. The two compositors of the fake differ in when they say a
// buffer is free again, and a client that only works with one is broken. "The
// same observable result" is defined as all of:
//
//   - the compositor was given exactly 20 application frames, in order, each
//     the whole 640x480 buffer of that frame's colour: none lost, repeated or
//     torn, and nothing else once the animation ended;
//   - the application's Paint ran exactly 20 times, so every frame it drew was
//     presented;
//   - the client created exactly the two buffers of its pool, and both are
//     still alive: nothing was rebuilt and nothing leaked;
//   - the compositor saw no protocol error.
//
// Which of the two buffers a frame goes into is not part of it, and does
// differ: released at once, the first buffer is free again for the next frame;
// held until the next commit, the frames alternate. What must hold in the
// holding mode is that a frame is never drawn into the buffer the compositor
// still holds, which is the one the previous frame went into.
func TestBothReleaseModesGiveTheSameObservableResult(t *testing.T) {
	immediate := runAnimation(t, wltest.ReleaseImmediately)
	onNext := runAnimation(t, wltest.ReleaseOnNextCommit)

	for name, res := range map[string]animation{"ReleaseImmediately": immediate, "ReleaseOnNextCommit": onNext} {
		if len(res.frames) != animatedFrames {
			t.Errorf("%s: %d frames reached the compositor, want %d", name, len(res.frames), animatedFrames)
		}
		if res.paints != animatedFrames {
			t.Errorf("%s: the application painted %d frames, want %d", name, res.paints, animatedFrames)
		}
		if res.buffersMade != frameCount || res.buffersAlive != frameCount {
			t.Errorf("%s: %d buffers created and %d alive, want %d and %d", name, res.buffersMade, res.buffersAlive, frameCount, frameCount)
		}
	}
	for i := range immediate.frames {
		if !equalPixels(immediate.frames[i], onNext.frames[i]) {
			t.Errorf("frame %d differs between the two release modes", i+1)
		}
	}
	for i := 1; i < len(onNext.usedBuffer); i++ {
		if onNext.usedBuffer[i] == onNext.usedBuffer[i-1] {
			t.Errorf("frame %d was drawn into the buffer frame %d had handed to a compositor that holds it: %v",
				i+1, i, onNext.usedBuffer)
			break
		}
	}
}

// SetTitle is safe from any goroutine and arrives as xdg_toplevel.set_title.
func TestSetTitleReachesTheCompositorFromAnyGoroutine(t *testing.T) {
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{Title: "first"}, func(w *Window) (Content, error) {
		windows <- w
		w.SetTitle("from init")
		return appContent(), nil
	})
	w := receiveWindow(t, windows)
	tw.waitForAppFrame()

	// The pong comes after the title on the same socket, and the fake handles
	// them in order, so once it is in, so is the title.
	tw.pingPong(1)
	if got := tw.srv.Title(); got != "from init" {
		t.Errorf("after init the title is %q, want %q", got, "from init")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.SetTitle("from another goroutine")
	}()
	select {
	case <-done:
	case <-time.After(settle):
		t.Fatal("SetTitle did not return")
	}
	tw.pingPong(2)
	if got := tw.srv.Title(); got != "from another goroutine" {
		t.Errorf("the title is %q, want %q", got, "from another goroutine")
	}

	tw.onUI(w, func() { w.SetTitle("from the UI goroutine") })
	tw.pingPong(3)
	if got := tw.srv.Title(); got != "from the UI goroutine" {
		t.Errorf("the title is %q, want %q", got, "from the UI goroutine")
	}

	tw.finish()
	assertNothingRunning(t, w)
}

// Do may be called from any goroutine and runs every closure on the UI
// goroutine, one at a time.
func TestDoFromManyGoroutinesRunsEveryClosureOnTheUIGoroutine(t *testing.T) {
	const callers = 16
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, appContent(), nil))
	w := receiveWindow(t, windows)
	tw.waitForAppFrame()

	var ui uint64
	tw.onUI(w, func() { ui = goid() })

	var (
		ran       int // written by the closures without a lock: only the UI goroutine may
		ids       [callers]uint64
		callerIDs [callers]uint64
		wg        sync.WaitGroup
	)
	wg.Add(callers)
	for i := range callers {
		go func() {
			callerIDs[i] = goid()
			w.Do(func() {
				ran++
				ids[i] = goid()
				wg.Done()
			})
		}()
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(settle):
		t.Fatal("not every closure given to Do ran")
	}

	var total int
	tw.onUI(w, func() { total = ran })
	if total != callers {
		t.Errorf("%d closures ran, want %d", total, callers)
	}
	for i, id := range ids {
		if id != ui {
			t.Errorf("closure %d ran on goroutine %d, want the UI goroutine %d", i, id, ui)
		}
		if callerIDs[i] == ui {
			t.Errorf("caller %d was the UI goroutine: the test does not call from elsewhere", i)
		}
	}
	tw.finish()
}

// The buffers are the window's own machinery, and their failure has to be
// loud. While the application is still loading it is a failed start: the
// failure screen is shown, the window stays open and Run returns the error.
func TestAPoolFailureWhileLoadingShowsTheFailureScreenAndRunReturnsTheError(t *testing.T) {
	t.Run("a size the pool cannot build", func(t *testing.T) {
		gate := make(chan struct{})
		var touched atomic.Bool // set by any callback of the application's
		unpark := sync.OnceFunc(func() { close(gate) })
		t.Cleanup(unpark)
		windows := make(chan *Window, 1)
		tw := openWindow(t, wltest.Options{}, Config{},
			windowFrom(windows, gate, Content{
				Paint: func(cv *canvas.Canvas, _ uint32) bool {
					touched.Store(true)
					cv.Clear(appColor)
					return false
				},
				OnResize: func(int, int) { touched.Store(true) },
			}, nil))
		w := receiveWindow(t, windows)
		loader := tw.nextCommit()

		tw.srv.Configure(math.MaxInt32, math.MaxInt32)
		// Still 640x480: the size the pool could not honor is not the window's.
		failed := tw.waitForFailedFrame(640, 480)
		if equalPixels(failed.Pixels, loader.Pixels) {
			t.Error("the failure screen is the loader's")
		}

		// init succeeds afterwards, into a window that already failed: its
		// content is not installed and never paints.
		unpark()
		waitInitDone(t, w) // init has queued its content for the UI
		tw.barrier(w)      // and the UI has run whatever it queued
		if touched.Load() {
			t.Error("the application was called in a window that had failed")
		}

		err := tw.closeAndWait()
		if err == nil || !strings.Contains(err.Error(), "buffers") {
			t.Errorf("Run returned %v, want an error about the buffers", err)
		}
		assertNothingRunning(t, w)
	})

	t.Run("a buffer the compositor was never given", func(t *testing.T) {
		gate := make(chan struct{})
		t.Cleanup(func() { close(gate) })
		windows := make(chan *Window, 1)
		// The second buffer of the first pool: the first can still show the
		// failure.
		tw := openWindowWith(t, wltest.Options{}, Config{}, windowFrom(windows, gate, appContent(), nil),
			failCreates(func(n int) bool { return n == 2 }))
		w := receiveWindow(t, windows)

		tw.waitForFailedFrame(640, 480)
		err := tw.closeAndWait()
		if err == nil || !strings.Contains(err.Error(), "buffers") {
			t.Errorf("Run returned %v, want an error about the buffers", err)
		}
		assertNothingRunning(t, w)
	})
}

// With no buffer at all there is nothing to show the failure on, and a window
// that stays open and blank forever is the worst answer: it closes itself and
// Run returns the error.
func TestAWindowThatCannotHaveAnyBufferClosesItselfWithTheError(t *testing.T) {
	t.Run("every buffer fails", func(t *testing.T) {
		gate := make(chan struct{})
		t.Cleanup(func() { close(gate) })
		windows := make(chan *Window, 1)
		tw := openWindowWith(t, wltest.Options{}, Config{}, windowFrom(windows, gate, appContent(), nil),
			failCreates(func(int) bool { return true }))
		w := receiveWindow(t, windows)

		err := tw.wait() // nobody closed it
		if err == nil || !strings.Contains(err.Error(), "buffers") {
			t.Errorf("Run returned %v, want an error about the buffers", err)
		}
		tw.noProtocolErrors()
		assertContextDone(t, w)
		if n := w.owned.Load(); n != 0 {
			t.Errorf("%d goroutines the window owns are still running", n)
		}
	})
	t.Run("the first size cannot be built", func(t *testing.T) {
		gate := make(chan struct{})
		t.Cleanup(func() { close(gate) })
		windows := make(chan *Window, 1)
		tw := openWindow(t, wltest.Options{ManualConfigure: true}, Config{Width: math.MaxInt32, Height: math.MaxInt32},
			windowFrom(windows, gate, appContent(), nil))
		w := receiveWindow(t, windows)
		tw.waitForRequest("wl_surface.commit")
		tw.srv.Configure(0, 0)

		err := tw.wait()
		if err == nil || !strings.Contains(err.Error(), "buffers") {
			t.Errorf("Run returned %v, want an error about the buffers", err)
		}
		tw.noProtocolErrors()
		if n := w.owned.Load(); n != 0 {
			t.Errorf("%d goroutines the window owns are still running", n)
		}
	})
}

// Once the application is running, a resize to a size the pool cannot build is
// refused and the window carries on at the size it had: the old buffers are
// intact, the application is not told of a size that never took effect, and
// Run's result is still the orderly close.
func TestAResizeThePoolCannotHonorIsRefusedWhileTheApplicationRuns(t *testing.T) {
	logged := captureLog(t)
	rec := &recorder{}
	windows := make(chan *Window, 1)
	tw := openWindow(t, wltest.Options{}, Config{}, windowFrom(windows, nil, rec.content(), nil))
	w := receiveWindow(t, windows)
	tw.waitForAppFrame()

	tw.srv.Configure(math.MaxInt32, math.MaxInt32)
	tw.barrier(w)

	var width, height int
	tw.onUI(w, func() { width, height = w.Size() })
	if width != 640 || height != 480 {
		t.Errorf("Size reports %dx%d after a refused resize, want 640x480", width, height)
	}
	for _, entry := range tw.log(w, rec) {
		if strings.HasPrefix(entry, "resize") && entry != "resize 640x480" {
			t.Errorf("the application was told %q for a size that never took effect", entry)
		}
	}
	if !strings.Contains(logged.String(), "buffers") {
		t.Errorf("the refusal was not logged:\n%s", logged.String())
	}

	// And it still works: a size the pool can build is honored afterwards.
	tw.srv.Configure(800, 600)
	tw.waitForAppFrameAtSize(800, 600)

	tw.finish()
	assertNothingRunning(t, w)
}

// A buffer that cannot be created once the application runs leaves a pool
// that can only ever stall, and the window closes with the error.
func TestAPoolFailureWhileTheApplicationRunsClosesTheWindowWithTheError(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(n int) bool
	}{
		{"every buffer of the new pool", func(n int) bool { return n > frameCount }},
		{"one buffer of the new pool", func(n int) bool { return n == frameCount+1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows := make(chan *Window, 1)
			tw := openWindowWith(t, wltest.Options{}, Config{}, windowFrom(windows, nil, appContent(), nil),
				failCreates(tc.fail))
			w := receiveWindow(t, windows)
			tw.waitForAppFrame()

			tw.srv.Configure(800, 600)
			err := tw.wait() // nobody closed it
			if err == nil || !strings.Contains(err.Error(), "buffers") {
				t.Errorf("Run returned %v, want an error about the buffers", err)
			}
			tw.noProtocolErrors()
			assertContextDone(t, w)
			assertNothingRunning(t, w)
		})
	}
}
