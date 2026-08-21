package canvas

// mul8 multiplies two 8-bit values as if they were fractions of 255. It is
// the standard approximation of (a*b)/255: exact at 0 and 255, within one
// everywhere else, and free of division.
func mul8(a, b uint32) uint32 {
	t := a*b + 0x80
	return (t + (t >> 8)) >> 8
}

// premultiply converts a straight [Color] into the packed premultiplied
// 0xAARRGGBB the buffer stores. Each operation calls it once, before its
// raster loop, never per pixel.
func premultiply(c Color) uint32 {
	a := uint32(c.A)
	r := mul8(uint32(c.R), a)
	g := mul8(uint32(c.G), a)
	b := mul8(uint32(c.B), a)
	return a<<24 | r<<16 | g<<8 | b
}

// coverage8 quantizes geometric coverage in [0,1] to [0,255]. Out-of-range
// input is clamped rather than allowed to wrap: an SDF evaluated at a
// degenerate shape can produce values slightly outside the interval, and a
// wrap there would paint a bright pixel in the middle of a smooth edge.
// NaN fails both comparisons and falls through to zero.
func coverage8(cov float32) uint32 {
	if !(cov > 0) {
		return 0
	}
	if cov >= 1 {
		return 255
	}
	return uint32(cov*255 + 0.5)
}

// blendPixel composites a premultiplied source over the pixel at index i
// with the given coverage, the source-over every Fill, Stroke and Line
// pixel goes through.
//
// Coverage scales all four channels, not just alpha:
//
//	src' = (Sr·cov, Sg·cov, Sb·cov, Sa·cov)
//	dst  = src' + dst·(1 − Sa')
//
// That is the direct consequence of working premultiplied and the classic
// mistake with the format. Scaling only A leaves RGB too bright for its new
// alpha and produces a light halo on every antialiased edge — invisible
// against white, obvious against black.
func (c *Canvas) blendPixel(i int, src, cov uint32) {
	if cov == 0 {
		return
	}

	sa := src >> 24 & 0xFF
	sr := src >> 16 & 0xFF
	sg := src >> 8 & 0xFF
	sb := src & 0xFF

	if cov != 255 {
		sa = mul8(sa, cov)
		sr = mul8(sr, cov)
		sg = mul8(sg, cov)
		sb = mul8(sb, cov)
	}

	// Fully opaque after coverage: nothing of the destination survives, so
	// skip the read-modify-write entirely.
	if sa == 255 {
		c.buf.Pixels[i] = sa<<24 | sr<<16 | sg<<8 | sb
		return
	}
	if sa == 0 && sr == 0 && sg == 0 && sb == 0 {
		return
	}

	dst := c.buf.Pixels[i]
	inv := 255 - sa
	da := sa + mul8(dst>>24&0xFF, inv)
	dr := sr + mul8(dst>>16&0xFF, inv)
	dg := sg + mul8(dst>>8&0xFF, inv)
	db := sb + mul8(dst&0xFF, inv)

	c.buf.Pixels[i] = da<<24 | dr<<16 | dg<<8 | db
}

// replacePixel writes src over the pixel at index i, interpolating toward
// it by cov. This is what Clear and ClearRect use: clearing replaces
// content rather than compositing, so clearing with a fully transparent
// color yields 0x00000000 instead of leaving the old pixel untouched.
//
// At a subpixel boundary the two are mixed linearly:
//
//	dst = lerp(dst, src, cov)
//
// which is a plain interpolation, deliberately not a source-over.
func (c *Canvas) replacePixel(i int, src, cov uint32) {
	if cov == 0 {
		return
	}
	if cov == 255 {
		c.buf.Pixels[i] = src
		return
	}

	dst := c.buf.Pixels[i]
	inv := 255 - cov

	a := mul8(src>>24&0xFF, cov) + mul8(dst>>24&0xFF, inv)
	r := mul8(src>>16&0xFF, cov) + mul8(dst>>16&0xFF, inv)
	g := mul8(src>>8&0xFF, cov) + mul8(dst>>8&0xFF, inv)
	b := mul8(src&0xFF, cov) + mul8(dst&0xFF, inv)

	c.buf.Pixels[i] = a<<24 | r<<16 | g<<8 | b
}
