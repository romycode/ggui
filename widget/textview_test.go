package widget

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/romycode/ggui/canvas"
)

// runeFont is the fake Font the text field is tested against: a fixed
// advance per *rune*, not per byte like the one in button_test.go, because
// a per-byte font would make "á" twice as wide as "a" and freeze that in
// the expectations of every geometry test here.
//
// It also counts what it is asked, which is how the work assertions check
// that a field holding 100.000 characters measures and draws no more of
// them than one holding 1.000.
type runeFont struct {
	advance float32
	// kern is added to the advance of two adjacent runes. A font with one
	// of these is not additive — Measure("AV") is not Measure("A") plus
	// Measure("V") — which is what catches any code that adds widths up
	// instead of measuring the substring it means.
	kern map[[2]rune]float32

	measured int // runes Measure has seen
	drawn    int // runes Draw has seen
	calls    int // Measure calls

	at   canvas.Point
	text string
	col  canvas.Color
	clip canvas.Rect
}

func newRuneFont(advance float32) *runeFont { return &runeFont{advance: advance} }

// newKernFont is the same font with one kerning pair: "AV" is three units
// narrower than an A and a V apart.
func newKernFont(advance float32) *runeFont {
	return &runeFont{advance: advance, kern: map[[2]rune]float32{{'A', 'V'}: -3}}
}

func (f *runeFont) Measure(s string) float32 {
	f.calls++
	var w float32
	prev := rune(-1)
	for _, r := range s {
		f.measured++
		if prev >= 0 {
			w += f.kern[[2]rune{prev, r}]
		}
		w += f.advance
		prev = r
	}
	return w
}

func (f *runeFont) Draw(_ *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect) {
	f.drawn += utf8.RuneCountInString(s)
	f.at, f.text, f.col, f.clip = at, s, col, clip
}

func (f *runeFont) reset() { f.measured, f.drawn, f.calls = 0, 0, 0 }

// boundaries lists every rune boundary of s, both ends included.
func boundaries(s string) []int {
	b := []int{0}
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		b = append(b, i)
	}
	return b
}

// slowFitBack is fitBack by brute force, for the searches to be compared
// against.
func slowFitBack(f Font, s string, i int, limit float32) int {
	i = floorBoundary(s, i)
	best := i
	for _, b := range boundaries(s) {
		if b <= i && measure(f, s[b:i]) <= budget(limit) && b < best {
			best = b
		}
	}
	return best
}

func slowFitFwd(f Font, s string, i int, limit float32) int {
	i = ceilBoundary(s, i)
	best := i
	for _, e := range boundaries(s) {
		if e >= i && measure(f, s[i:e]) <= budget(limit) && e > best {
			best = e
		}
	}
	return best
}

var sampleTexts = []string{
	"",
	"a",
	"hello world",
	"añañañ",
	"AVAVAVAWAY",
	"日本語のテキスト",
	"aé日𝄞bc𝄞",
	strings.Repeat("AV", 40),
	strings.Repeat("añ日𝄞", 30),
}

func TestFitBackAndFitFwdMatchBruteForce(t *testing.T) {
	for _, f := range []Font{newRuneFont(10), newKernFont(10), nil} {
		for _, s := range sampleTexts {
			for _, limit := range []float32{0, 1, 9.5, 10, 25, 100, 1e9} {
				for _, i := range boundaries(s) {
					if got, want := fitBack(f, s, i, limit), slowFitBack(f, s, i, limit); got != want {
						t.Fatalf("fitBack(%q, %d, %v) = %d, want %d", s, i, limit, got, want)
					}
					if got, want := fitFwd(f, s, i, limit), slowFitFwd(f, s, i, limit); got != want {
						t.Fatalf("fitFwd(%q, %d, %v) = %d, want %d", s, i, limit, got, want)
					}
				}
			}
		}
	}
}

// Offsets that are not boundaries, and offsets outside the text, are
// snapped rather than a panic.
func TestFitPrimitivesSnapAndClamp(t *testing.T) {
	f := newRuneFont(10)
	const s = "añ日"
	for i := -5; i <= len(s)+5; i++ {
		if got, want := fitBack(f, s, i, 15), slowFitBack(f, s, i, 15); got != want {
			t.Fatalf("fitBack(%d) = %d, want %d", i, got, want)
		}
		if got, want := fitFwd(f, s, i, 15), slowFitFwd(f, s, i, 15); got != want {
			t.Fatalf("fitFwd(%d) = %d, want %d", i, got, want)
		}
	}
}

func TestSyncIsIdempotentAndKeepsTheCaretInside(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	for _, f := range []Font{newRuneFont(10), newKernFont(10)} {
		for range 500 {
			s := sampleTexts[rng.Intn(len(sampleTexts))]
			bs := boundaries(s)
			caret := bs[rng.Intn(len(bs))]
			innerW := float32(rng.Intn(120))

			var v view
			v.anchor = bs[rng.Intn(len(bs))]
			v.sync(f, s, caret, max(innerW-2, 0), innerW)
			first := v
			v.sync(f, s, caret, max(innerW-2, 0), innerW)

			if v != first {
				t.Fatalf("sync is not idempotent on %q caret %d innerW %v: %+v then %+v", s, caret, innerW, first, v)
			}
			if v.caretX > v.viewW {
				t.Fatalf("caret at %v is outside the %v wide view (%q, caret %d)", v.caretX, v.viewW, s, caret)
			}
			if v.anchor > caret {
				t.Fatalf("anchor %d is past the caret %d", v.anchor, caret)
			}
		}
	}
}

// Rule 3: whatever the anchor was, a text that fits entirely leaves it at
// the start rather than scrolled with blank space on the right.
func TestSyncPullsTheAnchorBackWhenTheTextFits(t *testing.T) {
	f := newRuneFont(10)
	v := view{anchor: 6}
	v.sync(f, "abcdefgh", 8, 200, 202)
	if v.anchor != 0 {
		t.Fatalf("anchor = %d for a text that fits, want 0", v.anchor)
	}
}

// Deleting in the middle of a scrolled field used to leave text off the
// left edge and blank space on the right; rule 3 is what closes that hole.
func TestSyncClosesTheGapAfterDeletingInAScrolledField(t *testing.T) {
	f := newRuneFont(10)
	s := strings.Repeat("x", 20) // 200 units
	v := view{anchor: 15}
	v.sync(f, s, 20, 100, 102)
	// The last ten characters fill the 100 units: the anchor cannot be
	// further right than that.
	if v.anchor != 10 {
		t.Fatalf("anchor = %d, want 10", v.anchor)
	}
}

// The central property of the hit test: clicking exactly on a boundary
// lands on that boundary, with a font whose advances do not add up.
func TestHitLandsOnEveryVisibleBoundary(t *testing.T) {
	f := newKernFont(10)
	s := strings.Repeat("AVWañ日", 20)

	for _, caret := range []int{0, len(s) / 2, len(s)} {
		var v view
		v.sync(f, s, caret, 98, 100)
		for _, j := range boundaries(s) {
			if j < v.anchor || j > v.visEnd {
				continue
			}
			rel := measure(f, s[v.anchor:j])
			if rel > v.viewW {
				continue
			}
			if got := v.hit(f, s, rel); got != j {
				t.Fatalf("hit at the boundary %d (rel %v) = %d", j, rel, got)
			}
		}
	}
}

func TestHitClampsToTheVisibleRun(t *testing.T) {
	f := newRuneFont(10)
	s := strings.Repeat("x", 100)
	var v view
	v.sync(f, s, 0, 98, 100)

	if got := v.hit(f, s, -50); got != v.anchor {
		t.Fatalf("a click left of the area = %d, want the anchor %d", got, v.anchor)
	}
	far := v.hit(f, s, 1e6)
	if far == len(s) {
		t.Fatal("a click past the right edge jumped to the end of the text")
	}
	if far != 9 {
		t.Fatalf("a click past the right edge = %d, want the last boundary that fits, 9", far)
	}
}

func TestNilFontKeepsTheViewAtTheStart(t *testing.T) {
	var v view
	v.sync(nil, "hello world", 11, 98, 100)
	if v.anchor != 0 || v.caretX != 0 {
		t.Fatalf("view with no font = %+v, want anchor 0 and caret 0", v)
	}
}

// Rule 1: a caret moved left of the anchor drags the anchor with it.
func TestSyncDragsTheAnchorWithACaretMovedLeft(t *testing.T) {
	f := newRuneFont(10)
	s := strings.Repeat("x", 60)
	var v view
	v.sync(f, s, 60, 98, 100)
	if v.anchor == 0 {
		t.Fatal("a text far longer than the area did not scroll")
	}
	v.sync(f, s, 3, 98, 100)
	if v.anchor != 3 {
		t.Fatalf("anchor = %d after the caret moved left of it, want 3", v.anchor)
	}
	if v.caretX != 0 {
		t.Fatalf("caretX = %v with the caret on the anchor, want 0", v.caretX)
	}
}

// hostileFont answers what a Font never should, which the view has to take
// as no width at all rather than propagate into the searches.
type hostileFont struct{ w float32 }

func (f hostileFont) Measure(string) float32                                             { return f.w }
func (hostileFont) Draw(*canvas.Canvas, canvas.Point, string, canvas.Color, canvas.Rect) {}

func TestAbsurdWidthsAreTakenAsZero(t *testing.T) {
	for _, w := range []float32{-10, float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN())} {
		f := hostileFont{w: w}
		if got := measure(f, "abc"); got != 0 {
			t.Errorf("measure with a font returning %v = %v, want 0", w, got)
		}
		var v view
		v.sync(f, "hello world", 11, float32(math.NaN()), float32(math.NaN()))
		if v.anchor != 0 || v.caretX != 0 || v.viewW != 0 || v.innerW != 0 {
			t.Errorf("view with absurd widths = %+v, want everything at zero", v)
		}
		if got := v.hit(f, "hello world", -1); got != 0 {
			t.Errorf("a click left of the area = %d, want the anchor", got)
		}
		// Every boundary measures the same nothing, so there is no
		// position to tell from any other and the click lands at the end
		// of the run — which, with no widths, is the whole text.
		if got := v.hit(f, "hello world", 5); got != len("hello world") {
			t.Errorf("a click with a font that measures nothing = %d, want the end of the run", got)
		}
	}
}
