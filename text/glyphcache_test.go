package text

import (
	"image"
	"testing"

	"golang.org/x/image/math/fixed"

	"github.com/romycode/ggui/canvas"
)

// drawInto renders s with f and returns the visible pixels.
func drawInto(t *testing.T, f *Face, s string, x, y float32, scale float32) []uint32 {
	t.Helper()

	cv, px, stride := newTestCanvas(t, 220, 40, scale, black)
	f.Draw(cv, canvas.Point{X: x, Y: y}, s, white, canvas.Rect{Width: 220, Height: 40})
	if err := f.Err(); err != nil {
		t.Fatalf("Face error: %v", err)
	}
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}

	pw, ph := int(220*scale), int(40*scale)
	out := make([]uint32, 0, pw*ph)
	for y := range ph {
		out = append(out, px[y*stride:y*stride+pw]...)
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

// The cache must be invisible: what a Face draws cannot depend on what it
// drew before. This is what catches a key collision, a stale offset, or a
// mask cached from the wrong subpixel bin.
func TestACachedFaceDrawsWhatAFreshOneDraws(t *testing.T) {
	cases := []struct {
		name string
		s    string
		x    float32
	}{
		{"whole pixel", "Hamburgefonstiv", 10},
		{"quarter", "Hamburgefonstiv", 10.25},
		{"half", "Hamburgefonstiv", 10.5},
		{"three quarters", "Hamburgefonstiv", 10.75},
		{"an eighth, between bins", "Hamburgefonstiv", 10.125},
		{"repeated letters", "aaa bbb aaa", 12.3},
		{"accents", "àéîõü çñ", 9.7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := newTestFace(t, 16)
			want := drawInto(t, fresh, tc.s, tc.x, 20, 1)

			// A face that has already drawn a lot of other text, at other
			// positions, must still agree pixel for pixel.
			warm := newTestFace(t, 16)
			for _, prior := range []string{"zzz", "Hamburgefonstiv", "different text", tc.s} {
				for _, x := range []float32{3, 7.5, 11.25, 40.75} {
					drawInto(t, warm, prior, x, 20, 1)
				}
			}
			got := drawInto(t, warm, tc.s, tc.x, 20, 1)

			if !equalPixels(got, want) {
				t.Error("a warm face drew something different from a fresh one")
			}
		})
	}
}

// The point of the cache: after the first frame, redrawing a label
// rasterizes nothing and allocates nothing.
func TestWarmDrawDoesNotAllocate(t *testing.T) {
	f := newTestFace(t, 16)
	cv, _, _ := newTestCanvas(t, 220, 40, 1, black)
	clip := canvas.Rect{Width: 220, Height: 40}
	at := canvas.Point{X: 10.5, Y: 20}

	draw := func() { f.Draw(cv, at, "Hamburgefonstiv", white, clip) }
	draw() // warm the cache

	if n := testing.AllocsPerRun(50, draw); n != 0 {
		t.Fatalf("a warm Draw allocates %v times per call, want 0", n)
	}
	if err := f.Err(); err != nil {
		t.Fatalf("Face error: %v", err)
	}
}

func TestGlyphsAreCachedPerSubpixelBin(t *testing.T) {
	f := newTestFace(t, 16)
	sf, err := f.faceAt(1)
	if err != nil {
		t.Fatalf("faceAt: %v", err)
	}

	// One rune at each of the four bins.
	for bin := range subpixelBins {
		dot := fixed.Point26_6{
			X: fixed.I(10) + fixed.Int26_6(bin*64/subpixelBins),
			Y: fixed.I(20),
		}
		if _, _, err := sf.glyphAt(dot, 'n'); err != nil {
			t.Fatalf("glyphAt: %v", err)
		}
	}
	if len(sf.glyphs) != subpixelBins {
		t.Fatalf("cache holds %d masks for one rune, want %d (one per bin)", len(sf.glyphs), subpixelBins)
	}

	// Positions inside the same bin must share a mask, not add one.
	for _, frac := range []int{0, 5, 15} {
		dot := fixed.Point26_6{X: fixed.I(30) + fixed.Int26_6(frac), Y: fixed.I(20)}
		if _, _, err := sf.glyphAt(dot, 'n'); err != nil {
			t.Fatalf("glyphAt: %v", err)
		}
	}
	if len(sf.glyphs) != subpixelBins {
		t.Fatalf("cache grew to %d: positions within one bin did not share a mask", len(sf.glyphs))
	}
}

// A glyph's mask is meaningless at another size, so the caches must not be
// shared between scales.
func TestGlyphsAreNotSharedBetweenScales(t *testing.T) {
	f := newTestFace(t, 16)

	one, err := f.faceAt(1)
	if err != nil {
		t.Fatalf("faceAt(1): %v", err)
	}
	two, err := f.faceAt(2)
	if err != nil {
		t.Fatalf("faceAt(2): %v", err)
	}
	if one == two {
		t.Fatal("two scales share one scaled face")
	}

	dot := fixed.Point26_6{X: fixed.I(10), Y: fixed.I(20)}
	g1, _, err := one.glyphAt(dot, 'M')
	if err != nil {
		t.Fatalf("glyphAt at scale 1: %v", err)
	}
	g2, _, err := two.glyphAt(dot, 'M')
	if err != nil {
		t.Fatalf("glyphAt at scale 2: %v", err)
	}
	if len(two.glyphs) != 1 {
		t.Fatalf("scale 2 holds %d masks, want 1: it saw scale 1's cache", len(two.glyphs))
	}
	if g1.mask.Rect.Dx() >= g2.mask.Rect.Dx() {
		t.Errorf("mask at scale 1 is %v, at scale 2 %v: the larger scale did not rasterize bigger",
			g1.mask.Rect, g2.mask.Rect)
	}
}

// Dropping a scale has to drop its masks too, or the bound on scaled faces
// would not bound the memory.
func TestEvictingAScaleDropsItsGlyphs(t *testing.T) {
	f := newTestFace(t, 16)

	first, err := f.faceAt(1)
	if err != nil {
		t.Fatalf("faceAt: %v", err)
	}
	drawInto(t, f, "Hamburgefonstiv", 10, 20, 1)
	if len(first.glyphs) == 0 {
		t.Fatal("drawing cached no glyphs")
	}

	// Past the bound, which clears every scale.
	for i := range maxScaledFaces + 1 {
		if _, err := f.faceAt(1 + float32(i)*0.125); err != nil {
			t.Fatalf("faceAt: %v", err)
		}
	}
	if len(f.scaled) > maxScaledFaces {
		t.Fatalf("holding %d scaled faces, want at most %d", len(f.scaled), maxScaledFaces)
	}
	if _, ok := f.scaled[1]; ok {
		t.Fatal("the first scale survived the eviction, so its glyphs did too")
	}
}

func TestGlyphCacheIsBounded(t *testing.T) {
	f := newTestFace(t, 16)
	sf, err := f.faceAt(1)
	if err != nil {
		t.Fatalf("faceAt: %v", err)
	}

	// More distinct runes than the bound, each at every subpixel bin.
	for r := rune(0x20); r < 0x20+rune(maxCachedGlyphs); r++ {
		for bin := range subpixelBins {
			dot := fixed.Point26_6{
				X: fixed.I(10) + fixed.Int26_6(bin*64/subpixelBins),
				Y: fixed.I(20),
			}
			if _, _, err := sf.glyphAt(dot, r); err != nil {
				t.Fatalf("glyphAt(%q): %v", r, err)
			}
		}
		if len(sf.glyphs) > maxCachedGlyphs {
			t.Fatalf("cache holds %d masks, want at most %d", len(sf.glyphs), maxCachedGlyphs)
		}
	}
}

// The rasterizer reuses one buffer for every glyph, so a cached mask that
// pointed into it would be overwritten by the next glyph on the line.
func TestCachedMasksDoNotAliasTheRasterizer(t *testing.T) {
	f := newTestFace(t, 16)
	sf, err := f.faceAt(1)
	if err != nil {
		t.Fatalf("faceAt: %v", err)
	}
	dot := fixed.Point26_6{X: fixed.I(10), Y: fixed.I(20)}

	first, _, err := sf.glyphAt(dot, 'M')
	if err != nil {
		t.Fatalf("glyphAt: %v", err)
	}
	before := make([]uint8, len(first.mask.Pix))
	copy(before, first.mask.Pix)

	// Rasterize a pile of other glyphs through the same face.
	for r := rune('a'); r <= 'z'; r++ {
		if _, _, err := sf.glyphAt(dot, r); err != nil {
			t.Fatalf("glyphAt(%q): %v", r, err)
		}
	}

	if !equalBytes(first.mask.Pix, before) {
		t.Fatal("a cached mask changed when later glyphs were rasterized")
	}
}

func equalBytes(a, b []uint8) bool {
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

// copyMask must never read past what the source holds, however the face
// describes its mask.
func TestCopyMaskClampsToWhatTheSourceHolds(t *testing.T) {
	src := image.NewAlpha(image.Rect(0, 0, 3, 2))
	for i := range src.Pix {
		src.Pix[i] = uint8(i + 1)
	}

	// Asking for more than the source has must fill the rest with zeros.
	got := copyMask(src, image.Pt(1, 0), image.Pt(4, 3))
	if got.Rect != image.Rect(0, 0, 4, 3) {
		t.Fatalf("Rect = %v, want the requested size", got.Rect)
	}
	want := []uint8{
		2, 3, 0, 0,
		5, 6, 0, 0,
		0, 0, 0, 0,
	}
	if !equalBytes(got.Pix, want) {
		t.Fatalf("Pix = %v, want %v", got.Pix, want)
	}

	// And a degenerate size is an empty mask, not a panic.
	if e := copyMask(src, image.Point{}, image.Pt(-1, 5)); !e.Rect.Empty() {
		t.Fatalf("Rect = %v for a negative size, want empty", e.Rect)
	}
}
