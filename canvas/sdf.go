package canvas

import "math"

// shape is anything that can report, for a point in physical pixels, its
// signed distance to its own boundary — negative inside, positive outside —
// together with the physical bounding box that contains it.
//
// Implementations are small value types, never pointers. [sdfFill] is
// generic over the concrete type so each call site is monomorphized: the
// signedDistance calls devirtualize and the shape value stays on the stack.
// Boxing a shape into an interface value, or passing a closure instead,
// would move it to the heap and break the package's zero-allocation
// guarantee for drawing operations.
type shape interface {
	// signedDistance returns the distance from (x, y) to the boundary, in
	// physical pixels, negative inside the shape.
	signedDistance(x, y float32) float32
	// bounds returns the half-open physical box [x0,x1) x [y0,y1) that
	// contains the shape, before any antialiasing margin.
	bounds() (x0, y0, x1, y1 float32)
}

// aaMargin is how far outside its bounds a shape can still tint a pixel.
// Coverage is clamp(0.5-d, 0, 1), which reaches zero half a pixel out, so
// one whole pixel is a comfortable margin.
const aaMargin = 1

// sdfFill rasterizes s, compositing src source-over with coverage derived
// from the signed distance at each pixel center:
//
//	cov = clamp(0.5 - d, 0, 1)
//
// This estimates the covered area, it does not compute it. The error grows
// where the boundary curves sharply within a single pixel — very small
// radii — and it is the same approximation every rasterizer of this class
// makes. It is written down so nobody measures it in six months and
// believes they found a bug. Axis-aligned rectangles do not go through
// here; they get exact coverage from axisCoverage instead.
//
// It returns the clipped box it scanned and whether anything was scanned,
// so the caller can union that into the damage.
func sdfFill[S shape](c *Canvas, s S, src uint32) (PixelRect, bool) {
	x0, y0, x1, y1 := s.bounds()
	box, ok := c.clipRect(x0-aaMargin, y0-aaMargin, x1+aaMargin, y1+aaMargin)
	if !ok {
		return PixelRect{}, false
	}

	xEnd := box.X + box.Width
	yEnd := box.Y + box.Height
	for y := box.Y; y < yEnd; y++ {
		row := y * c.buf.Stride
		py := float32(y) + 0.5
		for x := box.X; x < xEnd; x++ {
			cov := 0.5 - s.signedDistance(float32(x)+0.5, py)
			if !(cov > 0) {
				continue
			}
			c.blendPixel(row+x, src, coverage8(cov))
		}
	}
	return box, true
}

// ring turns a filled shape into an inward stroke of width 2*half: the
// band running from the shape's boundary to that depth inside it. With d
// the fill's signed distance, the band is where d lies in [-2*half, 0],
// and its own signed distance is abs(d + half) - half.
//
// Strokes are always inward — applying one never grows the outer bounding
// box — which is why bounds is simply the inner shape's.
//
// Callers must not use a ring whose band would swallow the shape entirely
// (2*half at or beyond the shape's deepest point): the deepest point then
// lands exactly on the band boundary and comes out half-covered. Fill the
// shape instead; StrokeCircle and StrokeRoundedRect both do.
type ring[S shape] struct {
	inner S
	// half is half the stroke width, in physical pixels.
	half float32
}

func (r ring[S]) signedDistance(x, y float32) float32 {
	return absf(r.inner.signedDistance(x, y)+r.half) - r.half
}

func (r ring[S]) bounds() (x0, y0, x1, y1 float32) { return r.inner.bounds() }

// clamp01 constrains v to [0,1]. NaN fails both comparisons and yields 0.
func clamp01(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// hypotf is sqrt(x*x + y*y). math.Hypot's overflow-safe scaling is not
// needed here — the inputs are pixel distances — and costs several times
// more per pixel.
func hypotf(x, y float32) float32 {
	return float32(math.Sqrt(float64(x*x + y*y)))
}
