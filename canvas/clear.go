package canvas

// Clear replaces every visible pixel with color. It replaces rather than
// composites, so clearing with a fully transparent color yields
// 0x00000000 instead of leaving the previous contents in place.
//
// The padding between the visible width and the stride is never written.
// Clear always damages the whole visible region.
func (c *Canvas) Clear(color Color) {
	// No validation: every field of Color is a uint8, so there is no
	// argument here that can be invalid.
	if c.failed() {
		return
	}

	src := premultiply(color)
	for y := range c.buf.Height {
		row := y * c.buf.Stride
		line := c.buf.Pixels[row : row+c.buf.Width]
		for i := range line {
			line[i] = src
		}
	}
	c.addDamage(PixelRect{X: 0, Y: 0, Width: c.buf.Width, Height: c.buf.Height})
}

// ClearRect replaces the pixels under an axis-aligned rectangle, given in
// logical units, with color. Like [Canvas.Clear] it replaces rather than
// composites: a fully transparent color erases to 0x00000000, which is why
// this is not the no-op that a transparent [Canvas.FillRect] is.
//
// Pixels fully inside the rectangle are replaced outright. At a subpixel
// boundary the old value and the clear color are mixed linearly by
// coverage — an interpolation, deliberately not a source-over.
//
// A zero width or height is a no-op. Negative dimensions and non-finite
// values are errors. The rectangle is clipped to the canvas.
func (c *Canvas) ClearRect(rect Rect, color Color) {
	const op = "ClearRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 {
		return
	}

	x0 := rect.X * c.scale
	y0 := rect.Y * c.scale
	x1 := (rect.X + rect.Width) * c.scale
	y1 := (rect.Y + rect.Height) * c.scale

	box, ok := c.clipRect(x0, y0, x1, y1)
	if !ok {
		return
	}

	src := premultiply(color)
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
			c.replacePixel(row+x, src, coverage8(covX*covY))
		}
	}
	c.addDamage(box)
}
