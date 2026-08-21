package canvas

import (
	"fmt"
	"math"
)

// Canvas draws into borrowed pixel memory. Create one with [New]; the
// buffer, the sizes and the scale are immutable for its lifetime. If any
// of them changes, discard the Canvas and build another over the new
// buffer — the object is cheap precisely because it allocates nothing.
//
// A Canvas is not safe for concurrent use.
type Canvas struct {
	buf Buffer

	// w and h are the logical size. Integers on purpose: with fractional
	// scaling this size ends up in wp_viewport.set_destination, which takes
	// int32, so a canvas 800.5 logical units wide is not expressible to a
	// compositor and must not be constructible here.
	w, h int

	// scale is physical pixels per logical unit. Finite and greater than
	// zero, integral or fractional.
	scale float32

	// err is the first error recorded by any operation. Once set it is
	// never cleared and every later operation is a no-op. See [Canvas.Err].
	err error

	// dmg is the union of the clipped physical bounding boxes of everything
	// actually written since the last reset; hasDmg says whether anything
	// has been written at all. See [Canvas.Damage].
	dmg    PixelRect
	hasDmg bool
}

// New validates the buffer description against the logical size and scale
// and returns a Canvas drawing into buffer.Pixels. The pixels are borrowed:
// New does not copy, allocate, resize or take ownership of them, and the
// caller must keep them valid and mapped for as long as the Canvas is used.
//
// width and height are the logical size in the units every drawing method
// takes; scale is physical pixels per logical unit. The physical size lives
// in buffer.Width and buffer.Height and is chosen by the platform
// integration, which owns the rounding policy — New only checks that it
// matches width and height scaled, to within one physical pixel per axis.
//
// New is the only function in the package that returns an error; drawing
// methods record theirs on the Canvas instead. See [Canvas.Err].
func New(buffer Buffer, width, height int, scale float32) (*Canvas, error) {
	const op = "New"

	if width <= 0 {
		return nil, invalidArg(op, "width", fmt.Sprintf("must be greater than zero (got %d)", width))
	}
	if height <= 0 {
		return nil, invalidArg(op, "height", fmt.Sprintf("must be greater than zero (got %d)", height))
	}
	if !isFinite32(scale) || scale <= 0 {
		return nil, invalidArg(op, "scale", fmt.Sprintf("must be finite and greater than zero (got %v)", scale))
	}
	if buffer.Width <= 0 {
		return nil, invalidArg(op, "buffer.Width", fmt.Sprintf("must be greater than zero (got %d)", buffer.Width))
	}
	if buffer.Height <= 0 {
		return nil, invalidArg(op, "buffer.Height", fmt.Sprintf("must be greater than zero (got %d)", buffer.Height))
	}
	if buffer.Stride < buffer.Width {
		return nil, invalidArg(op, "buffer.Stride", fmt.Sprintf("must be at least buffer.Width (got %d)", buffer.Stride))
	}

	// The physical size must correspond to the logical size scaled, with
	// less than one physical pixel of slack per axis. The tolerance accepts
	// whatever rounding policy the platform applied without making the
	// canvas pick one, and it does not authorize an arbitrarily oversized
	// buffer.
	if d := absf(float32(buffer.Width) - float32(width)*scale); !(d < 1) {
		return nil, invalidArg(op, "buffer.Width", fmt.Sprintf(
			"must be width*scale to within one pixel (got %d, want ~%v)", buffer.Width, float32(width)*scale))
	}
	if d := absf(float32(buffer.Height) - float32(height)*scale); !(d < 1) {
		return nil, invalidArg(op, "buffer.Height", fmt.Sprintf(
			"must be height*scale to within one pixel (got %d, want ~%v)", buffer.Height, float32(height)*scale))
	}

	required, ok := requiredLen(buffer.Width, buffer.Height, buffer.Stride)
	if !ok {
		return nil, invalidArg(op, "buffer.Stride", fmt.Sprintf(
			"buffer.Height*buffer.Stride overflows (height %d, stride %d)", buffer.Height, buffer.Stride))
	}
	if required > int64(len(buffer.Pixels)) {
		return nil, invalidArg(op, "buffer.Pixels", fmt.Sprintf(
			"buffer is too short (need %d, got %d)", required, len(buffer.Pixels)))
	}

	return &Canvas{buf: buffer, w: width, h: height, scale: scale}, nil
}

// requiredLen returns the index just past the last visible pixel,
// (height-1)*stride + width. It stops at the last visible pixel rather than
// at stride*height because no padding is needed after the final row.
//
// The result is int64 with an explicit overflow guard rather than a plain
// int multiplication: on a 64-bit platform int is 64 bits, so a buggy or
// hostile description could otherwise wrap around into a small value that
// passes the length check and lets the raster loops index out of bounds.
// ok is false when the product does not fit, which means the description
// needs more memory than any slice can hold.
func requiredLen(width, height, stride int) (int64, bool) {
	rows := int64(height - 1)
	s := int64(stride)
	if rows != 0 && s > (math.MaxInt64-int64(width))/rows {
		return 0, false
	}
	return rows*s + int64(width), true
}

// Width returns the logical width the drawing methods take coordinates in.
func (c *Canvas) Width() int { return c.w }

// Height returns the logical height the drawing methods take coordinates in.
func (c *Canvas) Height() int { return c.h }

// Scale returns physical pixels per logical unit.
func (c *Canvas) Scale() float32 { return c.scale }

// PixelWidth returns the visible physical width, the width everything is
// clipped against.
func (c *Canvas) PixelWidth() int { return c.buf.Width }

// PixelHeight returns the visible physical height, the height everything is
// clipped against.
func (c *Canvas) PixelHeight() int { return c.buf.Height }

// Stride returns the distance in uint32 elements between the start of two
// rows. It is at least PixelWidth; the difference is padding the canvas
// never touches.
func (c *Canvas) Stride() int { return c.buf.Stride }

// Pixels returns the borrowed storage, not a copy. It is a deliberate
// escape hatch: whoever writes through it takes on the guarantees the
// canvas otherwise makes on its own — stay inside the visible region,
// leave the padding alone, and only store premultiplied values. Damage
// does not record writes made this way.
func (c *Canvas) Pixels() []uint32 { return c.buf.Pixels }

// isFinite32 reports whether v is neither NaN nor an infinity.
func isFinite32(v float32) bool {
	f := float64(v)
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// absf is math.Abs for float32 without the float64 round trip.
func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
