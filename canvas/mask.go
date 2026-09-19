package canvas

import (
	"fmt"
	"image"
)

// DrawMask composites color through an 8-bit coverage mask, source-over,
// and folds what it touched into the damage. It is what a text rasterizer
// draws with: a glyph is a coverage mask and a color, which is the same
// color-plus-coverage pair per pixel every filled shape already produces,
// so this reuses the one compositor instead of adding a second.
//
// # Physical pixels, deliberately
//
// at is in physical pixels, not logical units — the one place in this
// package where that is so. A coverage mask has already been rasterized at
// the canvas's own resolution: scaling it here would resample coverage that
// was computed exactly, blurring the edges the rasterizer took care to get
// right. There is nothing to convert, so nothing is converted, and
// [PixelRect]'s reasoning applies to the argument too.
//
// The mask is positioned so that mask.Rect.Min lands on at. Everything
// outside the visible region is clipped away, row padding included.
//
// A nil mask, or one whose Pix is too short for its own Rect and Stride, is
// an error. An empty mask, a fully transparent color, and a position that
// puts the mask entirely off-screen are no-ops.
func (c *Canvas) DrawMask(at image.Point, mask *image.Alpha, color Color) {
	const op = "DrawMask"

	if c.failed() {
		return
	}
	if mask == nil {
		c.fail(invalidArg(op, "mask", "must not be nil"))
		return
	}
	if mask.Rect.Empty() || color.A == 0 {
		return
	}
	if err := validMask(op, mask); err != nil {
		c.fail(err)
		return
	}

	// Widened to int64 before the translation: at is the caller's, and a
	// position far outside int range must fall off the edge rather than
	// wrap back into the visible region. This is the same guarantee
	// floorToInt gives the logical operations.
	w, h := int64(mask.Rect.Dx()), int64(mask.Rect.Dy())
	x, y := int64(at.X), int64(at.Y)
	box, ok := c.clipPixels(x, y, x+w, y+h)
	if !ok {
		return
	}

	// Premultiplied once for the whole operation, never per pixel, like
	// every other drawing method here.
	src := premultiply(color)

	// Clipping only shrank the rectangle, so every pixel below is still
	// inside the mask: at.Y <= y < at.Y+h and at.X <= x < at.X+w. The
	// mask offset of (x, y) is therefore PixOffset(x-at.X+Rect.Min.X, …),
	// which reduces to the two subtractions here.
	for py := box.Y; py < box.Y+box.Height; py++ {
		row := py * c.buf.Stride
		maskRow := (py - at.Y) * mask.Stride
		for px := box.X; px < box.X+box.Width; px++ {
			if cov := uint32(mask.Pix[maskRow+px-at.X]); cov != 0 {
				c.blendPixel(row+px, src, cov)
			}
		}
	}
	c.addDamage(box)
}

// validMask checks that a non-empty mask can actually be indexed over its
// own Rect. image.Alpha carries Pix, Stride and Rect independently, so a
// hand-built one can describe more pixels than it holds; catching that here
// turns a panic deep in the raster loop into the package's ordinary sticky
// error.
func validMask(op string, m *image.Alpha) error {
	if m.Stride < m.Rect.Dx() {
		return invalidArg(op, "mask.Stride",
			fmt.Sprintf("must be at least mask.Rect.Dx() = %d (got %d)", m.Rect.Dx(), m.Stride))
	}
	need := (m.Rect.Dy()-1)*m.Stride + m.Rect.Dx()
	if len(m.Pix) < need {
		return invalidArg(op, "mask.Pix",
			fmt.Sprintf("must hold at least %d bytes for this Rect and Stride (got %d)", need, len(m.Pix)))
	}
	return nil
}

// clipPixels intersects an integer physical rectangle with the visible
// region, taking int64 so a caller's position cannot overflow on its way
// in. It is the [Canvas.clipRect] of the pixel-space operations: the single
// place guaranteeing that a mask loop, and the damage it reports, stay
// inside the buffer and out of the row padding.
func (c *Canvas) clipPixels(x0, y0, x1, y1 int64) (PixelRect, bool) {
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, int64(c.buf.Width)), min(y1, int64(c.buf.Height))
	if x1 <= x0 || y1 <= y0 {
		return PixelRect{}, false
	}
	return PixelRect{X: int(x0), Y: int(y0), Width: int(x1 - x0), Height: int(y1 - y0)}, true
}
