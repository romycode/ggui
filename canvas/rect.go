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
