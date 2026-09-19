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
	corner   = 8  // input radius; widget.DefaultButtonStyle uses the same
	border   = 2  // input outline width; likewise
	caretW   = 2  // caret width, logical units
	textPad  = 12 // control edge to its first glyph

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
	colorInput        = canvas.Color{R: 0x2b, G: 0x30, B: 0x38, A: 0xff}
	colorBorder       = canvas.Color{R: 0x3a, G: 0x40, B: 0x49, A: 0xff}
	colorAccent       = canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff}
	colorText         = canvas.Color{R: 0xe6, G: 0xe9, B: 0xef, A: 0xff}
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

// ui is the whole widget state. There is no retained widget tree: the input
// is three fields here, the button is a widget.Button, and a frame is a pure
// function of this plus the window size.
type ui struct {
	// text is the input's contents, as runes rather than a string so that
	// backspace deletes a character instead of a byte.
	text []rune
	// focused is our own notion of focus, not the compositor's. Wayland
	// focuses surfaces; which widget inside the surface has the caret is
	// entirely the client's business.
	focused bool

	// font draws and measures every string in the window, the input's and the
	// button's alike.
	font widget.Font

	// button clears the input. Its Bounds are set from the layout every time
	// it is used, see place.
	button *widget.Button
	// clicked is set by the button's OnClick and read back by
	// pointerReleased, so the window can tell that a release fired it.
	clicked bool

	// caretOn is the caret's blink phase: whether it is showing right now.
	// A timer flips it while the input is focused, and any edit or click
	// sets it again so the caret does not vanish under the user's hands.
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

// newUI builds the ui drawing with font, with its button wired to clear the
// input.
func newUI(font widget.Font) *ui {
	u := &ui{font: font, caretOn: true}
	u.button = widget.NewButton("Clear", font)
	u.button.OnClick = func() {
		u.text = u.text[:0]
		u.clicked = true
	}
	return u
}

// place puts the button where the layout says. The widget owns its bounds
// but not the layout, so the caller pushes it in before every use.
func (u *ui) place(l layout) {
	u.button.Bounds = l.button
}

// pointerMoved updates the button's hover and reports whether anything
// visible changed. Motion arrives on every pixel the pointer crosses;
// repainting the window for each one would be pure waste when only a
// transition is visible.
func (u *ui) pointerMoved(l layout, x, y float32) bool {
	u.place(l)
	return u.button.PointerMove(x, y)
}

// pointerPressed moves focus and lets the button take the press.
func (u *ui) pointerPressed(l layout, x, y float32) {
	u.place(l)
	u.focused = hit(l.input, x, y)
	if u.focused {
		u.caretOn = true
	}
	u.button.PointerDown(x, y)
}

// pointerReleased hands the release to the button and reports whether it
// fired. The click-on-release rule itself lives in widget.Button.
func (u *ui) pointerReleased(l layout, x, y float32) bool {
	u.place(l)
	u.clicked = false
	u.button.PointerUp(x, y)
	return u.clicked
}

// insert appends composed text and reports whether the input changed.
//
// Control characters are dropped here rather than at the call site because
// Composer.Feed returns them: Keysym.Rune maps Return to '\r' and Tab to
// '\t' through the legacy table, so both arrive as ordinary text that would
// otherwise be stored and drawn as U+FFFD.
func (u *ui) insert(s string) bool {
	if !u.focused {
		return false
	}

	changed := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		u.text = append(u.text, r)
		changed = true
	}
	if changed {
		u.caretOn = true
	}
	return changed
}

// backspace deletes the last rune and reports whether it deleted anything.
func (u *ui) backspace() bool {
	if !u.focused || len(u.text) == 0 {
		return false
	}
	u.text = u.text[:len(u.text)-1]
	u.caretOn = true
	return true
}

// animating reports whether the ui wants a frame on every compositor
// callback. Only a running task does: its spinner moves. Everything else
// changes in response to an event and asks for its own repaint, which is what
// lets an idle window stop drawing.
func (u *ui) animating() bool { return u.busy }

// draw paints one complete frame. It always repaints everything: each frame
// goes into a buffer the compositor has finished with, whose previous
// contents are two frames old, so there is nothing to preserve.
func draw(cv *canvas.Canvas, l layout, u *ui) {
	cv.Clear(colorBackground)

	drawInput(cv, l.input, u)
	u.place(l)
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

func drawInput(cv *canvas.Canvas, r canvas.Rect, u *ui) {
	cv.FillRoundedRect(r, corner, colorInput)

	outline := colorBorder
	if u.focused {
		outline = colorAccent
	}
	cv.StrokeRoundedRect(r, corner, border, outline)

	baseline := canvas.Point{X: r.X + textPad, Y: r.Y + r.Height/2}

	if len(u.text) == 0 && !u.focused {
		u.font.Draw(cv, baseline, placeholderString, colorTextDim, r)
		return
	}

	text := string(u.text)
	u.font.Draw(cv, baseline, text, colorText, r)

	// The caret sits after the last glyph and blinks: a timer flips caretOn
	// twice a second, and nothing else is drawing when it does.
	if u.focused && u.caretOn {
		caret := canvas.Rect{
			X:      baseline.X + u.font.Measure(text),
			Y:      r.Y + textPad/2,
			Width:  caretW,
			Height: r.Height - textPad,
		}
		// Clamp into the control so a long line's caret does not escape it.
		if limit := r.X + r.Width - textPad; caret.X > limit {
			caret.X = limit
		}
		cv.FillRect(caret, colorAccent)
	}
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
