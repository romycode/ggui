package canvas

import (
	"fmt"
	"math"
)

// segmentShape is a capsule: every point within half of the stroke width of
// the segment. It is the LineCapRound shape.
type segmentShape struct {
	ax, ay  float32 // start, physical
	dx, dy  float32 // end minus start
	invLen2 float32 // 1 / (dx*dx + dy*dy), precomputed so the loop divides nothing
	half    float32 // half the stroke width
}

func (s segmentShape) signedDistance(x, y float32) float32 {
	pax := x - s.ax
	pay := y - s.ay
	// Project onto the segment and clamp: outside the ends the nearest point
	// is the endpoint itself, which is exactly what rounds the caps.
	h := clamp01((pax*s.dx + pay*s.dy) * s.invLen2)
	return hypotf(pax-s.dx*h, pay-s.dy*h) - s.half
}

func (s segmentShape) bounds() (x0, y0, x1, y1 float32) {
	bx := s.ax + s.dx
	by := s.ay + s.dy
	return min(s.ax, bx) - s.half, min(s.ay, by) - s.half,
		max(s.ax, bx) + s.half, max(s.ay, by) + s.half
}

// boxShape is a rectangle expressed in the line's own frame: one axis runs
// along the segment, the other across it. Butt and square caps differ only
// in heT — square adds half the stroke width at each end — so one shape
// serves both.
type boxShape struct {
	cx, cy   float32 // center, physical
	ux, uy   float32 // unit vector along the line
	heT, heS float32 // half extents along and across the line
}

func (s boxShape) signedDistance(x, y float32) float32 {
	dx := x - s.cx
	dy := y - s.cy
	t := dx*s.ux + dy*s.uy  // along
	u := -dx*s.uy + dy*s.ux // across
	qt := absf(t) - s.heT
	qu := absf(u) - s.heS
	return hypotf(max(qt, 0), max(qu, 0)) + min(max(qt, qu), 0)
}

func (s boxShape) bounds() (x0, y0, x1, y1 float32) {
	// Axis-aligned extent of a rotated box: project both half extents onto
	// each axis. The across-axis unit vector is (-uy, ux).
	ex := absf(s.ux)*s.heT + absf(s.uy)*s.heS
	ey := absf(s.uy)*s.heT + absf(s.ux)*s.heS
	return s.cx - ex, s.cy - ey, s.cx + ex, s.cy + ey
}

// Line draws a straight segment of the given width between two points in
// logical units, compositing source-over. The width is centered on the
// segment, half to each side.
//
// The caps behave as [LineCapButt], [LineCapSquare] and [LineCapRound]
// describe. If the two points coincide the segment has no direction, and
// each cap collapses to the shape it names: round draws a circle, square
// draws a square, and butt draws nothing at all.
//
// A zero width is a no-op, as is a fully transparent color. A negative
// width, non-finite coordinates and an unrecognized cap are errors.
//
// The cap parameter shadows the builtin of the same name; the builtin is
// not needed here, and this is the name the API contract uses.
func (c *Canvas) Line(from Point, to Point, width float32, cap LineCap, color Color) {
	const op = "Line"

	if c.failed() {
		return
	}
	if err := validPoint(op, "from", from); err != nil {
		c.fail(err)
		return
	}
	if err := validPoint(op, "to", to); err != nil {
		c.fail(err)
		return
	}
	if err := measure(op, "width", width); err != nil {
		c.fail(err)
		return
	}
	switch cap {
	case LineCapButt, LineCapSquare, LineCapRound:
	default:
		c.fail(invalidArg(op, "cap", fmt.Sprintf("unknown LineCap(%d)", uint8(cap))))
		return
	}
	if width == 0 || color.A == 0 {
		return
	}

	ax := from.X * c.scale
	ay := from.Y * c.scale
	bx := to.X * c.scale
	by := to.Y * c.scale
	half := width * c.scale / 2

	dx := bx - ax
	dy := by - ay
	len2 := dx*dx + dy*dy
	src := premultiply(color)

	if len2 == 0 {
		c.degenerateLine(ax, ay, half, cap, src)
		return
	}

	if cap == LineCapRound {
		s := segmentShape{ax: ax, ay: ay, dx: dx, dy: dy, invLen2: 1 / len2, half: half}
		if box, ok := sdfFill(c, s, src); ok {
			c.addDamage(box)
		}
		return
	}

	length := float32(math.Sqrt(float64(len2)))
	heT := length / 2
	if cap == LineCapSquare {
		heT += half
	}
	s := boxShape{
		cx:  (ax + bx) / 2,
		cy:  (ay + by) / 2,
		ux:  dx / length,
		uy:  dy / length,
		heT: heT,
		heS: half,
	}
	if box, ok := sdfFill(c, s, src); ok {
		c.addDamage(box)
	}
}

// degenerateLine handles from == to, where the segment has no direction and
// therefore no local frame to build. Each cap collapses to the shape it
// describes.
func (c *Canvas) degenerateLine(x, y, half float32, cap LineCap, src uint32) {
	switch cap {
	case LineCapButt:
		// A zero-length butt line covers no area at all.
		return

	case LineCapRound:
		if box, ok := sdfFill(c, circleShape{cx: x, cy: y, r: half}, src); ok {
			c.addDamage(box)
		}

	case LineCapSquare:
		// Axis-aligned, so this takes the exact-coverage path rather than
		// the distance approximation.
		x0, y0 := x-half, y-half
		x1, y1 := x+half, y+half
		if box, ok := c.clipRect(x0, y0, x1, y1); ok {
			c.fillAxisRect(box, x0, y0, x1, y1, src)
			c.addDamage(box)
		}
	}
}
