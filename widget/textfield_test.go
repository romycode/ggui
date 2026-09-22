package widget

import (
	"fmt"
	"strings"
	"testing"

	"github.com/romycode/ggui/canvas"
)

var fieldBounds = canvas.Rect{X: 100, Y: 50, Width: 200, Height: 44}

// newTestField returns a focused field 200 units wide drawn with a 10-unit
// fixed-pitch font: 200 - 2*(2+12) = 172 units of text area, 170 of which
// the caret is kept inside.
func newTestField(t *testing.T) (*TextField, *runeFont) {
	t.Helper()
	font := newRuneFont(10)
	f := NewTextField("type here", font)
	f.Bounds = fieldBounds
	f.SetFocused(true)
	return f, font
}

func TestFieldGeometry(t *testing.T) {
	f, _ := newTestField(t)
	if got, want := f.innerWidth(), float32(172); got != want {
		t.Fatalf("innerW = %v, want %v", got, want)
	}
	if got, want := f.textX(), float32(114); got != want {
		t.Fatalf("textX = %v, want %v", got, want)
	}
	f.SetText("abc")
	if got, want := f.vw.viewW, float32(170); got != want {
		t.Fatalf("viewW = %v, want %v", got, want)
	}
	if got, want := f.vw.caretX, float32(30); got != want {
		t.Fatalf("caretX = %v, want %v", got, want)
	}
}

func TestTypingScrollsAndKeepsTheCaretVisible(t *testing.T) {
	f, font := newTestField(t)
	for range 40 {
		f.Insert("x")
	}
	if f.vw.anchor == 0 {
		t.Fatal("a text far longer than the field did not scroll")
	}
	if f.vw.caretX > f.vw.viewW {
		t.Fatalf("caret at %v, outside the %v wide view", f.vw.caretX, f.vw.viewW)
	}
	if got := measure(font, f.Text()[f.vw.anchor:f.Caret()]); got != f.vw.caretX {
		t.Fatalf("caretX %v does not match the text from the anchor, %v", f.vw.caretX, got)
	}
}

func TestKeysMoveEditAndReportChanges(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("abc")

	if f.KeyDown(KeyRight) {
		t.Fatal("moving right at the end reported a change")
	}
	if !f.KeyDown(KeyLeft) {
		t.Fatal("moving left reported no change")
	}
	if f.Caret() != 2 {
		t.Fatalf("caret %d after Left, want 2", f.Caret())
	}
	if !f.KeyDown(KeyHome) || f.Caret() != 0 {
		t.Fatalf("Home left the caret at %d", f.Caret())
	}
	if f.KeyDown(KeyBackspace) {
		t.Fatal("Backspace at the start reported a change")
	}
	if !f.KeyDown(KeyDelete) || f.Text() != "bc" {
		t.Fatalf("text %q after Delete, want %q", f.Text(), "bc")
	}
	if !f.KeyDown(KeyEnd) || f.Caret() != 2 {
		t.Fatalf("End left the caret at %d", f.Caret())
	}
	if !f.KeyDown(KeyBackspace) || f.Text() != "b" {
		t.Fatalf("text %q after Backspace, want %q", f.Text(), "b")
	}
	if f.KeyDown(KeySpace) || f.KeyDown(KeyEscape) || f.KeyDown(KeyTab) || f.KeyDown(KeyNone) {
		t.Fatal("a key the field does not act on reported a change")
	}
	if f.KeyUp(KeyBackspace) {
		t.Fatal("a key release reported a change")
	}
}

func TestCallbacks(t *testing.T) {
	f, _ := newTestField(t)
	var changes, submits []string
	f.OnChange = func(s string) { changes = append(changes, s) }
	f.OnSubmit = func(s string) { submits = append(submits, s) }

	f.SetText("ab") // the program's own change: no OnChange
	f.Insert("c")
	f.KeyDown(KeyBackspace)
	f.KeyDown(KeyLeft)
	if f.KeyDown(KeyEnter) {
		t.Fatal("Enter reported a visible change")
	}
	if len(changes) != 2 || changes[0] != "abc" || changes[1] != "ab" {
		t.Fatalf("OnChange saw %q", changes)
	}
	if len(submits) != 1 || submits[0] != "ab" {
		t.Fatalf("OnSubmit saw %q", submits)
	}
}

func TestUnfocusedAndDisabledFieldsIgnoreInput(t *testing.T) {
	f, _ := newTestField(t)
	f.SetFocused(false)
	if f.Insert("x") || f.KeyDown(KeyBackspace) || f.Text() != "" {
		t.Fatal("an unfocused field took input")
	}
	if !f.SetText("abc") {
		t.Fatal("SetText on an unfocused field reported no change")
	}

	f.SetFocused(true)
	f.Disabled = true
	if f.Insert("x") || f.KeyDown(KeyDelete) {
		t.Fatal("a disabled field took input")
	}
	f.focused = false
	if f.SetFocused(true) || f.Focused() {
		t.Fatal("a disabled field took the focus")
	}
}

func TestFocusAndCaretVisibility(t *testing.T) {
	f, _ := newTestField(t)
	f.SetFocused(false)
	if f.SetCaretVisible(false) {
		t.Fatal("hiding the caret of an unfocused field reported a change")
	}
	if !f.SetFocused(true) {
		t.Fatal("focusing reported no change")
	}
	if !f.caretShown() {
		t.Fatal("gaining the focus left the caret hidden")
	}
	if f.SetFocused(true) {
		t.Fatal("focusing an already focused field reported a change")
	}
	if !f.SetCaretVisible(false) || f.caretShown() {
		t.Fatal("hiding the caret of a focused field did not report a change")
	}
	f.Style.CaretWidth = 0
	f.SetCaretVisible(true)
	if f.caretShown() {
		t.Fatal("a zero-width caret is shown")
	}
}

// Editing and moving the caret restart the blink.
func TestUserActionsShowTheCaretAgain(t *testing.T) {
	cases := []struct {
		name string
		act  func(f *TextField)
	}{
		{"typing", func(f *TextField) { f.Insert("a") }},
		{"backspace", func(f *TextField) { f.SetText("ab"); f.KeyDown(KeyBackspace) }},
		{"moving", func(f *TextField) { f.SetText("ab"); f.KeyDown(KeyLeft) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := newTestField(t)
			f.SetCaretVisible(false)
			c.act(f)
			if !f.caretShown() {
				t.Fatal("the caret is still hidden after the user acted")
			}
		})
	}
}

func TestSetTextAndSetCaretSemantics(t *testing.T) {
	f, _ := newTestField(t)
	if f.SetText("") {
		t.Fatal("setting the same empty text reported a change")
	}
	if !f.SetText("hello") {
		t.Fatal("setting new text reported no change")
	}
	if f.SetText("hello") {
		t.Fatal("setting the same text with the caret at the end reported a change")
	}
	if !f.SetCaret(2) || f.Caret() != 2 {
		t.Fatalf("SetCaret(2) left the caret at %d", f.Caret())
	}
	if f.SetCaret(2) {
		t.Fatal("SetCaret to the same place reported a change")
	}
	f.SetText("añ")
	if !f.SetCaret(2) || f.Caret() != 1 {
		t.Fatalf("SetCaret(2) inside a character left the caret at %d, want 1", f.Caret())
	}
}

// A field whose width the caller changed must re-apply the view rules on
// the next call that reads it, without being told.
func TestAWidthChangeReachesTheView(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 30))
	wide := f.vw.anchor

	f.Bounds.Width = 100
	f.ensure()
	if f.vw.anchor == wide {
		t.Fatal("narrowing the field did not move the anchor")
	}
	if f.vw.caretX > f.vw.viewW {
		t.Fatalf("caret at %v is outside the %v wide view after the resize", f.vw.caretX, f.vw.viewW)
	}
}

// A field that is focused and then disabled shows its placeholder again:
// unfocusing it has to report that, or the frame keeps the caret nobody
// can justify.
func TestDisabledFocusedEmptyFieldReportsThePlaceholderComingBack(t *testing.T) {
	f, _ := newTestField(t)
	f.Disabled = true
	if !f.SetFocused(false) {
		t.Fatal("unfocusing a disabled empty field, which brings its placeholder back, reported no change")
	}
}

func TestTextAndCaretMovesDoNotAllocate(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 60))

	if n := testing.AllocsPerRun(100, func() { _ = f.Text() }); n != 0 {
		t.Fatalf("Text allocates %v times per call, want 0", n)
	}
	if n := testing.AllocsPerRun(100, func() { f.KeyDown(KeyLeft); f.KeyDown(KeyRight) }); n != 0 {
		t.Fatalf("moving the caret allocates %v times per call, want 0", n)
	}
}

func TestInsertAllocatesAtMostOnce(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 4096))

	n := testing.AllocsPerRun(200, func() {
		f.Insert("y")
		f.KeyDown(KeyBackspace)
	})
	if n > 2 {
		t.Fatalf("an insert and a backspace allocate %v times, want at most one each", n)
	}
}

func TestClickPlacesTheCaret(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("abcdef")
	y := fieldBounds.Y + fieldBounds.Height/2

	// Boundary 3 sits 30 units right of the text origin.
	if !f.PointerDown(f.textX()+31, y) {
		t.Fatal("a click that moved the caret reported no change")
	}
	if f.Caret() != 3 {
		t.Fatalf("caret %d after a click at 31 units, want 3", f.Caret())
	}
	// 29 units round to the same boundary: nothing moved.
	if f.PointerDown(f.textX()+29, y) {
		t.Fatal("a click that moved nothing reported a change")
	}
	if f.PointerDown(5, 5) {
		t.Fatal("a click outside the field reported a change")
	}
	if f.Caret() != 3 {
		t.Fatalf("caret %d after a click outside, want it untouched", f.Caret())
	}
	f.PointerDown(fieldBounds.X+1, y)
	if f.Caret() != 0 {
		t.Fatalf("caret %d after a click on the left edge, want 0", f.Caret())
	}
	f.PointerDown(fieldBounds.X+fieldBounds.Width-1, y)
	if f.Caret() != 6 {
		t.Fatalf("caret %d after a click past the text, want 6", f.Caret())
	}
}

// A click works on an unfocused field — the application focuses and clicks
// on the same press — but never focuses it: a widget that focused itself
// would leave two of them focused at once.
func TestClickDoesNotFocusTheFieldAndWorksUnfocused(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("abcdef")
	f.SetFocused(false)

	f.PointerDown(f.textX()+31, fieldBounds.Y+10)
	if f.Focused() {
		t.Fatal("a click focused the field")
	}
	if f.Caret() != 3 {
		t.Fatalf("caret %d after a click on an unfocused field, want 3", f.Caret())
	}
	f.Disabled = true
	if f.PointerDown(f.textX()+1, fieldBounds.Y+10) {
		t.Fatal("a disabled field took a click")
	}
}

// A click past the right edge of a scrolled field lands at the end of what
// is visible and must not scroll.
func TestClickPastTheRightEdgeDoesNotScroll(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 60))
	f.SetCaret(0)
	anchor, y := f.vw.anchor, fieldBounds.Y+fieldBounds.Height/2

	f.PointerDown(fieldBounds.X+fieldBounds.Width-1, y)
	if f.vw.anchor != anchor {
		t.Fatalf("anchor moved from %d to %d on a click", anchor, f.vw.anchor)
	}
	if f.Caret() == len(f.Text()) {
		t.Fatal("a click past the right edge jumped to the end of the text")
	}
}

// Clicking a field restarts the blink, like an edit does.
func TestClickingShowsTheCaretAgain(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("ab")
	f.SetCaretVisible(false)
	f.PointerDown(fieldBounds.X+fieldBounds.Width/2, fieldBounds.Y+fieldBounds.Height/2)
	if !f.caretShown() {
		t.Fatal("the caret is still hidden after a click")
	}
}

// With a font whose advances do not add up, clicking where the caret is
// drawn must put the caret back exactly there, for every visible boundary.
func TestCaretAndClickAgreeWithAKerningFont(t *testing.T) {
	font := newKernFont(10)
	f := NewTextField("", font)
	f.Bounds = fieldBounds
	f.SetFocused(true)
	f.SetText(strings.Repeat("AVWañ", 12))
	f.SetCaret(0)

	for j := f.vw.anchor; j <= f.vw.visEnd; j++ {
		if f.SetCaret(j); f.Caret() != j {
			continue // not a character boundary
		}
		if f.vw.caretX > f.vw.viewW {
			continue
		}
		f.PointerDown(f.textX()+f.vw.caretX, fieldBounds.Y+10)
		if f.Caret() != j {
			t.Fatalf("clicking where the caret for %d is drawn landed on %d", j, f.Caret())
		}
	}
}

func TestDrawsTheVisibleRunClippedToTheTextArea(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, font := newTestField(t)
	f.SetText(strings.Repeat("x", 60))
	font.reset()

	f.Draw(cv)

	if font.text != f.Text()[f.vw.anchor:f.vw.visEnd] {
		t.Fatalf("drew %q, want the visible run", font.text)
	}
	if font.calls != 0 {
		t.Fatalf("Draw measured %d times, want none", font.calls)
	}
	want := canvas.Rect{X: f.textX(), Y: fieldBounds.Y + 2, Width: 172, Height: 40}
	if font.clip != want {
		t.Fatalf("clip %+v, want %+v", font.clip, want)
	}
	if font.at != (canvas.Point{X: f.textX(), Y: fieldBounds.Y + fieldBounds.Height/2}) {
		t.Fatalf("text anchored at %+v", font.at)
	}
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
}

func TestPlaceholderShowsOnlyWhileEmptyAndUnfocused(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, font := newTestField(t)
	f.SetFocused(false)

	f.Draw(cv)
	if font.text != "type here" {
		t.Fatalf("drew %q, want the placeholder", font.text)
	}
	f.SetFocused(true)
	font.reset()
	font.text = ""
	f.Draw(cv)
	if font.text != "" {
		t.Fatalf("a focused empty field drew %q", font.text)
	}
}

func TestFieldEachStateDrawsDifferently(t *testing.T) {
	snapshot := func(set func(f *TextField)) []uint32 {
		cv, px := newTestCanvas(t, 640, 200, 0)
		f, _ := newTestField(t)
		f.SetText("abc")
		set(f)
		f.Draw(cv)
		if err := cv.Err(); err != nil {
			t.Fatalf("canvas error: %v", err)
		}
		return px
	}
	base := snapshot(func(f *TextField) {})
	states := map[string][]uint32{
		"unfocused":  snapshot(func(f *TextField) { f.SetFocused(false) }),
		"no caret":   snapshot(func(f *TextField) { f.SetCaretVisible(false) }),
		"disabled":   snapshot(func(f *TextField) { f.Disabled = true }),
		"empty":      snapshot(func(f *TextField) { f.SetText("") }),
		"scrolled":   snapshot(func(f *TextField) { f.SetText(strings.Repeat("x", 60)) }),
		"caret home": snapshot(func(f *TextField) { f.SetCaret(0) }),
	}
	for name, px := range states {
		if equalPixels(base, px) {
			t.Errorf("%s renders the same as the resting field", name)
		}
	}
}

func equalPixels(a, b []uint32) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Nothing the field draws may land in row padding.
func TestFieldDrawStaysInsideTheVisibleRegion(t *testing.T) {
	const w, h, pad = 320, 160, 8
	cv, px := newTestCanvas(t, w, h, pad)
	for i := range px {
		px[i] = 0xdeadbeef
	}
	f, _ := newTestField(t)
	// Straddle the right edge so the field is clipped by the buffer.
	f.Bounds = canvas.Rect{X: w - 40, Y: 20, Width: 120, Height: 44}
	f.SetText("abcdef")

	f.Draw(cv)

	for y := range h {
		for x := w; x < w+pad; x++ {
			if got := px[y*(w+pad)+x]; got != 0xdeadbeef {
				t.Fatalf("padding written at row %d, column %d: %#08x", y, x, got)
			}
		}
	}
}

// A valid Bounds must never produce an invalid inner rectangle, however
// little room is left after the border and the padding.
func TestTinyFieldsStillDraw(t *testing.T) {
	sizes := []canvas.Rect{
		{X: 10, Y: 10, Width: 0, Height: 0},
		{X: 10, Y: 10, Width: 1, Height: 1},
		{X: 10, Y: 10, Width: 4, Height: 4},
		{X: 10, Y: 10, Width: 28, Height: 28},
		{X: 10, Y: 10, Width: 29, Height: 3},
	}
	for _, r := range sizes {
		cv, _ := newTestCanvas(t, 320, 160, 0)
		f, _ := newTestField(t)
		f.Bounds = r
		f.SetText("abc")
		f.Draw(cv)
		if err := cv.Err(); err != nil {
			t.Fatalf("canvas error at %+v: %v", r, err)
		}
	}
}

func TestFieldDrawAndClickDoNotAllocate(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 60))

	if n := testing.AllocsPerRun(100, func() { f.Draw(cv) }); n != 0 {
		t.Fatalf("Draw allocates %v times per call, want 0", n)
	}
	x, y := fieldBounds.X+50, fieldBounds.Y+10
	if n := testing.AllocsPerRun(100, func() { f.PointerDown(x, y) }); n != 0 {
		t.Fatalf("PointerDown allocates %v times per call, want 0", n)
	}
}

// The performance criterion, as an assertion rather than a benchmark: the
// work per event is bounded by what is on screen and does not grow with
// the text. Times are never asserted — the work is, and that does not
// depend on the machine.
func TestWorkDoesNotGrowWithTheText(t *testing.T) {
	// The field is 172 units wide with a 10-unit font: 18 runes at most,
	// counting the one that pokes out of the right edge.
	const visible = 18
	const limit = 100 * visible

	work := func(n int, act func(f *TextField)) int {
		font := newRuneFont(10)
		f := NewTextField("", font)
		f.Bounds = fieldBounds
		f.SetFocused(true)
		f.SetText(strings.Repeat("x", n))
		font.reset()
		act(f)
		return font.measured + font.drawn
	}

	cv, _ := newTestCanvas(t, 640, 200, 0)
	acts := map[string]func(f *TextField){
		"insert at the end":    func(f *TextField) { f.Insert("y") },
		"insert at the start":  func(f *TextField) { f.SetCaret(0); f.Insert("y") },
		"insert in the middle": func(f *TextField) { f.SetCaret(len(f.Text()) / 2); f.Insert("y") },
		"backspace":            func(f *TextField) { f.KeyDown(KeyBackspace) },
		"move left":            func(f *TextField) { f.KeyDown(KeyLeft) },
		"home":                 func(f *TextField) { f.KeyDown(KeyHome) },
		"end":                  func(f *TextField) { f.KeyDown(KeyHome); f.KeyDown(KeyEnd) },
		"click":                func(f *TextField) { f.PointerDown(fieldBounds.X+90, fieldBounds.Y+10) },
		"frame":                func(f *TextField) { f.Draw(cv) },
	}
	for name, act := range acts {
		small, mid, big := work(1_000, act), work(10_000, act), work(100_000, act)
		if mid > small || big > small {
			t.Errorf("%s: %d runes at 1k, %d at 10k, %d at 100k: the work grows with the text", name, small, mid, big)
		}
		if big > limit {
			t.Errorf("%s: %d runes at 100k, more than the %d the visible run allows", name, big, limit)
		}
	}
}

// A frame that changed nothing must not measure anything at all.
func TestAnUnchangedFrameMeasuresNothing(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, font := newTestField(t)
	f.SetText(strings.Repeat("x", 1000))
	f.Draw(cv)
	font.reset()

	f.Draw(cv)
	f.Draw(cv)
	if font.calls != 0 {
		t.Fatalf("two unchanged frames called Measure %d times, want none", font.calls)
	}
	if font.drawn > 40 {
		t.Fatalf("two unchanged frames drew %d runes, want about twice the visible run", font.drawn)
	}
}

// benchField is a field of n characters, focused, with the caret at the
// end, and a canvas to draw into.
func benchField(n int) (*TextField, *canvas.Canvas) {
	px := make([]uint32, 640*200)
	cv, _ := canvas.New(canvas.Buffer{Pixels: px, Width: 640, Height: 200, Stride: 640}, 640, 200, 1)
	f := NewTextField("", newRuneFont(10))
	f.Bounds = fieldBounds
	f.SetFocused(true)
	f.SetText(strings.Repeat("x", n))
	return f, cv
}

// The benchmarks are informative: what they are read for is the shape —
// flat in the length of the text — and not the absolute numbers.
func BenchmarkTextFieldDraw(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			f, cv := benchField(n)
			b.ReportAllocs()
			for b.Loop() {
				f.Draw(cv)
			}
		})
	}
}

func BenchmarkTextFieldEdit(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		for _, where := range []string{"end", "middle"} {
			b.Run(fmt.Sprintf("%s/n=%d", where, n), func(b *testing.B) {
				f, _ := benchField(n)
				if where == "middle" {
					f.SetCaret(n / 2)
				}
				b.ReportAllocs()
				for b.Loop() {
					f.Insert("y")
					f.KeyDown(KeyBackspace)
				}
			})
		}
	}
}

func BenchmarkTextFieldPointerDown(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			f, _ := benchField(n)
			x, y := fieldBounds.X+90, fieldBounds.Y+10
			b.ReportAllocs()
			for b.Loop() {
				f.PointerDown(x, y)
			}
		})
	}
}
