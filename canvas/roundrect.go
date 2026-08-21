package canvas

// roundRectShape is an axis-aligned rectangle with all four corners rounded
// by the same radius, expressed around its center in physical pixels. r is
// already clamped to min(hw, hh) by the constructor below.
type roundRectShape struct {
	cx, cy float32 // center
	hw, hh float32 // half extents
	r      float32 // corner radius
}

func (s roundRectShape) signedDistance(x, y float32) float32 {
	// Fold into the first quadrant, shrink the box by the radius, then take
	// the distance to that smaller box and subtract the radius back. The
	// min(max(...), 0) term is what makes the distance correct on the inside
	// as well as the outside.
	qx := absf(x-s.cx) - (s.hw - s.r)
	qy := absf(y-s.cy) - (s.hh - s.r)
	return hypotf(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - s.r
}

func (s roundRectShape) bounds() (x0, y0, x1, y1 float32) {
	return s.cx - s.hw, s.cy - s.hh, s.cx + s.hw, s.cy + s.hh
}

// newRoundRectShape builds the shape for a physical rectangle, clamping the
// corner radius to half the shorter side so an oversized radius yields a
// stadium instead of a self-intersecting distance field.
func newRoundRectShape(x0, y0, x1, y1, radius float32) roundRectShape {
	hw := (x1 - x0) / 2
	hh := (y1 - y0) / 2
	return roundRectShape{
		cx: x0 + hw,
		cy: y0 + hh,
		hw: hw,
		hh: hh,
		r:  min(radius, min(hw, hh)),
	}
}

// FillRoundedRect fills an axis-aligned rectangle with all four corners
// rounded by the same radius, in logical units, compositing source-over.
//
// A zero radius is valid and equivalent to [Canvas.FillRect] — including
// its exact coverage — so a theme whose corner radius is 0 needs no branch
// at the call site. A radius larger than half the shorter side is clamped
// to that maximum.
//
// A zero width or height is a no-op, as is a fully transparent color.
// Negative dimensions or radius, and non-finite values, are errors.
func (c *Canvas) FillRoundedRect(rect Rect, radius float32, color Color) {
	const op = "FillRoundedRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "radius", radius); err != nil {
		c.fail(err)
		return
	}
	if rect.Width == 0 || rect.Height == 0 || color.A == 0 {
		return
	}
	if radius == 0 {
		// Arguments are already validated, so this cannot fail; taking the
		// rectangle path also keeps the exact coverage.
		c.FillRect(rect, color)
		return
	}

	s := newRoundRectShape(
		rect.X*c.scale,
		rect.Y*c.scale,
		(rect.X+rect.Width)*c.scale,
		(rect.Y+rect.Height)*c.scale,
		radius*c.scale,
	)
	if box, ok := sdfFill(c, s, premultiply(color)); ok {
		c.addDamage(box)
	}
}

// StrokeRoundedRect draws a border on a rounded rectangle, in logical
// units, compositing source-over.
//
// Like every stroke in this package it is drawn entirely inward, so the
// outer bounding box is exactly the rectangle passed in. A width past the
// available interior closes the hole and the result is a solid fill.
//
// A zero radius is equivalent to [Canvas.StrokeRect]. A zero width, a zero
// rectangle dimension and a fully transparent color are no-ops. Negative
// values and non-finite values are errors.
func (c *Canvas) StrokeRoundedRect(rect Rect, radius, width float32, color Color) {
	const op = "StrokeRoundedRect"

	if c.failed() {
		return
	}
	if err := validRect(op, rect); err != nil {
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
	if rect.Width == 0 || rect.Height == 0 || width == 0 || color.A == 0 {
		return
	}
	if radius == 0 {
		c.StrokeRect(rect, width, color)
		return
	}

	inner := newRoundRectShape(
		rect.X*c.scale,
		rect.Y*c.scale,
		(rect.X+rect.Width)*c.scale,
		(rect.Y+rect.Height)*c.scale,
		radius*c.scale,
	)
	src := premultiply(color)

	// The shape's deepest point is min(hw, hh) inside its boundary. A band
	// that deep leaves no interior, so fill instead of stroking — the same
	// reasoning as StrokeCircle: a ring exactly that deep would render its
	// deepest point at half coverage.
	w := width * c.scale
	if w >= min(inner.hw, inner.hh) {
		if box, ok := sdfFill(c, inner, src); ok {
			c.addDamage(box)
		}
		return
	}

	if box, ok := sdfFill(c, ring[roundRectShape]{inner: inner, half: w / 2}, src); ok {
		c.addDamage(box)
	}
}
