package widget

import (
	"math"
	"unicode/utf8"
)

// view is the scrolled window onto a text: which character is the first
// visible one, and the measurements a frame needs. Everything here is
// relative to that character — the anchor — and nothing is ever measured
// from the start of the text, which is what keeps the work per event
// bounded by what is on screen instead of by how much has been typed.
type view struct {
	// anchor is the byte offset of the first visible character, on a rune
	// boundary. The visible run is drawn from the fixed origin of the text
	// area, so scrolling moves by whole characters and never by a fraction
	// of one.
	anchor int
	// caretX is the caret's offset from that origin, the width of
	// text[anchor:caret].
	caretX float32
	// visEnd is the first boundary past the text area, so text[anchor:visEnd]
	// includes the character that pokes out of the right edge and is clipped.
	visEnd int
	// viewW is the width the caret is kept inside: the text area less the
	// caret's own width, so a caret at the end of the run still fits.
	viewW float32
	// innerW is the text area's width, which is also the clip handed to
	// [Font.Draw]. It is cached to notice a Bounds the caller resized.
	innerW float32
}

// measure is [Font.Measure] with the answers a Font is not trusted to get
// right taken out: no font at all, and a width that is negative or not a
// number, measure zero.
func measure(f Font, s string) float32 {
	if f == nil || s == "" {
		return 0
	}
	w := f.Measure(s)
	if !(w >= 0) || math.IsInf(float64(w), 1) {
		return 0
	}
	return w
}

// budget clamps a width to something the searches can work with: anything
// negative or not a number is no room at all, and +Inf is clamped the same
// way [measure] clamps a Font's answer, so the two cannot disagree about
// what an unbounded width means.
func budget(w float32) float32 {
	if !(w >= 0) || math.IsInf(float64(w), 1) {
		return 0
	}
	return w
}

// floorBoundary returns the rune boundary at or before i.
func floorBoundary(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// ceilBoundary returns the rune boundary at or after i.
func ceilBoundary(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

// nextBoundary returns the boundary after i, which must be one itself.
func nextBoundary(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	if i < 0 {
		i = 0
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return i + size
}

// fitBack returns the leftmost rune boundary b <= i whose text up to i
// fits in limit: measure(s[b:i]) <= limit.
//
// It doubles its way back from i until it overshoots and then bisects, so
// the substrings it measures are a few times what fits in limit and never
// the whole text. Every measurement is of one substring — advances are not
// additive under kerning, so nothing here adds widths up.
func fitBack(f Font, s string, i int, limit float32) int {
	limit = budget(limit)
	i = floorBoundary(s, i)
	if i == 0 {
		return 0
	}

	// lo fits (the empty string always does), hi does not.
	lo, hi := i, -1
	for step := 1; ; step *= 2 {
		cand := floorBoundary(s, i-step)
		if measure(f, s[cand:i]) <= limit {
			lo = cand
			if cand == 0 {
				return 0
			}
			continue
		}
		hi = cand
		break
	}

	for nextBoundary(s, hi) < lo {
		mid := floorBoundary(s, hi+(lo-hi)/2)
		if mid <= hi || mid >= lo {
			mid = nextBoundary(s, hi)
		}
		if measure(f, s[mid:i]) <= limit {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// fitFwd is fitBack the other way round: the rightmost rune boundary
// e >= i whose text from i fits in limit, measure(s[i:e]) <= limit.
func fitFwd(f Font, s string, i int, limit float32) int {
	limit = budget(limit)
	i = ceilBoundary(s, i)
	if i >= len(s) {
		return len(s)
	}

	// fit fits, over does not.
	fit, over := i, -1
	for step := 1; ; step *= 2 {
		cand := ceilBoundary(s, i+step)
		if measure(f, s[i:cand]) <= limit {
			fit = cand
			if cand == len(s) {
				return len(s)
			}
			continue
		}
		over = cand
		break
	}

	for nextBoundary(s, fit) < over {
		mid := ceilBoundary(s, fit+(over-fit)/2)
		if mid <= fit || mid >= over {
			mid = nextBoundary(s, fit)
		}
		if measure(f, s[i:mid]) <= limit {
			fit = mid
		} else {
			over = mid
		}
	}
	return fit
}

// sync applies the view rules after any change to the text, the caret or
// the width, and recomputes what a frame draws. It is idempotent: running
// it twice over the same state gives the same view.
func (v *view) sync(f Font, text string, caret int, viewW, innerW float32) {
	v.viewW, v.innerW = budget(viewW), budget(innerW)
	caret = floorBoundary(text, caret)

	a := floorBoundary(text, min(max(v.anchor, 0), len(text)))
	// fill is the rightmost the anchor may ever be: put it further and the
	// text would not reach the right edge. With the caret at the end — the
	// common case, typing — it is also what rule 2 asks for, so the two
	// rules share the one search.
	fill := fitBack(f, text, len(text), v.viewW)

	// 1. The caret is left of the anchor: it drags the anchor with it.
	if caret < a {
		a = caret
	}
	// 2. The caret is past the right edge: pull the anchor forward just
	//    enough to bring it back in.
	b := fill
	if caret != len(text) {
		b = fitBack(f, text, caret, v.viewW)
	}
	if b > a {
		a = b
	}
	// 3. No gap at the right: if everything from the anchor fits, pull the
	//    anchor back until the area is full. This is what closes the hole
	//    left by deleting in the middle of a scrolled field, and what puts
	//    a text shorter than the area back at anchor 0.
	if fill < a {
		a = fill
	}

	v.anchor = a
	v.caretX = measure(f, text[a:caret])
	v.visEnd = nextBoundary(text, fitFwd(f, text, a, v.innerW))
}

// hit returns the character boundary a click rel units right of the text
// area's origin lands on. It is the nearest boundary of the visible run,
// with ties going left, and it never leaves that run: a click past the
// last visible character lands at the end of what is on screen rather than
// at the end of the text, so clicking does not scroll.
func (v *view) hit(f Font, text string, rel float32) int {
	if !(rel > 0) {
		return v.anchor
	}
	if rel > v.viewW {
		rel = v.viewW
	}

	left := fitFwd(f, text, v.anchor, rel)
	right := nextBoundary(text, left)
	if right <= left || right > len(text) {
		return left
	}
	wr := measure(f, text[v.anchor:right])
	if wr > v.viewW {
		return left
	}
	if wr-rel < rel-measure(f, text[v.anchor:left]) {
		return right
	}
	return left
}
