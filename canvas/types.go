package canvas

// Buffer describes borrowed pixel memory. It is a description, not a
// renderer: Canvas reads these four fields once in New and never resizes,
// reallocates, replaces or frees the storage behind Pixels.
type Buffer struct {
	// Pixels is the borrowed storage, one ARGB8888 premultiplied pixel
	// per element. The logical value of a pixel is 0xAARRGGBB.
	Pixels []uint32
	// Width is the visible physical width in pixels.
	Width int
	// Height is the visible physical height in pixels.
	Height int
	// Stride is the distance in uint32 elements between the start of two
	// consecutive rows. It is at least Width; anything beyond Width in a
	// row is padding that no canvas operation may read or write.
	Stride int
}

// Point is a position in logical units. Fractional values are the point:
// subpixel positioning is what the antialiasing exists for.
type Point struct {
	X float32
	Y float32
}

// Rect is an axis-aligned rectangle in logical units, anchored at its
// top-left corner. Width and Height must not be negative; zero is a
// documented no-op, not an error.
type Rect struct {
	X      float32
	Y      float32
	Width  float32
	Height float32
}

// PixelRect is integer geometry in physical pixels. Only Damage returns
// one — it is deliberately a separate type from Rect so a physical
// rectangle can never be passed to a drawing method that expects logical
// units.
type PixelRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Color is a straight (non-premultiplied) 8-bit RGBA color. Canvas
// premultiplies it once per operation; callers never premultiply.
type Color struct {
	R uint8
	G uint8
	B uint8
	A uint8
}

// LineCap selects how Line terminates each end of the segment.
type LineCap uint8

const (
	// LineCapButt ends exactly at the given points.
	LineCapButt LineCap = iota
	// LineCapSquare extends half the stroke width past each end.
	LineCapSquare
	// LineCapRound ends with a semicircle of radius half the stroke width.
	LineCapRound
)
