package canvas

// axisCoverage returns the length of the overlap between the span [lo, hi)
// and the unit interval [i, i+1) — the exact one-dimensional coverage of
// pixel i.
//
// For an axis-aligned rectangle the two-dimensional coverage of a pixel is
// simply the product of its two axis coverages, so rectangles are rendered
// with the true covered area rather than the distance approximation the
// curved shapes use.
func axisCoverage(lo, hi float32, i int) float32 {
	a := float32(i)
	b := a + 1
	if lo > a {
		a = lo
	}
	if hi < b {
		b = hi
	}
	if b <= a {
		return 0
	}
	return b - a
}

// FillRect fills an axis-aligned rectangle, given in logical units and
// anchored at its top-left corner, compositing source-over.
//
// Coverage is exact: fractional edges are antialiased by the true covered
// area of each pixel, not an approximation.
//
// A zero width or height is a no-op, as is a fully transparent color;
// neither writes pixels nor extends the damage. Negative dimensions and
// non-finite values are errors — see [Canvas.Err]. A rectangle that falls
// partly or entirely outside the canvas is clipped, not rejected.
func (c *Canvas) FillRect(rect Rect, color Color) {
	const op = "FillRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || color.A == 0 {
		return
	}

	// Scale the whole geometry once. Positions and sizes keep their
	// fractions all the way to rasterization: rounding them separately is
	// what produces gaps and inconsistent thicknesses.
	x0 := rect.X * c.scale
	y0 := rect.Y * c.scale
	x1 := (rect.X + rect.Width) * c.scale
	y1 := (rect.Y + rect.Height) * c.scale

	box, ok := c.clipRect(x0, y0, x1, y1)
	if !ok {
		return
	}

	src := premultiply(color)
	c.fillAxisRect(box, x0, y0, x1, y1, src)
	c.addDamage(box)
}

// fillAxisRect composites the axis-aligned physical rectangle
// [x0,x1) x [y0,y1) over the pixels of box with exact coverage. box must
// already be clipped to the visible region.
func (c *Canvas) fillAxisRect(box PixelRect, x0, y0, x1, y1 float32, src uint32) {
	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		covY := axisCoverage(y0, y1, y)
		if covY <= 0 {
			continue
		}
		row := y * c.buf.Stride
		for x := box.X; x < xEnd; x++ {
			covX := axisCoverage(x0, x1, x)
			if covX <= 0 {
				continue
			}
			c.blendPixel(row+x, src, coverage8(covX*covY))
		}
	}
}

// StrokeRect draws a border on an axis-aligned rectangle, given in logical
// units, compositing source-over.
//
// The border is drawn entirely inward: the outer bounding box is exactly
// the rectangle passed in, and thickening the border never grows the shape.
// If width exceeds half the shorter side the interior disappears and the
// result is visually a solid fill.
//
// Coverage is exact — the stroke is the set difference of two axis-aligned
// rectangles — so the corners composite once, not twice.
//
// A zero width, a zero rectangle dimension and a fully transparent color
// are all no-ops. Negative values and non-finite values are errors.
func (c *Canvas) StrokeRect(rect Rect, width float32, color Color) {
	const op = "StrokeRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || width == 0 || color.A == 0 {
		return
	}

	ox0 := rect.X * c.scale
	oy0 := rect.Y * c.scale
	ox1 := (rect.X + rect.Width) * c.scale
	oy1 := (rect.Y + rect.Height) * c.scale
	w := width * c.scale

	box, ok := c.clipRect(ox0, oy0, ox1, oy1)
	if !ok {
		return
	}

	src := premultiply(color)

	// The hole is the outer rectangle deflated by the stroke width on every
	// side. When it collapses there is nothing to subtract and the exact
	// fill path handles the whole thing.
	ix0, iy0 := ox0+w, oy0+w
	ix1, iy1 := ox1-w, oy1-w
	if !(ix1 > ix0) || !(iy1 > iy0) {
		c.fillAxisRect(box, ox0, oy0, ox1, oy1, src)
		c.addDamage(box)
		return
	}

	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		covOuterY := axisCoverage(oy0, oy1, y)
		if covOuterY <= 0 {
			continue
		}
		covInnerY := axisCoverage(iy0, iy1, y)
		row := y * c.buf.Stride
		for x := box.X; x < xEnd; x++ {
			covOuterX := axisCoverage(ox0, ox1, x)
			if covOuterX <= 0 {
				continue
			}
			// The hole is a subset of the outer rectangle, so the difference
			// is never negative.
			cov := covOuterX*covOuterY - axisCoverage(ix0, ix1, x)*covInnerY
			c.blendPixel(row+x, src, coverage8(cov))
		}
	}
	c.addDamage(box)
}
