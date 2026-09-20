package window

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/wayland/wlcore"
)

// realBound is how long the real compositor gets for each step below. It is
// generous on purpose: this test measures, and only fails on a window that
// does not come up at all.
const realBound = 3 * time.Second

// requireRealCompositor skips the test unless it was asked for and there is a
// compositor to be run against. The test opens a window on the desktop of
// whoever runs it, so it must never run by accident: a plain "go test ./..."
// skips it.
func requireRealCompositor(t *testing.T) {
	t.Helper()
	if os.Getenv("GGUI_REAL_WAYLAND") != "1" {
		t.Skip("opens a window on the live Wayland session; run with GGUI_REAL_WAYLAND=1 go test ./window -run Real -race -v")
	}

	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		t.Skip("GGUI_REAL_WAYLAND=1 but WAYLAND_DISPLAY is not set: there is no Wayland session to open a window on")
	}
	path := name
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			t.Skip("GGUI_REAL_WAYLAND=1 but XDG_RUNTIME_DIR is not set: there is no Wayland socket to find")
		}
		path = filepath.Join(dir, name)
	}
	probe, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Skipf("GGUI_REAL_WAYLAND=1 but the compositor's socket is not reachable: %v", err)
	}
	probe.Close()
}

// The window opens against the compositor the machine is running, presents a
// frame, and closes when the application says so, with the compositor
// reporting no protocol error. Everything else in this package is proved
// against a fake compositor that says what its author expects of a real one;
// this is the test that asks a real one.
//
// It goes through run and not Run so that the connection is the test's, and a
// protocol error, which the compositor reports by killing the connection, can
// be read afterwards instead of only ending the window.
func TestRealCompositorOpensPresentsAndCloses(t *testing.T) {
	requireRealCompositor(t)

	conn, err := wlcore.Connect()
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// run closes the connection itself; this is for a test that fails before
	// that, so that no goroutine is left holding a socket. Close is idempotent.
	t.Cleanup(func() { conn.Close() })

	var (
		mu         sync.Mutex
		protoError []string
	)
	conn.OnError(func(objectID, code uint32, msg string) {
		mu.Lock()
		defer mu.Unlock()
		protoError = append(protoError, fmt.Sprintf("object %d, code %d: %s", objectID, code, msg))
	})
	// A copy, since the connection's goroutine may still be adding to it.
	protocolErrors := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), protoError...)
	}

	var (
		start      = time.Now()
		win        = make(chan *Window, 1)
		firstPaint = make(chan time.Duration, 1)
		secondOnce sync.Once
		second     = make(chan struct{})
		paints     int // UI goroutine only, until run has returned
	)
	init := func(w *Window) (Content, error) {
		win <- w
		return Content{Paint: func(cv *canvas.Canvas, _ uint32) (animating bool) {
			paints++
			cv.Clear(appColor)
			switch paints {
			case 1:
				firstPaint <- time.Since(start)
			case 2:
				// A second paint only comes from the compositor answering the
				// frame callback of the first one, so it is the proof that the
				// frame was presented and not merely committed.
				secondOnce.Do(func() { close(second) })
			}
			return true // keep asking for frames, so that the second one comes
		}}, nil
	}

	returned := make(chan error, 1)
	go func() {
		returned <- run(conn, Config{Title: "ggui window smoke test", AppID: "ggui.window.smoke", Width: 320, Height: 200}, init)
	}()

	var w *Window
	select {
	case w = <-win:
	case err := <-returned:
		t.Fatalf("run returned before the application started: %v; protocol errors: %v", err, protocolErrors())
	case <-time.After(realBound):
		t.Fatalf("init was not called within %v", realBound)
	}

	select {
	case elapsed := <-firstPaint:
		t.Logf("time to the first frame: %v", elapsed)
	case err := <-returned:
		t.Fatalf("run returned before the first frame: %v; protocol errors: %v", err, protocolErrors())
	case <-time.After(realBound):
		t.Fatalf("no frame was painted within %v of opening the window; protocol errors: %v", realBound, protocolErrors())
	}

	// Not every compositor answers a frame callback at once: one that has the
	// window out of sight may hold it back. So the round trip is reported and
	// not required; the first frame and the clean close are what fail the test.
	select {
	case <-second:
		t.Logf("the compositor answered the first frame's callback: the frame was presented")
	case <-time.After(realBound):
		t.Logf("the compositor did not answer the first frame's callback within %v", realBound)
	}

	closed := time.Now()
	go w.Close() // from another goroutine, as an application's own would be
	select {
	case err := <-returned:
		if err != nil {
			t.Errorf("run returned %v after Close, want nil", err)
		}
		t.Logf("closed in %v", time.Since(closed))
	case <-time.After(realBound):
		t.Fatalf("run did not return within %v of Close", realBound)
	}

	if errs := protocolErrors(); len(errs) != 0 {
		t.Errorf("the compositor reported protocol errors: %v", errs)
	}
}
