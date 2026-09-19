package text

import (
	"fmt"
	"image"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// subpixelBins is how many horizontal positions within a pixel a glyph is
// rasterized at.
//
// Text keeps the fraction of its x position — that is what makes letter
// spacing even instead of stepping a whole pixel at a time — and the
// rasterizer honors it, so the coverage a glyph produces depends on that
// fraction. A cache keyed on the rune alone would therefore be wrong, and
// one keyed on the exact fraction would miss almost every time: there are
// 64 of them.
//
// Quantizing to quarter pixels bounds the positioning error at an eighth of
// a pixel, well under what an eye resolves at UI sizes, and bounds the
// cache at four masks per glyph. Vertically there is nothing to quantize:
// Draw snaps the baseline to a whole pixel to keep horizontal stems crisp,
// so the vertical bias is always zero.
const subpixelBins = 4

// maxCachedGlyphs bounds the masks one scaled face keeps. A window's text
// is a few alphabets' worth; reaching this means something is drawing from
// a much larger repertoire, at which point the cache is not the thing
// making it fast.
const maxCachedGlyphs = 512

// glyphKey identifies a rasterized glyph within one scaled face.
type glyphKey struct {
	r rune
	// xbin is the horizontal subpixel bin, in [0, subpixelBins).
	xbin uint8
}

// glyph is one rasterized glyph the cache owns.
type glyph struct {
	// mask is the coverage, anchored at the origin. It is a copy: the
	// rasterizer's own buffer is valid only until the next Glyph call, so
	// a cached glyph must not point into it.
	mask *image.Alpha
	// offset places mask's top-left relative to the whole pixel the dot
	// sits on. The rasterizer's output shifts with the dot's integer part
	// and not with anything else, so this offset is the same wherever on
	// screen the glyph is drawn — which is what makes the mask reusable.
	offset image.Point
	// advance is how far the pen moves after drawing. It does not depend
	// on the subpixel bin: the outlines are unhinted, so advances are
	// linear in size and independent of position.
	advance fixed.Int26_6
}

// scaledFace is a font rasterized for one canvas scale, together with the
// glyphs drawn from it. The cache hangs off the face rather than off the
// Face so that dropping a scale drops its masks with it.
type scaledFace struct {
	face   font.Face
	glyphs map[glyphKey]glyph
}

func newScaledFace(face font.Face) *scaledFace {
	return &scaledFace{face: face, glyphs: make(map[glyphKey]glyph)}
}

// glyphAt returns the glyph for r at dot, rasterizing and caching it on a
// miss. org is the whole pixel the dot sits on, which is what the returned
// glyph's offset is relative to.
func (sf *scaledFace) glyphAt(dot fixed.Point26_6, r rune) (glyph, image.Point, error) {
	org := image.Pt(dot.X.Floor(), dot.Y.Floor())

	// The fraction left over after the whole pixel, quantized. frac is in
	// [0, 64), so bin lands in [0, subpixelBins).
	frac := dot.X - fixed.I(org.X)
	bin := uint8(int(frac) * subpixelBins / 64)

	key := glyphKey{r: r, xbin: bin}
	if g, ok := sf.glyphs[key]; ok {
		return g, org, nil
	}

	// Rasterize at the bin's own position rather than the exact one, so
	// that every hit on this key is a glyph drawn where this mask says.
	quantized := fixed.Point26_6{
		X: fixed.I(org.X) + fixed.Int26_6(int(bin)*64/subpixelBins),
		Y: fixed.I(org.Y),
	}

	// ok is false for the fallback glyph, which is still worth drawing and
	// still worth caching — an unknown rune is usually not alone.
	dr, mask, maskp, advance, _ := sf.face.Glyph(quantized, r)
	alpha, isAlpha := mask.(*image.Alpha)
	if !isAlpha {
		return glyph{}, org, fmt.Errorf("text: rasterizer returned a %T mask, want *image.Alpha", mask)
	}

	g := glyph{
		mask:    copyMask(alpha, maskp, dr.Size()),
		offset:  dr.Min.Sub(org),
		advance: advance,
	}

	// Clearing outright rather than evicting one entry: picking a victim
	// well needs use counts this has no reason to keep, and the bound is
	// set where reaching it already means the working set is not what this
	// cache was built for.
	if len(sf.glyphs) >= maxCachedGlyphs {
		clear(sf.glyphs)
	}
	sf.glyphs[key] = g
	return g, org, nil
}

// copyMask copies the size-sized region of src starting at maskp into a
// mask anchored at the origin.
//
// The source region is intersected with what src actually holds, so a face
// that reports a rectangle larger than its mask yields transparent pixels
// rather than a read past the end. For the rasterizer this package uses the
// two always coincide.
func copyMask(src *image.Alpha, maskp image.Point, size image.Point) *image.Alpha {
	dst := image.NewAlpha(image.Rectangle{Max: image.Pt(max(size.X, 0), max(size.Y, 0))})
	if dst.Rect.Empty() {
		return dst
	}

	have := image.Rectangle{Min: maskp, Max: maskp.Add(size)}.Intersect(src.Rect)
	for y := have.Min.Y; y < have.Max.Y; y++ {
		from := src.PixOffset(have.Min.X, y)
		to := (y - maskp.Y) * dst.Stride
		copy(dst.Pix[to+have.Min.X-maskp.X:], src.Pix[from:from+have.Dx()])
	}
	return dst
}
