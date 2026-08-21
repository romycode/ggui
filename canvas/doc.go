// Package canvas is an immediate-mode CPU rasterizer for 2D shapes.
//
// The caller owns the pixels. A Canvas borrows a [Buffer], validates that
// its description is coherent, and from then on writes straight into that
// memory: no copy, no allocation, no internal scene graph, no render
// loop. Each drawing call modifies the buffer immediately and returns.
//
// # Pixel format
//
// Every pixel is a uint32 holding ARGB8888 with premultiplied alpha, so
// its logical value is 0xAARRGGBB. Callers pass straight (non-premultiplied)
// [Color] values; the canvas premultiplies once per operation.
// Compositing happens on sRGB values treated as linear — there is no gamma
// correction, the same tradeoff Cairo and Skia make by default.
//
// # Coordinates
//
// The API takes logical units. The canvas is built with a logical size, a
// physical size and a scale factor, and multiplies geometry by that scale
// exactly once per shape. Logical sizes are integers because a fractional
// logical size cannot be expressed to a Wayland compositor; shape geometry
// is float32 because subpixel placement is the point.
//
// # Errors
//
// No drawing method returns an error. The first invalid argument is
// stored, every later operation becomes a no-op, and [Canvas.Err] reports
// it — check it once when the frame is done. Every possible error is a
// programming bug rather than a runtime condition, so a per-call error
// return would be thirty unreachable branches in a paint function. Only
// [New] returns an error, because there is no object yet to attach it to.
//
// # Damage
//
// A Canvas accumulates the union of everything it actually wrote, in
// physical pixels, which is exactly what wl_surface.damage_buffer wants.
// See [Canvas.Damage] and [Canvas.ResetDamage].
//
// A Canvas is not safe for concurrent use.
package canvas
