package window

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/internal/wltest"
	"github.com/romycode/ggui/wayland/wlcore"
)

// createCall is what the stub create saw: the frame it was asked to give a
// buffer, the size of the descriptor it was handed, and the buffer geometry.
type createCall struct {
	f             *frame
	fdSize        int64
	size          int
	width, height int32
}

// stubPool is a pool wired to plain funcs, so a test needs no connection and
// no second goroutine. Everything posted or done is queued and run by the
// test, on the test goroutine, when it says so.
type stubPool struct {
	t *testing.T
	p *pool

	posted    []func()
	done      []func()
	pushed    []eventloop.Event
	created   []createCall
	destroyed []*wlcore.Buffer
	adopted   int
	failures  []error
}

func newStubPool(t *testing.T) *stubPool {
	t.Helper()
	s := &stubPool{t: t}
	s.p = newPool(nil,
		func(fn func()) { s.posted = append(s.posted, fn) },
		func(fn func()) { s.done = append(s.done, fn) },
		func(ev eventloop.Event) { s.pushed = append(s.pushed, ev) },
	)
	s.p.create = func(f *frame, fd, size int, width, height int32) {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			t.Errorf("fstat of the descriptor handed to create: %v", err)
		}
		unix.Close(fd)
		s.created = append(s.created, createCall{f: f, fdSize: st.Size, size: size, width: width, height: height})
	}
	s.p.destroy = func(b *wlcore.Buffer) { s.destroyed = append(s.destroyed, b) }
	s.p.adopted = func() { s.adopted++ }
	s.p.failed = func(err error) { s.failures = append(s.failures, err) }
	t.Cleanup(func() { s.p.close(false) })
	return s
}

// runPosted runs, in order, what the pool queued for the Wayland goroutine.
func (s *stubPool) runPosted() {
	q := s.posted
	s.posted = nil
	for _, fn := range q {
		fn()
	}
}

// Buffers come from the Wayland goroutine, so a frame whose wl_buffer has not
// been created yet must not be handed out, and neither may a busy or dead one.
func TestFreeSkipsFramesWithoutABufferBusyAndDeadOnes(t *testing.T) {
	s := newStubPool(t)
	p := s.p
	if p.free() != nil {
		t.Error("an empty pool has a free frame")
	}

	b1, b2, b3 := &wlcore.Buffer{}, &wlcore.Buffer{}, &wlcore.Buffer{}
	pending := &frame{}
	p.frames[0] = pending
	if p.free() != nil {
		t.Error("a frame with no wl_buffer yet was handed out")
	}

	busy := &frame{buf: b1, busy: true}
	dead := &frame{buf: b3, dead: true}
	p.frames[0], p.frames[1] = busy, dead
	if p.free() != nil {
		t.Error("a busy frame and a dead one were handed out")
	}

	free := &frame{buf: b2}
	p.frames[0], p.frames[1] = busy, free
	if got := p.free(); got != free {
		t.Errorf("free returned %v, want the idle frame", got)
	}
	p.frames = [frameCount]*frame{} // nothing to unmap in this test
}

// The UI reserves memory and posts the wl_buffer creation; nothing is usable
// until the Wayland goroutine has reported back. The descriptor handed to the
// creation is exactly the size of the mapping.
func TestEnsureMapsOneFramePerSlotAndPostsItsBufferCreation(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(s.posted) != frameCount {
		t.Fatalf("%d creations posted, want one per frame (%d)", len(s.posted), frameCount)
	}
	if len(s.created) != 0 {
		t.Fatal("create ran before the Wayland goroutine did")
	}
	for i, f := range s.p.frames {
		if f == nil || f.buf != nil || f.busy || f.dead {
			t.Fatalf("frame %d: want a fresh mapped frame with no wl_buffer, got %+v", i, f)
		}
		if f.cv.PixelWidth() != 100 || f.cv.PixelHeight() != 80 {
			t.Errorf("frame %d is %dx%d, want 100x80", i, f.cv.PixelWidth(), f.cv.PixelHeight())
		}
		if len(f.data) != 100*80*4 {
			t.Errorf("frame %d maps %d bytes, want %d", i, len(f.data), 100*80*4)
		}
	}
	if s.p.free() != nil {
		t.Error("a frame was free before any buffer existed")
	}

	s.runPosted()
	if len(s.created) != frameCount {
		t.Fatalf("create ran %d times, want %d", len(s.created), frameCount)
	}
	for i, c := range s.created {
		if c.f != s.p.frames[i] || c.width != 100 || c.height != 80 || c.size != 100*80*4 || c.fdSize != int64(c.size) {
			t.Errorf("create %d = %+v, want frame %d, 100x80, 32000 bytes", i, c, i)
		}
	}
}

func TestEnsureWithTheSameSizeKeepsThePool(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatal(err)
	}
	first := s.p.frames
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatal(err)
	}
	if len(s.posted) != frameCount || s.p.frames != first {
		t.Error("an unchanged size rebuilt the pool")
	}
}

// Buffers cannot change size, so a resize replaces the whole pool: the old
// frames die, the ones that already had a wl_buffer get it destroyed, and the
// ones still waiting for theirs are destroyed when it arrives.
func TestEnsureReplacesThePoolOnResize(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatal(err)
	}
	first := s.p.frames
	s.runPosted()

	// The Wayland goroutine has reported one buffer back, the other is
	// still on its way.
	b0 := &wlcore.Buffer{}
	s.p.adopt(first[0], b0)

	if err := s.p.ensure(120, 90); err != nil {
		t.Fatal(err)
	}
	if !first[0].dead || !first[1].dead {
		t.Error("the replaced frames were not marked dead")
	}
	// One destroy for the frame that had a buffer, and a creation per new
	// frame.
	if want := 1 + frameCount; len(s.posted) != want {
		t.Fatalf("%d posts, want %d", len(s.posted), want)
	}
	s.runPosted()
	if len(s.destroyed) != 1 || s.destroyed[0] != b0 {
		t.Errorf("destroyed %v, want exactly the adopted buffer", s.destroyed)
	}
	for i, f := range s.p.frames {
		if f == first[i] || f.cv.PixelWidth() != 120 || f.cv.PixelHeight() != 90 {
			t.Errorf("frame %d was not replaced by a 120x90 one", i)
		}
	}
	if s.p.free() != nil {
		t.Error("a frame of the new pool was free before its buffer existed")
	}
}

// A size that cannot be built must leave the pool that works untouched, and
// must not ask the Wayland goroutine for anything.
func TestEnsureRejectsAnImpossibleSizeAndKeepsThePool(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatal(err)
	}
	first := s.p.frames
	posts := len(s.posted)

	for _, sz := range [][2]int32{{0, 80}, {100, 0}, {-1, 80}, {100, -5}, {math.MaxInt32, math.MaxInt32}, {30000, 30000}} {
		if err := s.p.ensure(sz[0], sz[1]); err == nil {
			t.Errorf("ensure(%d, %d) succeeded", sz[0], sz[1])
		}
	}
	if s.p.frames != first || first[0].dead || len(s.posted) != posts {
		t.Error("a rejected size disturbed the pool")
	}
}

// adopt is what the Wayland goroutine's report runs on the UI goroutine.
func TestAdoptGivesTheFrameItsBufferAndTellsTheOwner(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 10); err != nil {
		t.Fatal(err)
	}
	b := &wlcore.Buffer{}
	s.p.adopt(s.p.frames[0], b)
	if s.p.frames[0].buf != b {
		t.Fatal("the frame did not adopt the buffer")
	}
	if s.adopted != 1 {
		t.Errorf("the owner was told %d times, want 1", s.adopted)
	}
	if got := s.p.free(); got != s.p.frames[0] {
		t.Errorf("free = %v, want the frame that just got its buffer", got)
	}
}

// The property the whole design leans on: a buffer that was still being
// created when the pool was replaced must be destroyed when it arrives, never
// adopted and never leaked.
func TestABufferForADeadFrameIsDestroyedOnArrival(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatal(err)
	}
	old := s.p.frames
	if err := s.p.ensure(120, 90); err != nil { // replaced while both buffers are in flight
		t.Fatal(err)
	}
	s.posted = nil

	late := &wlcore.Buffer{}
	s.p.adopt(old[0], late)
	if old[0].buf != nil {
		t.Error("a dead frame adopted its late buffer")
	}
	if s.adopted != 0 {
		t.Error("the owner was told about a buffer nobody can use")
	}
	if len(s.posted) != 1 {
		t.Fatalf("%d posts, want the one destroy", len(s.posted))
	}
	s.runPosted()
	if len(s.destroyed) != 1 || s.destroyed[0] != late {
		t.Errorf("destroyed %v, want the late buffer", s.destroyed)
	}
	if s.p.free() != nil {
		t.Error("the late buffer made a frame of the new pool free")
	}
}

// One buffer per frame, ever: a second arrival for a frame that already has
// one is a bug somewhere, and must not replace the first, which the
// compositor may be reading.
func TestASecondBufferForTheSameFrameIsDestroyed(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 10); err != nil {
		t.Fatal(err)
	}
	first, second := &wlcore.Buffer{}, &wlcore.Buffer{}
	s.p.adopt(s.p.frames[0], first)
	s.p.adopt(s.p.frames[0], second)
	if s.p.frames[0].buf != first {
		t.Error("the frame swapped its buffer")
	}
	s.runPosted()
	if len(s.destroyed) != 1 || s.destroyed[0] != second {
		t.Errorf("destroyed %v, want the second buffer", s.destroyed)
	}
}

func TestReleasedFreesOnlyTheFrameItNames(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 10); err != nil {
		t.Fatal(err)
	}
	b0, b1 := &wlcore.Buffer{}, &wlcore.Buffer{}
	s.p.adopt(s.p.frames[0], b0)
	s.p.adopt(s.p.frames[1], b1)
	s.p.frames[0].busy, s.p.frames[1].busy = true, true

	s.p.released(&wlcore.Buffer{}) // a buffer the pool never made
	s.p.released(nil)
	if !s.p.frames[0].busy || !s.p.frames[1].busy {
		t.Fatal("a release for a buffer the pool does not own freed a frame")
	}
	s.p.released(b1)
	if !s.p.frames[0].busy {
		t.Error("released freed a frame it did not name")
	}
	if s.p.frames[1].busy {
		t.Error("released did not free the frame it named")
	}
	if got := s.p.free(); got != s.p.frames[1] {
		t.Errorf("free = %v, want the released frame", got)
	}
}

// close unmaps always, and destroys buffers only when there is still a
// connection to destroy them with.
func TestCloseUnmapsAndDestroysOnlyWhenAsked(t *testing.T) {
	for _, destroy := range []bool{true, false} {
		s := newStubPool(t)
		if err := s.p.ensure(10, 10); err != nil {
			t.Fatal(err)
		}
		frames := s.p.frames
		b := &wlcore.Buffer{}
		s.p.adopt(frames[0], b) // frames[1] has no buffer yet
		s.posted = nil

		s.p.close(destroy)
		s.p.close(destroy) // closing twice is harmless

		for i, f := range frames {
			if !f.dead || f.data != nil {
				t.Errorf("destroy=%v: frame %d not dead and unmapped: %+v", destroy, i, f)
			}
		}
		if s.p.free() != nil {
			t.Errorf("destroy=%v: a closed pool handed out a frame", destroy)
		}
		want := 0
		if destroy {
			want = 1 // only the frame that had a buffer
		}
		if len(s.posted) != want {
			t.Fatalf("destroy=%v: %d posts, want %d", destroy, len(s.posted), want)
		}
		s.runPosted()
		if destroy && (len(s.destroyed) != 1 || s.destroyed[0] != b) {
			t.Errorf("destroyed %v, want the adopted buffer", s.destroyed)
		}
	}
}

// The canvas draws straight into the mapping, and wl_shm's argb8888 is little
// endian: a pixel is the bytes B, G, R, A.
func TestFrameCanvasDrawsIntoTheMappingAsARGB8888(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(4, 3); err != nil {
		t.Fatal(err)
	}
	f := s.p.frames[0]
	f.cv.Clear(canvas.Color{R: 0x11, G: 0x22, B: 0x33, A: 0xff})
	if err := f.cv.Err(); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(f.data); got != 0xff112233 {
		t.Errorf("first pixel is %#08x, want 0xff112233", got)
	}
	if got := binary.LittleEndian.Uint32(f.data[len(f.data)-4:]); got != 0xff112233 {
		t.Errorf("last pixel is %#08x, want 0xff112233", got)
	}
}

// Scale is one field: the canvas draws in logical units, and the buffer is
// the physical size, so HiDPI changes nothing for whoever uses the pool.
func TestScaleMakesTheBufferPhysicalAndTheCanvasLogical(t *testing.T) {
	s := newStubPool(t)
	s.p.scale = 2
	if err := s.p.ensure(50, 40); err != nil {
		t.Fatal(err)
	}
	f := s.p.frames[0]
	if f.cv.Width() != 50 || f.cv.Height() != 40 || f.cv.PixelWidth() != 100 || f.cv.PixelHeight() != 80 {
		t.Errorf("canvas is %dx%d logical, %dx%d physical; want 50x40 and 100x80",
			f.cv.Width(), f.cv.Height(), f.cv.PixelWidth(), f.cv.PixelHeight())
	}
	s.runPosted()
	if c := s.created[0]; c.width != 100 || c.height != 80 || c.size != 100*80*4 {
		t.Errorf("buffer is %dx%d (%d bytes), want the physical 100x80", c.width, c.height, c.size)
	}
	// The same logical size at the same scale must not rebuild.
	if err := s.p.ensure(50, 40); err != nil || len(s.posted) != 0 {
		t.Errorf("same size rebuilt the pool (err %v, %d posts)", err, len(s.posted))
	}
}

// fakeWindow is a pool over a real eventloop.Loop and a real eventloop.UI,
// against the fake compositor. The Loop goroutine is the client's pump and the
// Wayland goroutine; the UI goroutine owns the pool. The test goroutine
// touches the pool only through onUI, and the Wayland side only through
// Post.
type fakeWindow struct {
	t    *testing.T
	srv  *wltest.Server
	conn *wlcore.Conn
	loop *eventloop.Loop
	ui   *eventloop.UI
	p    *pool

	// surface is a plain wl_surface with no xdg role, enough to attach and
	// commit a buffer and get a release back. Wayland goroutine only.
	surface *wlcore.Surface

	adopted  chan struct{} // one value per buffer the pool adopted
	released chan struct{} // one value per release the pool processed
}

// settle is how long a test waits for something another goroutine has to do. It
// is generous on purpose and costs nothing when the test is green: a wait that
// times out only says something is wrong, and a tight one says so on an
// overloaded machine too. It is a bound on waiting, never an assertion about how
// long something takes.
const settle = 10 * time.Second

func newFakeWindow(t *testing.T, opts wltest.Options) *fakeWindow {
	t.Helper()
	w := &fakeWindow{
		t:        t,
		srv:      wltest.NewServer(t, opts),
		adopted:  make(chan struct{}, 64),
		released: make(chan struct{}, 64),
	}
	w.conn = w.srv.Conn()

	var err error
	if w.loop, err = eventloop.New(w.conn); err != nil {
		t.Fatalf("eventloop.New: %v", err)
	}
	w.ui = eventloop.NewUI()

	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		w.loop.Run()
	}()
	uiDone := make(chan struct{})
	go func() {
		defer close(uiDone)
		w.ui.Run(eventloop.Handler{OnEvent: func(ev eventloop.Event) {
			if ev.Kind == eventloop.EvBufferRelease {
				w.p.released(ev.Buffer)
				w.released <- struct{}{}
			}
		}})
	}()

	// Cleanups run last to first: the UI stops, then the connection, and only
	// then does the pool unmap, with nothing left that could touch it.
	t.Cleanup(func() {
		if w.p != nil {
			w.p.close(false)
		}
	})
	t.Cleanup(func() {
		w.loop.Close()
		select {
		case <-loopDone:
		case <-time.After(settle):
			t.Error("the loop did not stop")
		}
	})
	t.Cleanup(func() {
		w.ui.Push(eventloop.Event{Kind: eventloop.EvClosed})
		select {
		case <-uiDone:
		case <-time.After(settle):
			t.Error("the UI did not stop")
		}
	})

	type bound struct {
		shm *wlcore.Shm
		err error
	}
	ch := make(chan bound, 1)
	w.loop.Post(func() {
		reg, err := w.conn.Display().GetRegistry()
		if err != nil {
			ch <- bound{err: err}
			return
		}
		var compositor *wlcore.Compositor
		var shm *wlcore.Shm
		reg.SetListener(wlcore.RegistryListener{Global: func(name uint32, iface string, version uint32) {
			var err error
			switch iface {
			case "wl_shm":
				shm, err = reg.Bind(name, version, wlcore.ShmInterface)
			case "wl_compositor":
				compositor, err = reg.Bind(name, version, wlcore.CompositorInterface)
			}
			if err != nil {
				ch <- bound{err: err}
			}
		}})
		// The registry's globals are all in by the time the round trip ends.
		if err := w.conn.Roundtrip(); err != nil {
			ch <- bound{err: err}
			return
		}
		if shm == nil || compositor == nil {
			ch <- bound{err: errMissingGlobals}
			return
		}
		if w.surface, err = compositor.CreateSurface(); err != nil {
			ch <- bound{err: err}
			return
		}
		ch <- bound{shm: shm}
	})
	var b bound
	select {
	case b = <-ch:
	case <-time.After(settle):
		t.Fatal("binding wl_shm timed out")
	}
	if b.err != nil {
		t.Fatalf("setup: %v", b.err)
	}

	w.p = newPool(b.shm, w.loop.Post, w.ui.Do, w.ui.Push)
	w.p.adopted = func() { w.adopted <- struct{}{} }
	return w
}

var errMissingGlobals = errors.New("the fake did not announce wl_shm and wl_compositor")

// onUI runs fn on the UI goroutine and waits for it.
func (w *fakeWindow) onUI(fn func()) {
	w.t.Helper()
	done := make(chan struct{})
	w.ui.Do(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(settle):
		w.t.Fatal("the UI goroutine did not run the closure")
	}
}

// waitFor takes n values from ch, and fails the test if they do not come.
func (w *fakeWindow) waitFor(ch <-chan struct{}, n int, what string) {
	w.t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(settle):
			w.t.Fatalf("timed out waiting for %s (%d of %d)", what, i, n)
		}
	}
}

// sync returns once the compositor has processed every request the Wayland
// goroutine made before the call: it is a round trip run on that goroutine.
func (w *fakeWindow) sync() {
	w.t.Helper()
	ch := make(chan error, 1)
	w.loop.Post(func() { ch <- w.conn.Roundtrip() })
	select {
	case err := <-ch:
		if err != nil {
			w.t.Fatalf("roundtrip: %v", err)
		}
	case <-time.After(settle):
		w.t.Fatal("the round trip timed out")
	}
}

// hold stops the Wayland goroutine, so everything posted from now on queues
// behind it, until the returned func is called. It makes "the pool was
// replaced while its buffers were still being created" deterministic.
func (w *fakeWindow) hold() (release func()) {
	gate := make(chan struct{})
	parked := make(chan struct{})
	w.loop.Post(func() {
		close(parked)
		<-gate
	})
	select {
	case <-parked:
	case <-time.After(settle):
		w.t.Fatal("the Wayland goroutine did not park")
	}
	return func() { close(gate) }
}

func (w *fakeWindow) checkServer() {
	w.t.Helper()
	if errs := w.srv.Errors(); len(errs) > 0 {
		w.t.Errorf("the compositor saw protocol errors: %v", errs)
	}
}

func fill(w, h int, px uint32) []uint32 {
	out := make([]uint32, w*h)
	for i := range out {
		out[i] = px
	}
	return out
}

func equalPixels(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// What the UI draws into frame.cv is what the compositor reads from the
// mapped pool: two frames, two buffers, two different contents.
func TestFakePixelsDrawnIntoTheCanvasAreWhatTheServerReads(t *testing.T) {
	w := newFakeWindow(t, wltest.Options{})
	w.onUI(func() {
		if err := w.p.ensure(64, 48); err != nil {
			t.Errorf("ensure: %v", err)
		}
	})
	w.waitFor(w.adopted, frameCount, "the buffers")

	red := canvas.Color{R: 0xff, A: 0xff}
	blue := canvas.Color{B: 0xff, A: 0xff}
	w.onUI(func() {
		w.p.frames[0].cv.Clear(red)
		w.p.frames[1].cv.Clear(blue)
	})
	w.sync()

	bufs := w.srv.Buffers()
	if len(bufs) != frameCount {
		t.Fatalf("the server has %d buffers, want %d", len(bufs), frameCount)
	}
	for i, want := range []uint32{0xffff0000, 0xff0000ff} {
		b := bufs[i]
		if b.Width != 64 || b.Height != 48 || b.Stride != 64*4 || b.Format != uint32(wlcore.ShmFormatArgb8888) || !b.Live {
			t.Errorf("buffer %d = %dx%d stride %d format %d live %v, want live 64x48 stride 256 ARGB8888",
				i, b.Width, b.Height, b.Stride, b.Format, b.Live)
		}
		if !equalPixels(b.Pixels, fill(64, 48, want)) {
			t.Errorf("buffer %d does not hold the pixels drawn into frame %d (first pixel %#08x, want %#08x)",
				i, i, b.Pixels[0], want)
		}
	}
	w.checkServer()
}

// A resize replaces the pool: the server sees buffers of the new size, and
// the ones it had are destroyed, so the live count is back to the pool size.
func TestFakeResizeGivesBuffersOfTheNewSizeAndDestroysTheOldOnes(t *testing.T) {
	w := newFakeWindow(t, wltest.Options{})
	w.onUI(func() { w.p.ensure(64, 48) })
	w.waitFor(w.adopted, frameCount, "the first buffers")

	w.onUI(func() {
		if err := w.p.ensure(80, 60); err != nil {
			t.Errorf("ensure: %v", err)
		}
	})
	w.waitFor(w.adopted, frameCount, "the resized buffers")
	w.sync()

	bufs := w.srv.Buffers()
	if len(bufs) != 2*frameCount {
		t.Fatalf("the server saw %d buffers, want %d", len(bufs), 2*frameCount)
	}
	for i, b := range bufs {
		wantW, wantH, live := int32(64), int32(48), false
		if i >= frameCount {
			wantW, wantH, live = 80, 60, true
		}
		if b.Width != wantW || b.Height != wantH || b.Live != live {
			t.Errorf("buffer %d = %dx%d live %v, want %dx%d live %v", i, b.Width, b.Height, b.Live, wantW, wantH, live)
		}
	}
	if n := w.srv.LiveBuffers(); n != frameCount {
		t.Errorf("%d live buffers, want %d", n, frameCount)
	}
	w.checkServer()
}

// The pool is replaced while the Wayland goroutine has not yet made a single
// buffer, so all of the first pool's buffers arrive for dead frames. Each must
// be destroyed on arrival: none adopted, none leaked.
func TestFakeBuffersForAReplacedPoolAreDestroyedOnArrival(t *testing.T) {
	w := newFakeWindow(t, wltest.Options{})
	release := w.hold()
	w.onUI(func() {
		w.p.ensure(64, 48)
		w.p.ensure(80, 60)
	})
	release()
	w.waitFor(w.adopted, frameCount, "the buffers of the pool that survived")
	w.sync()

	bufs := w.srv.Buffers()
	if len(bufs) != 2*frameCount {
		t.Fatalf("the server saw %d buffers, want %d (the dead pool's, made and destroyed, plus the live pool's)", len(bufs), 2*frameCount)
	}
	for i, b := range bufs {
		wantW, live := int32(64), false
		if i >= frameCount {
			wantW, live = 80, true
		}
		if b.Width != wantW || b.Live != live {
			t.Errorf("buffer %d is width %d live %v, want width %d live %v", i, b.Width, b.Live, wantW, live)
		}
	}
	if n := w.srv.LiveBuffers(); n != frameCount {
		t.Errorf("%d live buffers, want %d: a buffer leaked or was destroyed twice", n, frameCount)
	}
	select {
	case <-w.adopted:
		t.Error("a buffer for a dead frame was adopted")
	default:
	}
	w.checkServer()
}

// Until the Wayland goroutine has made the buffers, no frame is handed out.
func TestFakeNoFrameIsFreeUntilItsBufferExists(t *testing.T) {
	w := newFakeWindow(t, wltest.Options{})
	release := w.hold()
	w.onUI(func() {
		w.p.ensure(64, 48)
		if w.p.free() != nil {
			t.Error("a frame was free with its buffer still on the way")
		}
	})
	release()
	w.waitFor(w.adopted, frameCount, "the buffers")
	w.onUI(func() {
		if w.p.free() == nil {
			t.Error("no frame is free once both buffers exist")
		}
	})
}

// present commits frame f the way the window layer will: the buffer name is
// read on the UI goroutine and the requests are made on the Wayland one.
func (w *fakeWindow) present(f *frame) {
	w.t.Helper()
	var buf *wlcore.Buffer
	w.onUI(func() {
		f.busy = true
		buf = f.buf
	})
	w.loop.Post(func() {
		if err := w.surface.Attach(buf, 0, 0); err != nil {
			w.t.Errorf("attach: %v", err)
		}
		if err := w.surface.Commit(); err != nil {
			w.t.Errorf("commit: %v", err)
		}
	})
}

// The whole cycle, against a compositor that releases at once and one that
// releases at the next commit: a frame is handed out, painted, presented, busy
// until its release event arrives, and free again after. Both give the same
// result, and the compositor sees the pixels that were drawn.
func TestFakeReleaseCycleFreesTheFrameThatWasPresented(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode wltest.ReleaseMode
	}{{"immediately", wltest.ReleaseImmediately}, {"on next commit", wltest.ReleaseOnNextCommit}} {
		t.Run(mode.name, func(t *testing.T) {
			w := newFakeWindow(t, wltest.Options{ReleaseMode: mode.mode})
			w.onUI(func() { w.p.ensure(32, 24) })
			w.waitFor(w.adopted, frameCount, "the buffers")

			var first, second *frame
			w.onUI(func() {
				first = w.p.free()
				first.cv.Clear(canvas.Color{G: 0xff, A: 0xff})
			})
			w.present(first)
			select {
			case c := <-w.srv.Commits():
				if c.Width != 32 || c.Height != 24 || !equalPixels(c.Pixels, fill(32, 24, 0xff00ff00)) {
					t.Errorf("the compositor presented %dx%d starting with %#08x, want the drawn 32x24 green",
						c.Width, c.Height, c.Pixels[0])
				}
			case <-time.After(settle):
				t.Fatal("the commit never reached the compositor")
			}

			if mode.mode == wltest.ReleaseOnNextCommit {
				// Nothing releases the first buffer until another one is
				// committed, so the other frame is the only one free.
				w.onUI(func() {
					if !first.busy {
						t.Error("the frame was released before a second commit")
					}
					if second = w.p.free(); second == nil || second == first {
						t.Errorf("free = %v while %v is busy, want the other frame", second, first)
					}
				})
				w.present(second)
			}
			w.waitFor(w.released, 1, "the release")
			w.onUI(func() {
				if first.busy {
					t.Error("the frame is still busy after its release")
				}
				if w.p.free() == nil {
					t.Error("no frame is free after the release")
				}
			})
			w.checkServer()
		})
	}
}

// A buffer the Wayland goroutine could not make is reported to the owner, and
// the frame it was for can never be handed out.
func TestACreationFailureIsReportedAndTheFrameIsNeverFree(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 10); err != nil {
		t.Fatal(err)
	}
	if !s.p.usable() {
		t.Fatal("a fresh pool is not usable")
	}

	boom := errors.New("no buffer for you")
	s.p.createFailure(s.p.frames[0], boom)
	if len(s.failures) != 1 || !errors.Is(s.failures[0], boom) {
		t.Fatalf("the owner was told %v, want the failure once", s.failures)
	}
	if !s.p.frames[0].failed {
		t.Error("the frame that failed is not marked")
	}
	if !s.p.usable() {
		t.Error("one frame is left and the pool says it is not usable")
	}

	// The other frame gets its buffer; the failed one is never chosen, and does
	// not hide it.
	s.p.adopt(s.p.frames[1], &wlcore.Buffer{})
	if got := s.p.free(); got != s.p.frames[1] {
		t.Errorf("free = %v, want the frame that has a buffer", got)
	}

	s.p.createFailure(s.p.frames[1], boom)
	if s.p.usable() {
		t.Error("every frame failed and the pool says it is usable")
	}
}

// A frame of a pool that was replaced while its buffer was being made has
// nothing to do with the pool that replaced it, so its failure is not one.
func TestACreationFailureOfAReplacedFrameIsIgnored(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(100, 80); err != nil {
		t.Fatal(err)
	}
	old := s.p.frames
	if err := s.p.ensure(120, 90); err != nil {
		t.Fatal(err)
	}

	s.p.createFailure(old[0], errors.New("too late"))
	if len(s.failures) != 0 {
		t.Errorf("the owner was told about a pool that no longer exists: %v", s.failures)
	}
	if !s.p.usable() {
		t.Error("a dead frame's failure made the live pool unusable")
	}
}

// An empty pool, which is what a window has when its first size could not be
// built, and a closed one are not usable.
func TestAnEmptyOrClosedPoolIsNotUsable(t *testing.T) {
	s := newStubPool(t)
	if s.p.usable() {
		t.Error("an empty pool is usable")
	}
	if err := s.p.ensure(10, 10); err != nil {
		t.Fatal(err)
	}
	s.p.close(false)
	if s.p.usable() {
		t.Error("a closed pool is usable")
	}
}

// The real path, against the fake compositor: a wl_shm.create_pool that fails
// is reported to the UI goroutine, the frame is marked, the other one is
// untouched and comes out, and the compositor saw nothing wrong.
func TestFakeACreationFailureReachesTheUIAndSparesTheOtherFrame(t *testing.T) {
	w := newFakeWindow(t, wltest.Options{})
	failures := make(chan error, frameCount)
	w.p.failed = func(err error) { failures <- err } // on the UI goroutine, through do
	failCreates(func(n int) bool { return n == 1 })(w.p)

	w.onUI(func() {
		if err := w.p.ensure(64, 48); err != nil {
			t.Errorf("ensure: %v", err)
		}
	})
	w.waitFor(w.adopted, 1, "the buffer that could be made")
	select {
	case err := <-failures:
		if err == nil {
			t.Error("the failure carries no error")
		}
	case <-time.After(settle):
		t.Fatal("the failure never reached the UI goroutine")
	}

	w.onUI(func() {
		if !w.p.frames[0].failed || w.p.frames[1].failed {
			t.Errorf("failed flags are %v %v, want true false", w.p.frames[0].failed, w.p.frames[1].failed)
		}
		if got := w.p.free(); got != w.p.frames[1] {
			t.Errorf("free = %v, want the frame whose buffer exists", got)
		}
		if !w.p.usable() {
			t.Error("the pool with one good frame is not usable")
		}
	})
	w.sync()
	if n := w.srv.LiveBuffers(); n != 1 {
		t.Errorf("%d live buffers, want the one that was made", n)
	}
	w.checkServer()
}

// A creation that fails because the connection has been closed, by either
// side, is not the pool's failure: the window is going away, Run reports how
// the connection ended, and a Close called while the buffers are still being
// made must not turn an orderly close into an error.
func TestFakeACreationThatFailsBecauseTheConnectionIsGoneIsNotAFailure(t *testing.T) {
	w := newFakeWindow(t, wltest.Options{})
	failures := make(chan error, frameCount)
	w.p.failed = func(err error) { failures <- err }
	real := w.p.createBuffer
	ran := make(chan struct{}, frameCount)
	w.p.create = func(f *frame, fd, size int, width, height int32) {
		w.conn.Close() // on the Wayland goroutine, just before the request
		real(f, fd, size, width, height)
		ran <- struct{}{}
	}

	w.onUI(func() {
		if err := w.p.ensure(64, 48); err != nil {
			t.Errorf("ensure: %v", err)
		}
	})
	w.waitFor(ran, 1, "the creation that runs into the closed connection")
	// Everything the Wayland goroutine reported has been run by the UI once a
	// closure queued after the report has.
	w.onUI(func() {})
	w.onUI(func() {})
	select {
	case err := <-failures:
		t.Errorf("a closed connection was reported as a pool failure: %v", err)
	default:
	}
	w.onUI(func() {
		if !w.p.usable() || w.p.frames[0].failed {
			t.Error("the frame was marked failed because the connection closed")
		}
	})
}

// poison leaves cv in the state a bad argument leaves it in: canvas errors are
// sticky and there is no way back, so this is what an application's Paint
// does to the frame's canvas the first time it draws something invalid.
func poison(t *testing.T, cv *canvas.Canvas) {
	t.Helper()
	cv.FillRect(canvas.Rect{Width: -1, Height: 1}, appColor)
	if cv.Err() == nil {
		t.Fatal("an invalid rectangle did not poison the canvas: the test cannot make its point")
	}
}

// A canvas that failed stays failed, so the frame it belongs to has to get a
// new one over the same mapping: nothing else can bring it back, and the pool
// would hand the same free frame out forever.
func TestResetCanvasGivesAPoisonedFrameACleanCanvasOverTheSameMemory(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 8); err != nil {
		t.Fatal(err)
	}
	f := s.p.frames[0]
	poison(t, f.cv)
	old, data := f.cv, f.data

	if err := s.p.resetCanvas(f); err != nil {
		t.Fatalf("resetCanvas: %v", err)
	}
	if f.cv == old {
		t.Error("the frame kept the poisoned canvas")
	}
	if err := f.cv.Err(); err != nil {
		t.Errorf("the new canvas carries an error: %v", err)
	}
	if &f.data[0] != &data[0] || len(f.data) != len(data) {
		t.Error("the mapping was replaced: the new canvas must draw into the same memory")
	}
	if f.cv.Width() != 10 || f.cv.Height() != 8 || f.cv.PixelWidth() != 10 || f.cv.PixelHeight() != 8 {
		t.Errorf("the new canvas is %dx%d logical, %dx%d physical, want 10x8 both",
			f.cv.Width(), f.cv.Height(), f.cv.PixelWidth(), f.cv.PixelHeight())
	}

	// Writable, and into the mapping: what it draws is what the compositor reads.
	f.cv.Clear(appColor)
	if err := f.cv.Err(); err != nil {
		t.Fatalf("drawing on the new canvas: %v", err)
	}
	if got, want := binary.NativeEndian.Uint32(f.data[:4]), appWord(t); got != want {
		t.Errorf("the first pixel of the mapping is %#08x after a Clear, want %#08x", got, want)
	}
}

// If the canvas cannot be rebuilt the frame cannot be used any more, which the
// owner has to hear of, and it is never handed out again: the alternative is a
// loop of paint, fail, paint on the same frame.
func TestResetCanvasThatFailsMarksTheFrameFailedAndNeverFree(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 8); err != nil {
		t.Fatal(err)
	}
	s.p.adopt(s.p.frames[0], &wlcore.Buffer{})
	f := s.p.frames[0]
	if s.p.free() != f {
		t.Fatal("the frame is not free: the test cannot make its point")
	}

	s.p.scale = -1 // what canvas.New refuses
	if err := s.p.resetCanvas(f); err == nil {
		t.Fatal("resetCanvas succeeded with a scale canvas.New rejects")
	}
	if !f.failed {
		t.Error("the frame that could not be rebuilt is not marked failed")
	}
	if s.p.free() != nil {
		t.Error("a frame with no usable canvas is still handed out")
	}
}

// A dead frame has no mapping to rebuild over, and must not be resurrected.
func TestResetCanvasRefusesADeadFrame(t *testing.T) {
	s := newStubPool(t)
	if err := s.p.ensure(10, 8); err != nil {
		t.Fatal(err)
	}
	f := s.p.frames[0]
	s.p.close(false)
	if err := s.p.resetCanvas(f); err == nil {
		t.Error("resetCanvas rebuilt a dead frame")
	}
	if f.cv != nil || f.data != nil {
		t.Error("a dead frame got a canvas or a mapping back")
	}
}

// The descriptors of a pool that is being built start as "none yet", in every
// slot. A slot left at its zero value would be descriptor 0, and the cleanup of
// a half-built pool closes every slot that is not negative: it would close
// stdin. It was a literal with as many -1 as the pool had frames, which is
// right until the day frameCount changes.
func TestTheDescriptorsOfAPoolBeingBuiltStartAsNone(t *testing.T) {
	fds := noFDs()
	if len(fds) != frameCount {
		t.Fatalf("%d slots for %d frames", len(fds), frameCount)
	}
	for i, fd := range fds {
		if fd != -1 {
			t.Errorf("slot %d starts as %d, want -1: the cleanup would close it", i, fd)
		}
	}
}
