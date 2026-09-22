package widget

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// checkInvariants asserts the three the editor promises after every
// operation: valid UTF-8, a caret inside the text, and a caret on a
// character boundary. The cached string and the buffer must also agree, or
// Text and Draw would show something the editor does not hold.
func checkInvariants(t *testing.T, e *editor, what string) {
	t.Helper()
	if !utf8.Valid(e.buf) {
		t.Fatalf("%s: buffer is not valid UTF-8: %q", what, e.buf)
	}
	if e.caret < 0 || e.caret > len(e.buf) {
		t.Fatalf("%s: caret %d outside 0..%d", what, e.caret, len(e.buf))
	}
	if e.caret < len(e.buf) && !utf8.RuneStart(e.buf[e.caret]) {
		t.Fatalf("%s: caret %d is in the middle of a character", what, e.caret)
	}
	if e.text != string(e.buf) {
		t.Fatalf("%s: cached text %q does not match the buffer %q", what, e.text, e.buf)
	}
}

func TestEditorInsertAndDelete(t *testing.T) {
	cases := []struct {
		name  string
		do    func(e *editor)
		text  string
		caret int
	}{
		{"insert ascii", func(e *editor) { e.insert("abc") }, "abc", 3},
		{"insert two bytes", func(e *editor) { e.insert("ñ") }, "ñ", 2},
		{"insert three bytes", func(e *editor) { e.insert("日") }, "日", 3},
		{"insert four bytes", func(e *editor) { e.insert("𝄞") }, "𝄞", 4},
		{"insert in the middle", func(e *editor) { e.insert("ac"); e.left(); e.insert("b") }, "abc", 2},
		{"backspace one character", func(e *editor) { e.insert("a日"); e.backspace() }, "a", 1},
		{"backspace at the start", func(e *editor) { e.insert("a"); e.home(); e.backspace() }, "a", 0},
		{"delete one character", func(e *editor) { e.insert("日a"); e.home(); e.delete() }, "a", 0},
		{"delete at the end", func(e *editor) { e.insert("a"); e.delete() }, "a", 1},
		{"control characters are dropped", func(e *editor) { e.insert("a\r\n\tb") }, "ab", 2},
		{"invalid utf-8 becomes U+FFFD", func(e *editor) { e.insert("a\xffb") }, "a�b", 5},
		{"home and end", func(e *editor) { e.insert("añ"); e.home(); e.end() }, "añ", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e editor
			c.do(&e)
			checkInvariants(t, &e, c.name)
			if e.text != c.text || e.caret != c.caret {
				t.Fatalf("text %q caret %d, want %q and %d", e.text, e.caret, c.text, c.caret)
			}
		})
	}
}

func TestEditorReportsWhetherAnythingChanged(t *testing.T) {
	var e editor
	if e.backspace() || e.delete() || e.left() || e.right() || e.home() || e.end() {
		t.Fatal("an operation on an empty editor reported a change")
	}
	if e.insert("") || e.insert("\r") {
		t.Fatal("inserting nothing reported a change")
	}
	if e.version != 0 {
		t.Fatalf("version %d after no edit, want 0", e.version)
	}
	if !e.insert("ab") {
		t.Fatal("inserting text reported no change")
	}
	if e.version != 1 {
		t.Fatalf("version %d after one edit, want 1", e.version)
	}
	if e.right() || e.end() {
		t.Fatal("moving right at the end reported a change")
	}
}

func TestEditorSetTextAndSetCaret(t *testing.T) {
	var e editor
	if !e.setText("a\x00ñ") {
		t.Fatal("setText reported no change")
	}
	if e.text != "añ" || e.caret != 3 {
		t.Fatalf("text %q caret %d after setText, want %q and 3", e.text, e.caret, "añ")
	}
	if e.setText("añ") {
		t.Fatal("setText with the same text and the caret at the end reported a change")
	}
	e.home()
	if !e.setText("añ") {
		t.Fatal("setText with the caret elsewhere reported no change")
	}
	if e.caret != 3 {
		t.Fatalf("caret %d after setText, want it at the end", e.caret)
	}

	// setCaret rounds backwards, never into the middle of a character.
	for _, c := range []struct{ in, want int }{{-5, 0}, {0, 0}, {1, 1}, {2, 1}, {3, 3}, {99, 3}} {
		e.setCaret(c.in)
		if e.caret != c.want {
			t.Fatalf("setCaret(%d) left the caret at %d, want %d", c.in, e.caret, c.want)
		}
		checkInvariants(t, &e, "setCaret")
	}
	if e.setCaret(3) {
		t.Fatal("setCaret to where the caret already is reported a change")
	}
}

// Reading the text must be free, and an edit must cost at most the one
// string the cache is rebuilt from. The buffer is grown first, because its
// amortized doubling is not what is being measured.
func TestEditorAllocations(t *testing.T) {
	var e editor
	e.setText(strings.Repeat("a", 4096))

	if n := testing.AllocsPerRun(200, func() { _ = e.text }); n != 0 {
		t.Fatalf("reading the cached text allocates %v times, want 0", n)
	}
	if n := testing.AllocsPerRun(200, func() { _ = sanitize("already clean") }); n != 0 {
		t.Fatalf("sanitizing clean text allocates %v times, want 0", n)
	}
	n := testing.AllocsPerRun(200, func() {
		e.insert("x")
		e.backspace()
	})
	if n > 2 {
		t.Fatalf("an insert and a backspace allocate %v times, want at most one each", n)
	}
}

func FuzzEditor(f *testing.F) {
	f.Add("hello", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8})
	f.Add("añ日𝄞", []byte{3, 3, 1, 2, 7, 7, 4})
	f.Add("\xff\x00ok", []byte{8, 8, 1})

	f.Fuzz(func(t *testing.T, seed string, ops []byte) {
		var e editor
		e.setText(seed)
		checkInvariants(t, &e, "setText")

		for i, op := range ops {
			switch op % 9 {
			case 0:
				e.insert("a")
			case 1:
				e.insert("ñ日𝄞")
			case 2:
				e.insert(seed)
			case 3:
				e.backspace()
			case 4:
				e.delete()
			case 5:
				e.left()
			case 6:
				e.right()
			case 7:
				e.home()
			case 8:
				e.setCaret(i)
			}
			checkInvariants(t, &e, "after op")
		}

		// Nothing the editor holds may be a control character or invalid.
		for _, r := range e.text {
			if unicode.IsControl(r) {
				t.Fatalf("a control character survived: %q", e.text)
			}
		}
	})
}
