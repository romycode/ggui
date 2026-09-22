package widget

import (
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
