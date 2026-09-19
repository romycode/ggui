package eventloop

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/romycode/ggui/canvas"
)

func newTestCanvas(t testing.TB, w, h int) *canvas.Canvas {
	t.Helper()
	cv, err := canvas.New(canvas.Buffer{Pixels: make([]uint32, w*h), Width: w, Height: h, Stride: w}, w, h, 1)
	if err != nil {
		t.Fatalf("canvas.New: %v", err)
	}
	return cv
}

func samePixels(a, b []uint32) bool {
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

// The loader is what the window shows while the application prepares its
// real UI, so it has to draw something and it has to move: a frozen loader
// looks like a hung program.
func TestPaintLoaderDrawsAndIsAnimated(t *testing.T) {
	const w, h = 160, 120
	blank := newTestCanvas(t, w, h)
	a := newTestCanvas(t, w, h)
	b := newTestCanvas(t, w, h)

	PaintLoader(a, w, h, 0)
	PaintLoader(b, w, h, 240)

	if samePixels(a.Pixels(), blank.Pixels()) {
		t.Fatal("the loader drew nothing")
	}
	if samePixels(a.Pixels(), b.Pixels()) {
		t.Error("the loader looks the same at t=0 and t=240ms: it is not animated")
	}
	for _, cv := range []*canvas.Canvas{a, b} {
		if err := cv.Err(); err != nil {
			t.Errorf("the loader left a canvas error: %v", err)
		}
	}
}

// The same instant must always give the same frame: the loader's clock is
// the compositor's timestamp, not the wall clock, so a frame can be
// reproduced.
func TestPaintLoaderIsDeterministic(t *testing.T) {
	const w, h = 160, 120
	a := newTestCanvas(t, w, h)
	b := newTestCanvas(t, w, h)

	PaintLoader(a, w, h, 12345)
	PaintLoader(b, w, h, 12345)

	if !samePixels(a.Pixels(), b.Pixels()) {
		t.Error("two frames at the same t differ")
	}
}

// Drawing paths are allocation-free and that is asserted, not measured. The
// loader runs on every frame callback for as long as the application takes
// to start, so an allocation here is one per frame.
func TestPaintLoaderDoesNotAllocate(t *testing.T) {
	const w, h = 160, 120
	cv := newTestCanvas(t, w, h)

	var tick uint32
	allocs := testing.AllocsPerRun(100, func() {
		tick += 16
		PaintLoader(cv, w, h, tick)
	})
	if allocs != 0 {
		t.Errorf("PaintLoader allocates %v times per frame, want 0", allocs)
	}
}

// The damage handed to the compositor must stay inside the surface.
func TestPaintLoaderDamageStaysInsideTheCanvas(t *testing.T) {
	const w, h = 160, 120
	cv := newTestCanvas(t, w, h)
	PaintLoader(cv, w, h, 77)

	d, ok := cv.Damage()
	if !ok {
		t.Fatal("the loader reported no damage")
	}
	if d.X < 0 || d.Y < 0 || d.X+d.Width > w || d.Y+d.Height > h {
		t.Errorf("damage %+v is outside the %dx%d canvas", d, w, h)
	}
}

// A window can be configured tiny, or before its size is known. The loader
// must not turn that into a sticky canvas error that silences the real UI.
func TestPaintLoaderSurvivesTinyAndOddSizes(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {3, 7}, {8, 8}, {30, 500}, {500, 30}} {
		cv := newTestCanvas(t, size[0], size[1])
		PaintLoader(cv, float32(size[0]), float32(size[1]), 1000)
		if err := cv.Err(); err != nil {
			t.Errorf("%dx%d: %v", size[0], size[1], err)
		}
	}
}

func TestPaintFailedDrawsAndDoesNotAllocate(t *testing.T) {
	const w, h = 160, 120
	blank := newTestCanvas(t, w, h)
	cv := newTestCanvas(t, w, h)
	loader := newTestCanvas(t, w, h)
	PaintLoader(loader, w, h, 0)

	PaintFailed(cv, w, h)
	if samePixels(cv.Pixels(), blank.Pixels()) {
		t.Fatal("the failure state drew nothing")
	}
	if samePixels(cv.Pixels(), loader.Pixels()) {
		t.Error("the failure state looks like the loader")
	}
	if err := cv.Err(); err != nil {
		t.Errorf("canvas error: %v", err)
	}
	if allocs := testing.AllocsPerRun(100, func() { PaintFailed(cv, w, h) }); allocs != 0 {
		t.Errorf("PaintFailed allocates %v times, want 0", allocs)
	}
}

// The whole point of the loader is that it can be on screen before anything
// else is ready, and fonts are the slow thing. So the file that draws it may
// import canvas and math and nothing else: not text, not widget, not
// anything the application loads.
func TestLoaderImportsNothingTheApplicationLoads(t *testing.T) {
	allowed := map[string]bool{
		"math":                            true,
		"github.com/romycode/ggui/canvas": true,
	}
	f, err := parser.ParseFile(token.NewFileSet(), "loader.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !allowed[path] {
			t.Errorf("loader.go imports %q: the loader must not depend on anything the application loads", path)
		}
	}
}
