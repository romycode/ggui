package main

import (
	"context"
	"image"
	"log"

	"golang.org/x/image/font/basicfont"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/text"
	"github.com/romycode/ggui/widget"
)

// Geometry, in logical units. Everything the window draws is derived from
// these plus the configured size, so a resize needs no state beyond the new
// width and height.
const (
	padding  = 20 // window edge to the nearest control
	gap      = 14 // input to button
	buttonW  = 120
	controlH = 44

	statusOffset = 26 // input bottom to the status line's vertical center
	statusH      = 24 // height of the status line's clip
	spinnerR     = 7  // radius of the busy indicator's ring
)

// glyphPx is how many physical pixels one font pixel becomes before the
// canvas scale is applied. Face7x13 is a 13-pixel face, which is unreadably
// small on anything modern, so it is drawn at 2x.
const glyphPx = 2

// The palette. Opaque on purpose: the surface is opaque, and premultiplied
// alpha only matters here for the antialiased edges canvas produces itself.
var (
	colorBackground   = canvas.Color{R: 0x1e, G: 0x21, B: 0x28, A: 0xff}
	colorAccent       = canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff}
	colorTextDim      = canvas.Color{R: 0x6b, G: 0x72, B: 0x80, A: 0xff}
	placeholderString = "type something"
)

// layout is where the two controls sit for a given window size, in logical
// units. It is recomputed per frame rather than cached: it is four
// subtractions, and caching it means one more thing a resize can leave stale.
type layout struct {
	input  canvas.Rect
	button canvas.Rect
}

// computeLayout puts the button against the right padding at its natural
// width and gives the input everything left over.
func computeLayout(width, height float32) layout {
	y := (height - controlH) / 2
	buttonX := width - padding - buttonW

	// A window narrower than the button plus its padding would give the
	// input a negative width, which canvas rejects — and because canvas
	// errors are sticky, that one bad rect would silently discard every
	// later draw call in the frame.
	inputW := buttonX - gap - padding
	if inputW < 0 {
		inputW = 0
	}

	return layout{
		input:  canvas.Rect{X: padding, Y: y, Width: inputW, Height: controlH},
		button: canvas.Rect{X: buttonX, Y: y, Width: buttonW, Height: controlH},
	}
}

// hit reports whether a surface-local point is inside r. The far edges are
// exclusive so two adjacent controls can never both claim the same pixel.
func hit(r canvas.Rect, x, y float32) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

// ui is the whole widget state. There is no retained widget tree: two
// widgets, the chain that keeps at most one of them focused, and a frame
// that is a pure function of this plus the window size.
type ui struct {
	// field is the text input. Its Bounds come from the layout every time
	// it is used, see place.
	field *widget.TextField
	// button clears the field.
	button *widget.Button
	// focus is the tab order over the two. It is what knows they are
	// siblings: a widget is told whether it is focused and never asks.
	focus *widget.Chain

	// font draws and measures every string in the window, the field's and
	// the button's alike.
	font widget.Font

	// clicked is set by the button's OnClick and read back by
	// pointerReleased, so the window can tell that a release fired it.
	clicked bool
	// caretOn is the blink phase this application drives, which it hands
	// to the field: the widget has no clock. It can lag the field's own by
	// one tick after the user types, which resets it, and the next tick
	// puts them back in step.
	caretOn bool

	// busy means a background task is running. The window keeps asking the
	// compositor for frames while it is, so the spinner moves.
	busy bool
	// status is the line under the controls: what the last task did.
	status string
	// now is the compositor's millisecond clock as of the frame being
	// drawn. Animations read it instead of the wall clock, so a frame is a
	// pure function of the ui.
	now uint32
}

// newUI builds the ui drawing with font, with its button wired to clear
// the field.
func newUI(font widget.Font) *ui {
	u := &ui{font: font, caretOn: true}
	u.field = widget.NewTextField(placeholderString, font)
	u.button = widget.NewButton("Clear", font)
	u.button.OnClick = func() {
		u.field.SetText("")
		u.clicked = true
	}
	u.focus = widget.NewChain(u.field, u.button)
	return u
}

// place puts both widgets where the layout says. A widget owns its bounds
// but not the layout, so the caller pushes them in before every use.
func (u *ui) place(l layout) {
	u.field.Bounds = l.input
	u.button.Bounds = l.button
}

// pointerMoved updates the button's hover and reports whether anything
// visible changed. Motion arrives on every pixel the pointer crosses;
// repainting for each one would be pure waste when only a transition is
// visible.
func (u *ui) pointerMoved(l layout, x, y float32) bool {
	u.place(l)
	return u.button.PointerMove(x, y)
}

// pointerPressed moves the focus and lets the widgets take the press. The
// three calls on the field are the wiring docs/widget.md describes: the
// application decides the focus, because Focusable exposes no Bounds, and
// the widget places the caret.
func (u *ui) pointerPressed(l layout, x, y float32) bool {
	u.place(l)

	changed := false
	if hit(l.input, x, y) {
		changed = u.focus.Focus(u.field)
		changed = u.field.PointerDown(x, y) || changed
	} else {
		changed = u.focus.Blur()
	}
	return u.button.PointerDown(x, y) || changed
}

// pointerReleased hands the release to the button and reports whether it
// fired. The click-on-release rule itself lives in widget.Button.
func (u *ui) pointerReleased(l layout, x, y float32) bool {
	u.place(l)
	u.clicked = false
	u.button.PointerUp(x, y)
	return u.clicked
}

// keyDown hands a translated key to the chain, which moves the focus on
// Tab and forwards everything else to whichever widget has it.
func (u *ui) keyDown(k widget.Key) bool { return u.focus.KeyDown(k) }

func (u *ui) keyUp(k widget.Key) bool { return u.focus.KeyUp(k) }

// insert offers composed text to the field, which ignores it unless it has
// the focus.
func (u *ui) insert(s string) bool { return u.field.Insert(s) }

// blur takes the caret away from whatever has it.
func (u *ui) blur() bool { return u.focus.Blur() }

// setCaretVisible drives the blink from the application's timer.
func (u *ui) setCaretVisible(v bool) bool {
	u.caretOn = v
	return u.field.SetCaretVisible(v)
}

// text is what the field holds, which submitting sends.
func (u *ui) text() string { return u.field.Text() }

// editing reports whether the field has the caret.
func (u *ui) editing() bool { return u.field.Focused() }

// animating reports whether the ui wants a frame on every compositor
// callback. Only a running task does: its spinner moves.
func (u *ui) animating() bool { return u.busy }

// draw paints one complete frame. It always repaints everything: each
// frame goes into a buffer the compositor has finished with, whose
// previous contents are two frames old, so there is nothing to preserve.
func draw(cv *canvas.Canvas, l layout, u *ui) {
	cv.Clear(colorBackground)

	u.place(l)
	u.field.Draw(cv)
	u.button.Draw(cv)
	drawStatus(cv, l, u)
}

// drawStatus paints the line under the controls: a spinner while a task runs,
// then its status text. Both are clipped to the window, so a small one loses
// them rather than breaking the frame.
func drawStatus(cv *canvas.Canvas, l layout, u *ui) {
	if u.status == "" && !u.busy {
		return
	}
	y := l.input.Y + l.input.Height + statusOffset
	x := l.input.X

	if u.busy {
		eventloop.DrawSpinner(cv, canvas.Point{X: x + spinnerR, Y: y}, spinnerR, u.now, colorAccent)
		x += 2*spinnerR + 12
	}
	width := l.button.X + l.button.Width - x
	if u.status == "" || width <= 0 {
		return
	}
	u.font.Draw(cv, canvas.Point{X: x, Y: y}, u.status, colorTextDim,
		canvas.Rect{X: x, Y: y - statusH/2, Width: width, Height: statusH})
}

// face is the fallback font, used only when no system font can be found. It
// is ASCII-only: anything outside U+0020..U+007E, including every accented
// character Composer produces, falls back to the replacement glyph. System
// fonts, loaded through the text package, have no such limit.
var face = basicfont.Face7x13

// fontSize is the size of the system font, in logical units.
const fontSize = 16

// loadFont returns the system's sans-serif at fontSize, or the built-in
// bitmap font if the machine has none we can read. The example has to run
// either way, so a missing font is a log line, not an exit.
func loadFont(ctx context.Context) widget.Font {
	f, err := text.NewSystemFaceContext(ctx, fontSize, text.Regular)
	if err != nil {
		if ctx.Err() != nil {
			return bitmapFont{}
		}
		log.Printf("no system font (%v); falling back to the built-in bitmap font", err)
		return bitmapFont{}
	}
	return f
}

// textWidth is the advance of s in logical units. Face7x13 is fixed-pitch,
// so this is a multiplication, not a shaping pass.
func textWidth(s string) float32 {
	return float32(len([]rune(s)) * face.Advance * glyphPx)
}

// bitmapFont adapts the fallback blitter to widget.Font.
type bitmapFont struct{}

func (bitmapFont) Measure(s string) float32 { return textWidth(s) }

func (bitmapFont) Draw(cv *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect) {
	drawText(cv, at, s, col, clip)
}

// drawText blits s into the pixels the canvas borrowed, clipped to clip.
//
// It bypasses the canvas drawing API because canvas has no text: it fills
// shapes. Writing glyph pixels into Canvas.Pixels directly is within the
// borrow contract — canvas never owns or copies that memory — but it does
// mean these pixels are outside the canvas's damage tracking, which is why
// the window damages the whole buffer every frame rather than using
// Canvas.Damage.
//
// at.Y is the vertical center of the text, not the baseline: every caller
// wants text centered in a control, and centering on the cell is what a
// fixed-height face makes easy.
func drawText(cv *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect) {
	scale := cv.Scale()
	block := glyphPx * int(scale+0.5)
	if block < 1 {
		block = 1
	}

	cellH := face.Ascent + face.Descent

	originX := int(at.X*scale + 0.5)
	originY := int(at.Y*scale+0.5) - cellH*block/2

	// Clip in physical pixels, intersected with the visible buffer so no
	// write can reach the row padding beyond Width.
	x0, y0 := int(clip.X*scale+0.5), int(clip.Y*scale+0.5)
	x1, y1 := int((clip.X+clip.Width)*scale+0.5), int((clip.Y+clip.Height)*scale+0.5)
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, cv.PixelWidth()), min(y1, cv.PixelHeight())

	pen := originX
	for _, r := range s {
		drawGlyph(cv, r, pen, originY, block, col, x0, y0, x1, y1)
		pen += face.Advance * block

		// Everything from here on is past the clip; stop rather than
		// walking a long string one invisible glyph at a time.
		if pen >= x1 {
			break
		}
	}
}

// drawGlyph blits one rune's mask. The mask row for a rune is found the way
// basicfont documents it: the ranges map a rune to a vertical slice of the
// mask image, one cell tall.
func drawGlyph(cv *canvas.Canvas, r rune, originX, originY, block int, col canvas.Color, x0, y0, x1, y1 int) {
	cellH := face.Ascent + face.Descent

	row, ok := glyphRow(r)
	if !ok {
		return
	}

	px := cv.Pixels()
	stride := cv.Stride()
	mask, isAlpha := face.Mask.(*image.Alpha)
	if !isAlpha {
		return
	}

	for gy := range cellH {
		for gx := range face.Width {
			alpha := mask.AlphaAt(mask.Bounds().Min.X+gx, row*cellH+gy).A
			if alpha == 0 {
				continue
			}

			// One font pixel becomes a block x block square. The face is a
			// bilevel bitmap, so there is nothing to interpolate and a
			// nearest-neighbour blow-up is exactly right.
			for by := range block {
				y := originY + gy*block + by
				if y < y0 || y >= y1 {
					continue
				}
				base := y * stride
				for bx := range block {
					x := originX + gx*block + bx
					if x < x0 || x >= x1 {
						continue
					}
					px[base+x] = blend(px[base+x], col, alpha)
				}
			}
		}
	}
}

// glyphRow maps a rune to its row in the mask, falling back to the
// replacement character for anything the face does not carry.
func glyphRow(r rune) (int, bool) {
	for _, rg := range face.Ranges {
		if rg.Low <= r && r < rg.High {
			return int(r-rg.Low) + rg.Offset, true
		}
	}
	if r == '�' {
		return 0, false // no replacement glyph either; draw nothing
	}
	return glyphRow('�')
}

// blend composites a straight-alpha color over one opaque ARGB8888 pixel.
//
// canvas premultiplies internally but keeps that unexported, and the only
// destination here is the opaque control the glyph sits on, so plain
// source-over on each channel is both correct and enough.
func blend(dst uint32, col canvas.Color, alpha uint8) uint32 {
	if alpha == 0xff {
		return 0xff000000 | uint32(col.R)<<16 | uint32(col.G)<<8 | uint32(col.B)
	}

	a := uint32(alpha)
	inv := 255 - a

	dr := (dst >> 16) & 0xff
	dg := (dst >> 8) & 0xff
	db := dst & 0xff

	r := (uint32(col.R)*a + dr*inv) / 255
	g := (uint32(col.G)*a + dg*inv) / 255
	b := (uint32(col.B)*a + db*inv) / 255

	return 0xff000000 | r<<16 | g<<8 | b
}
