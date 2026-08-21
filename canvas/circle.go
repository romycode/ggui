package canvas

// circleShape is a disc in physical pixels.
type circleShape struct {
	cx, cy float32
	r      float32
}

func (s circleShape) signedDistance(x, y float32) float32 {
	return hypotf(x-s.cx, y-s.cy) - s.r
}

func (s circleShape) bounds() (x0, y0, x1, y1 float32) {
	return s.cx - s.r, s.cy - s.r, s.cx + s.r, s.cy + s.r
}

// FillCircle fills a disc given by its center and radius in logical units,
// compositing source-over.
//
// Coverage is approximated from the distance to the boundary rather than
// computed exactly; see the note on [sdfFill]. The approximation is least
// accurate at very small radii.
//
// A zero radius is a no-op, as is a fully transparent color. A negative
// radius and non-finite coordinates are errors. A circle outside the canvas
// is clipped, not rejected.
func (c *Canvas) FillCircle(center Point, radius float32, color Color) {
	const op = "FillCircle"

	if c.failed() {
		return
	}
	if err := validPoint(op, "center", center); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if radius == 0 || color.A == 0 {
		return
	}

	s := circleShape{cx: center.X * c.scale, cy: center.Y * c.scale, r: radius * c.scale}
	if box, ok := sdfFill(c, s, premultiply(color)); ok {
		c.addDamage(box)
	}
}

// StrokeCircle draws a ring on the disc given by center and radius, in
// logical units, compositing source-over.
//
// The stroke goes entirely inward, so the outer edge stays at radius no
// matter how thick it gets. A width at or beyond the radius closes the hole
// and the result is a solid disc.
//
// A zero radius or zero width is a no-op, as is a fully transparent color.
// Negative values and non-finite coordinates are errors.
func (c *Canvas) StrokeCircle(center Point, radius, width float32, color Color) {
	const op = "StrokeCircle"

	if c.failed() {
		return
	}
	if err := validPoint(op, "center", center); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	if radius == 0 || width == 0 || color.A == 0 {
		return
	}

	r := radius * c.scale
	w := width * c.scale
	disc := circleShape{cx: center.X * c.scale, cy: center.Y * c.scale, r: r}
	src := premultiply(color)

	// A band at or beyond the radius leaves no hole. Fill the disc outright
	// rather than clamping the ring: a ring exactly that deep puts its
	// deepest point on the band boundary, which would render the very center
	// at half coverage.
	if w >= r {
		if box, ok := sdfFill(c, disc, src); ok {
			c.addDamage(box)
		}
		return
	}

	if box, ok := sdfFill(c, ring[circleShape]{inner: disc, half: w / 2}, src); ok {
		c.addDamage(box)
	}
}
