package canvas

import "math"

// Err returns the first error any operation recorded, or nil. Drawing
// methods never return an error: they record it here and turn every later
// operation into a no-op, so a paint function calls Err once when the
// frame is finished instead of branching after each of thirty primitives.
//
// The error is permanent. There is no way to clear it, because every error
// the package can produce is a programming bug — a NaN coordinate, a
// negative radius, an unknown LineCap — and those reproduce on the first
// run rather than depending on runtime conditions. The cost is real and
// worth stating: one bad argument in the first shape leaves the rest of
// the frame unpainted, silently, until this call. Discard a poisoned
// Canvas and build another over the same buffer.
//
//	c.Clear(bg)
//	c.FillRoundedRect(box, 6, panelBG)
//	c.StrokeRect(box, 1, border)
//	if err := c.Err(); err != nil {
//		return err
//	}
func (c *Canvas) Err() error { return c.err }

// fail records err if this is the first one. Drawing methods call it and
// return immediately, before writing any pixel and without touching the
// accumulated damage.
func (c *Canvas) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

// failed reports whether the canvas is poisoned. Every drawing method
// starts with this check.
func (c *Canvas) failed() bool { return c.err != nil }

// Damage returns the union of the physical bounding boxes of everything
// written since the last [Canvas.ResetDamage], and whether anything was
// written at all. The rectangle is in physical pixels, which is exactly
// what wl_surface.damage_buffer takes — no conversion in between.
//
// Operations that write nothing do not extend it: zero alpha, a zero
// dimension, a shape entirely outside the canvas, or any call made while
// the canvas is in error.
//
// Resetting is the caller's job, after the commit, not the canvas's.
//
// Only one rectangle is accumulated, so two small changes in opposite
// corners damage everything between them. A list of rectangles with a
// merge heuristic is the natural evolution, but not before a measured case
// asks for it.
func (c *Canvas) Damage() (PixelRect, bool) {
	if !c.hasDmg {
		return PixelRect{}, false
	}
	return c.dmg, true
}

// ResetDamage forgets the accumulated region. Call it after attaching,
// damaging and committing the buffer.
//
// With a buffer pool, note that each buffer carries its own history: if
// frame N painted buffer A and frame N+1 paints buffer B, B still holds
// frame N-1, so damaging only what changed since frame N leaves B stale.
// Accumulating the last few frames' damage, or repainting the union, is
// the platform layer's decision — the canvas only reports what it touched.
func (c *Canvas) ResetDamage() {
	c.dmg = PixelRect{}
	c.hasDmg = false
}

// addDamage unions an already-clipped physical rectangle into the
// accumulator. Empty rectangles are ignored so a no-op cannot extend the
// damage.
func (c *Canvas) addDamage(r PixelRect) {
	if r.Width <= 0 || r.Height <= 0 {
		return
	}
	if !c.hasDmg {
		c.dmg = r
		c.hasDmg = true
		return
	}
	x0 := min(c.dmg.X, r.X)
	y0 := min(c.dmg.Y, r.Y)
	x1 := max(c.dmg.X+c.dmg.Width, r.X+r.Width)
	y1 := max(c.dmg.Y+c.dmg.Height, r.Y+r.Height)
	c.dmg = PixelRect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// clipRect turns a fractional physical bounding box into the half-open
// integer pixel range to scan, clipped to the visible region. It expands
// outward — floor on the low edge, ceil on the high edge — so a box that
// only grazes a pixel still includes it, and callers add their own
// antialiasing margin before calling.
//
// It returns false when nothing visible remains, which is the single place
// that guarantees no raster loop and no damage rectangle ever leaves the
// visible region.
func (c *Canvas) clipRect(x0, y0, x1, y1 float32) (PixelRect, bool) {
	// NaN fails every comparison below, so an unvalidated NaN reaching here
	// produces an empty range rather than an unbounded loop. Arguments are
	// still validated up front by each operation; this is the backstop.
	if !(x1 > x0) || !(y1 > y0) {
		return PixelRect{}, false
	}

	ix0 := floorToInt(x0)
	iy0 := floorToInt(y0)
	ix1 := ceilToInt(x1)
	iy1 := ceilToInt(y1)

	ix0 = max(ix0, 0)
	iy0 = max(iy0, 0)
	ix1 = min(ix1, c.buf.Width)
	iy1 = min(iy1, c.buf.Height)

	if ix1 <= ix0 || iy1 <= iy0 {
		return PixelRect{}, false
	}
	return PixelRect{X: ix0, Y: iy0, Width: ix1 - ix0, Height: iy1 - iy0}, true
}

// floorToInt clamps as well as floors: a coordinate far outside int range
// must saturate rather than wrap into the visible region.
func floorToInt(v float32) int {
	f := math.Floor(float64(v))
	if f < math.MinInt32 {
		return math.MinInt32
	}
	if f > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(f)
}

// ceilToInt is floorToInt's counterpart for the high edge of a range.
func ceilToInt(v float32) int {
	f := math.Ceil(float64(v))
	if f < math.MinInt32 {
		return math.MinInt32
	}
	if f > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(f)
}
