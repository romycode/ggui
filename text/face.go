package text

import (
	"errors"
	"image"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/romycode/ggui/canvas"
)

// dpi makes a face's Size mean pixels: at 72 DPI one point is one pixel.
const dpi = 72

// maxScaledFaces bounds the per-scale faces a Face keeps. A window sees a
// handful of scales in its life — one per monitor it is dragged across —
// so hitting this means something is feeding it a continuum of scales.
const maxScaledFaces = 8

// Face is a font at a size, ready to measure and draw single-line text. It
// implements widget.Font.
//
// The size is in logical units. The face is rasterized at that size times
// the scale of whichever canvas it draws to, and the rasterized faces are
// kept per scale, so drawing to a canvas at a scale it has seen before does
// no setup.
//
// Each scaled face caches the glyphs drawn from it, so a label redrawn
// every frame rasterizes its glyphs once and then only composites them. A
// warm Draw allocates nothing.
type Face struct {
	parsed *opentype.Font
	// size is the logical size in units, equal to pixels at scale 1.
	size float64
	// measure is the face at scale 1. Advances are unhinted and so linear
	// in size: measuring here and drawing at the physical size agree to
	// within the 1/64-unit quantization, and Measure needs no scale.
	measure font.Face
	// scaled are the faces used for drawing, with their glyph caches,
	// keyed by canvas scale.
	scaled map[float32]*scaledFace
	// sub is the mask header Draw hands to the canvas, reused across
	// glyphs. It describes the clipped part of a cached glyph's mask;
	// building it here rather than calling SubImage is what keeps a warm
	// Draw from allocating once per glyph.
	sub image.Alpha
	err error
}

// NewFace returns a face of f at size logical units. Release it with
// [Face.Close].
func NewFace(f *opentype.Font, size float32) (*Face, error) {
	if f == nil {
		return nil, errors.New("text: nil font")
	}
	if !(size > 0) || math.IsInf(float64(size), 0) {
		return nil, errors.New("text: face size must be positive and finite")
	}

	measure, err := newFace(f, float64(size))
	if err != nil {
		return nil, err
	}
	return &Face{
		parsed:  f,
		size:    float64(size),
		measure: measure,
		scaled:  make(map[float32]*scaledFace),
	}, nil
}

// NewSystemFace finds an installed font and returns a face of it at size
// logical units. With no families it looks for a common sans-serif, trying
// Inter, Noto Sans, DejaVu Sans, Liberation Sans and a few others in that
// order. See [Find] for how families are matched.
func NewSystemFace(size float32, style Style, families ...string) (*Face, error) {
	if len(families) == 0 {
		families = defaultFamilies
	}
	f, err := Find(style, families...)
	if err != nil {
		return nil, err
	}
	return NewFace(f, size)
}

func newFace(f *opentype.Font, px float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size: px,
		DPI:  dpi,
		// Unhinted, so advances stay linear in size; see Face.measure.
		Hinting: font.HintingNone,
	})
}

// Close releases the rasterizers and drops every cached glyph. The Face
// must not be used afterwards.
func (f *Face) Close() error {
	err := f.measure.Close()
	for s, sf := range f.scaled {
		err = errors.Join(err, sf.face.Close())
		delete(f.scaled, s)
	}
	return err
}

// Err returns the first error Draw hit, if any. Like canvas errors it is
// sticky, because Draw has no way to return one: it happens if a face
// cannot be built at a canvas's scale, or if the rasterizer hands back a
// mask this package cannot read.
func (f *Face) Err() error { return f.err }

// fail records the first error and keeps it. Later ones are dropped: the
// first is the one that explains the rest.
func (f *Face) fail(err error) {
	if f.err == nil {
		f.err = err
	}
}

// faceAt returns the face for drawing at scale, with its glyph cache.
func (f *Face) faceAt(scale float32) (*scaledFace, error) {
	if sf, ok := f.scaled[scale]; ok {
		return sf, nil
	}
	face, err := newFace(f.parsed, f.size*float64(scale))
	if err != nil {
		return nil, err
	}

	// Dropping a scale drops the glyphs rasterized for it: masks are
	// meaningless at any other size.
	if len(f.scaled) >= maxScaledFaces {
		for s, old := range f.scaled {
			old.face.Close()
			delete(f.scaled, s)
		}
	}
	sf := newScaledFace(face)
	f.scaled[scale] = sf
	return sf, nil
}

// skipped reports whether r is a control character, which neither takes
// space nor draws: a newline in a label is a caller bug, and the font's
// .notdef box for it would be worse than nothing.
func skipped(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}

// Measure returns the advance width of s in logical units, kerning
// included.
func (f *Face) Measure(s string) float32 {
	var w fixed.Int26_6
	prev := rune(-1)
	for _, r := range s {
		if skipped(r) {
			continue
		}
		if prev >= 0 {
			w += f.measure.Kern(prev, r)
		}
		adv, _ := f.measure.GlyphAdvance(r)
		w += adv
		prev = r
	}
	return float32(w) / 64
}

// Draw paints s in col with its left edge at at.X and its vertical center
// at at.Y, drawing nothing outside clip. The vertical center is that of the
// font's line box — ascent plus descent — so a label sits in the middle of
// a control whatever letters it happens to contain.
//
// Each glyph goes to the canvas as a coverage mask through
// [canvas.Canvas.DrawMask], so the text is composited by the canvas's own
// compositor and lands in [canvas.Canvas.Damage] like any other drawing. A
// rune the font lacks draws the font's fallback glyph, usually an empty
// box.
func (f *Face) Draw(cv *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect) {
	if s == "" || col.A == 0 {
		return
	}

	scale := cv.Scale()
	sf, err := f.faceAt(scale)
	if err != nil {
		f.fail(err)
		return
	}

	// Clip in physical pixels. DrawMask already keeps every write inside
	// the visible buffer; this is the caller's own narrower box, which the
	// canvas knows nothing about.
	x0, y0 := int(clip.X*scale+0.5), int(clip.Y*scale+0.5)
	x1, y1 := int((clip.X+clip.Width)*scale+0.5), int((clip.Y+clip.Height)*scale+0.5)
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, cv.PixelWidth()), min(y1, cv.PixelHeight())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	clipPx := image.Rect(x0, y0, x1, y1)

	// Put the middle of the line box on at.Y. The baseline is the box's
	// top plus the ascent, and snapping it to a whole pixel keeps the
	// horizontal stems crisp; x keeps its fraction, which the rasterizer
	// honors.
	m := sf.face.Metrics()
	centre := fixed.Int26_6(math.Round(float64(at.Y) * float64(scale) * 64))
	dot := fixed.Point26_6{
		X: fixed.Int26_6(math.Round(float64(at.X) * float64(scale) * 64)),
		Y: fixed.I((centre + (m.Ascent-m.Descent)/2).Round()),
	}

	prev := rune(-1)
	for _, r := range s {
		if skipped(r) {
			continue
		}
		if prev >= 0 {
			dot.X += sf.face.Kern(prev, r)
		}
		prev = r

		g, org, err := sf.glyphAt(dot, r)
		if err != nil {
			f.fail(err)
			return
		}
		f.drawGlyph(cv, g, org, col, clipPx)

		// The pen moves by the true advance, not by the quantized
		// position the mask was rasterized at, so the subpixel binning
		// never accumulates along the line.
		dot.X += g.advance

		// Everything from here on is past the clip; stop rather than
		// rasterizing a long string's invisible tail.
		if dot.X.Ceil() >= x1 {
			break
		}
	}
}

// drawGlyph hands one cached glyph to the canvas. org is the whole pixel
// the dot sits on, and clipPx the caller's clip in physical pixels.
//
// The clipping is done here, by cropping the mask, rather than by the
// canvas: DrawMask already keeps writes inside the buffer, but the box a
// label must stay within is the widget's, which the canvas knows nothing
// about.
func (f *Face) drawGlyph(cv *canvas.Canvas, g glyph, org image.Point, col canvas.Color, clipPx image.Rectangle) {
	// The mask is anchored at the origin, so where it lands on screen is
	// its own rectangle moved to the dot's pixel plus the glyph's offset.
	pos := org.Add(g.offset)
	area := g.mask.Rect.Add(pos).Intersect(clipPx)
	if area.Empty() {
		return
	}

	crop := area.Sub(pos)
	f.sub.Pix = g.mask.Pix[g.mask.PixOffset(crop.Min.X, crop.Min.Y):]
	f.sub.Stride = g.mask.Stride
	f.sub.Rect = crop
	cv.DrawMask(area.Min, &f.sub, col)
}
