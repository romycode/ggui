package eventloop

import (
	"encoding/binary"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/romycode/ggui/wayland/wlcore"
)

// opSurfaceCommit is wl_surface.commit, the request that makes a frame real.
const opSurfaceCommit = 6

// startup is a window opening against a fake compositor. The compositor
// answers every commit with a frame callback after a vsync's worth of delay,
// which is what a real one does and what keeps the loader from spinning as
// fast as the CPU allows.
type startup struct {
	t   *testing.T
	l   *Loop
	ui  *UI
	log chan paintRecord

	commits   atomic.Int64
	firstAt   atomic.Int64 // unix nanos of the first commit, 0 if none yet
	surfaceID uint32
}

type paintRecord struct {
	phase Phase
	at    time.Time
}

// newStartup builds a connection with a wl_surface, a Loop and a UI whose
// Paint draws the loader while loading and presents by committing. It does
// not send the first configure: the test does, because that is the moment
// the clock starts.
func newStartup(t *testing.T) *startup {
	t.Helper()
	conn, comp := newTestConn(t)
	s := &startup{t: t, log: make(chan paintRecord, 4096)}
	s.l, _ = startLoop(t, conn, nil)

	var surface *wlcore.Surface
	s.l.Post(func() {
		reg, err := conn.Display().GetRegistry()
		if err != nil {
			t.Errorf("GetRegistry: %v", err)
			return
		}
		compositor, err := reg.Bind(1, 1, wlcore.CompositorInterface)
		if err != nil {
			t.Errorf("Bind: %v", err)
			return
		}
		if surface, err = compositor.CreateSurface(); err != nil {
			t.Errorf("CreateSurface: %v", err)
		}
	})
	comp.readRequest() // wl_display.get_registry
	comp.readRequest() // wl_registry.bind
	_, _, body := comp.readRequest()
	s.surfaceID = binary.NativeEndian.Uint32(body) // wl_compositor.create_surface(new_id)

	cv := newTestCanvas(t, 320, 240)
	s.ui = NewUI()
	go s.ui.Run(Handler{Paint: func(now uint32) (bool, bool) {
		s.log <- paintRecord{phase: s.ui.Phase(), at: time.Now()}
		switch s.ui.Phase() {
		case PhaseLoading:
			PaintLoader(cv, 320, 240, now)
		case PhaseFailed:
			PaintFailed(cv, 320, 240)
		}
		s.l.Post(func() { surface.Commit() }) // stands for attach, damage, frame, commit
		return true, false
	}})
	t.Cleanup(func() { s.ui.Push(Event{Kind: EvClosed}) })

	// The compositor: every commit is answered with a frame callback one
	// vsync later.
	go func() {
		for {
			id, op, _, err := comp.tryReadRequest()
			if err != nil {
				return
			}
			if id == s.surfaceID && op == opSurfaceCommit {
				s.commits.Add(1)
				s.firstAt.CompareAndSwap(0, time.Now().UnixNano())
				time.AfterFunc(16*time.Millisecond, func() {
					s.ui.Push(Event{Kind: EvFrameDone, Time: uint32(time.Now().UnixMilli())})
				})
			}
		}
	}()
	return s
}

// tryReadRequest is readRequest for a goroutine that has to stop quietly when
// the connection ends, instead of failing the test from outside it.
func (c *compositor) tryReadRequest() (objectID uint32, opcode uint16, body []byte, err error) {
	var hdr [8]byte
	if _, err := readFull(c.conn, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	objectID = binary.NativeEndian.Uint32(hdr[0:4])
	sizeOp := binary.NativeEndian.Uint32(hdr[4:8])
	body = make([]byte, int(sizeOp>>16)-8)
	if _, err := readFull(c.conn, body); err != nil {
		return 0, 0, nil, err
	}
	return objectID, uint16(sizeOp & 0xffff), body, nil
}

func (s *startup) phases() (loading, ready, failed []time.Time) {
	for {
		select {
		case r := <-s.log:
			switch r.phase {
			case PhaseLoading:
				loading = append(loading, r.at)
			case PhaseReady:
				ready = append(ready, r.at)
			case PhaseFailed:
				failed = append(failed, r.at)
			}
		default:
			return
		}
	}
}

// The requirement the package was shaped around: the window opens at once.
// The application takes a second to build its UI — fonts, data, widgets —
// and none of it may delay the first frame. The first thing the compositor
// gets is the loader, within 100ms of the surface being configured, and the
// real UI follows only after the application says it is ready.
func TestStartupWindowOpensBeforeTheApplicationIsReady(t *testing.T) {
	const initTime = time.Second
	s := newStartup(t)

	// The application's init, on its own goroutine, as an application would
	// run it: slow, and finishing with SetReady on the UI goroutine.
	var initDoneAt atomic.Int64
	go func() {
		time.Sleep(initTime)
		s.ui.Do(func() {
			s.ui.SetReady()
			initDoneAt.Store(time.Now().UnixNano())
		})
	}()

	configuredAt := time.Now()
	s.ui.Push(Event{Kind: EvConfigure, Width: 320, Height: 240})

	deadline := time.Now().Add(2 * time.Second)
	for s.firstAt.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the compositor never received a frame")
		}
		time.Sleep(time.Millisecond)
	}
	if first := time.Unix(0, s.firstAt.Load()).Sub(configuredAt); first > 100*time.Millisecond {
		t.Errorf("the first frame reached the compositor %v after the configure, want under 100ms", first)
	}

	// Let the application finish, then let the real UI paint and settle.
	for initDoneAt.Load() == 0 {
		if time.Now().After(configuredAt.Add(initTime + 3*time.Second)) {
			t.Fatal("the application never finished starting")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	settled := s.commits.Load()
	time.Sleep(150 * time.Millisecond)

	loading, ready, failed := s.phases()
	doneAt := time.Unix(0, initDoneAt.Load())

	if len(failed) != 0 {
		t.Errorf("%d frames in the failed phase", len(failed))
	}
	if len(loading) < 10 {
		t.Errorf("only %d loader frames in %v: the loader was not animating", len(loading), initTime)
	}
	for _, at := range loading {
		if at.After(doneAt.Add(50 * time.Millisecond)) {
			t.Errorf("a loader frame was painted %v after the application was ready", at.Sub(doneAt))
			break
		}
	}
	if len(ready) == 0 {
		t.Fatal("the real UI never painted after the application was ready")
	}
	for _, at := range ready {
		if at.Before(doneAt) {
			t.Errorf("the real UI painted %v before the application said it was ready", doneAt.Sub(at))
			break
		}
	}
	if len(ready) != 1 {
		t.Errorf("a static UI painted %d frames, want 1", len(ready))
	}
	if now := s.commits.Load(); now != settled {
		t.Errorf("the compositor kept receiving frames from a static UI (%d, then %d)", settled, now)
	}
}

// An application that fails to start shows it, once, and the window stays
// open. Closing the window on failure would lose the reason.
func TestStartupFailedInitShowsTheFailureAndKeepsTheWindowOpen(t *testing.T) {
	s := newStartup(t)
	boom := errors.New("no fonts installed")

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.ui.Do(func() { s.ui.Fail(boom) })
	}()
	s.ui.Push(Event{Kind: EvConfigure, Width: 320, Height: 240})

	time.Sleep(600 * time.Millisecond)
	settled := s.commits.Load()
	time.Sleep(150 * time.Millisecond)

	loading, ready, failed := s.phases()
	if len(loading) == 0 {
		t.Error("the loader never showed before the failure")
	}
	if len(ready) != 0 {
		t.Errorf("%d frames of a real UI that never started", len(ready))
	}
	if len(failed) != 1 {
		t.Errorf("the failure screen painted %d times, want once and then quiet", len(failed))
	}
	if now := s.commits.Load(); now != settled {
		t.Errorf("the failure screen kept animating (%d commits, then %d)", settled, now)
	}
	select {
	case <-s.l.conn.Done():
		t.Error("the connection was closed by the failure")
	default:
	}
}
