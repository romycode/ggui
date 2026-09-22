package widget

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// editor is the text model behind [TextField]: the bytes, the caret, and
// nothing about how either of them looks. It knows no font, no canvas and
// no focus, which is what lets the editing rules be tested on their own.
//
// Three invariants hold after every operation: buf is valid UTF-8, the
// caret is within it, and the caret sits on a rune boundary.
//
// The zero value is an empty editor, ready to use.
type editor struct {
	// buf is the text, in UTF-8.
	buf []byte
	// caret is an offset into buf, on a rune boundary.
	caret int
	// text is buf as a string, rebuilt at the end of each edit rather than
	// when it is read. That is what makes reading and drawing free: the
	// visible run is a substring of this one, and a lazy rebuild would put
	// its allocation in the first frame after an edit.
	text string
	// version counts the edits. Two states with the same version hold the
	// same bytes, which is what lets the repaint predicate stand for the
	// text without comparing it.
	version uint64
}

// commit republishes the cached string and counts the edit. Every method
// that changes buf ends here.
func (e *editor) commit() {
	e.text = string(e.buf)
	e.version++
}

// insert puts s at the caret, after sanitizing it, and reports whether the
// text changed. The caret ends after what was inserted.
func (e *editor) insert(s string) bool {
	s = sanitize(s)
	if s == "" {
		return false
	}
	// Grow by len(s) first, then slide the tail right and write s into the
	// hole: one growth of buf, and no temporary of its own.
	e.buf = append(e.buf, s...)
	copy(e.buf[e.caret+len(s):], e.buf[e.caret:])
	copy(e.buf[e.caret:], s)
	e.caret += len(s)
	e.commit()
	return true
}

// backspace deletes the character before the caret and reports whether it
// deleted anything.
func (e *editor) backspace() bool {
	if e.caret == 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(e.buf[:e.caret])
	e.buf = append(e.buf[:e.caret-size], e.buf[e.caret:]...)
	e.caret -= size
	e.commit()
	return true
}

// delete deletes the character after the caret and reports whether it
// deleted anything. The caret does not move.
func (e *editor) delete() bool {
	if e.caret >= len(e.buf) {
		return false
	}
	_, size := utf8.DecodeRune(e.buf[e.caret:])
	e.buf = append(e.buf[:e.caret], e.buf[e.caret+size:]...)
	e.commit()
	return true
}

// left moves the caret one character back and reports whether it moved.
func (e *editor) left() bool {
	if e.caret == 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(e.buf[:e.caret])
	e.caret -= size
	return true
}

// right moves the caret one character forward and reports whether it moved.
func (e *editor) right() bool {
	if e.caret >= len(e.buf) {
		return false
	}
	_, size := utf8.DecodeRune(e.buf[e.caret:])
	e.caret += size
	return true
}

// home moves the caret to the start and reports whether it moved.
func (e *editor) home() bool {
	if e.caret == 0 {
		return false
	}
	e.caret = 0
	return true
}

// end moves the caret to the end and reports whether it moved.
func (e *editor) end() bool {
	if e.caret == len(e.buf) {
		return false
	}
	e.caret = len(e.buf)
	return true
}

// setText replaces the text with a sanitized s and puts the caret at the
// end. It reports whether the text or the caret changed.
func (e *editor) setText(s string) bool {
	s = sanitize(s)
	if s == e.text {
		return e.end()
	}
	e.buf = append(e.buf[:0], s...)
	e.caret = len(e.buf)
	// s is already the string commit would build, so use it.
	e.text, e.version = s, e.version+1
	return true
}

// setCaret moves the caret to offset, clamped into the text and rounded
// backwards to the character boundary at or before it, and reports whether
// it moved.
func (e *editor) setCaret(offset int) bool {
	if offset < 0 {
		offset = 0
	}
	if offset > len(e.buf) {
		offset = len(e.buf)
	}
	for offset > 0 && offset < len(e.buf) && !utf8.RuneStart(e.buf[offset]) {
		offset--
	}
	if offset == e.caret {
		return false
	}
	e.caret = offset
	return true
}

// sanitize drops the control characters of s and replaces every byte that
// is not valid UTF-8 with U+FFFD.
//
// Text that is already clean is returned as it came, without building a
// string, because that is what every keystroke of a real keyboard is:
// keyboard.Event.Text carries no control characters. The public API cannot
// rely on that, though — a caller may feed it keyboard.Composer output,
// which returns "\r" for Return, or bytes of its own.
func sanitize(s string) string {
	if isClean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isClean reports whether s can be stored as it is. A real U+FFFD in the
// input answers false, like the invalid byte it cannot be told apart from
// here; sanitize then writes the same character back, so the only cost is
// one string nobody was going to notice.
func isClean(s string) bool {
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
