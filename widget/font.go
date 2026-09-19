package widget

import "github.com/romycode/ggui/canvas"

// Font measures and draws single-line text. It is the only thing a widget
// needs from a text renderer, and it exists because canvas has none.
//
// All geometry is in logical units, the same as canvas.
type Font interface {
	// Measure returns the advance width of s.
	Measure(s string) float32

	// Draw paints s in col, with its left edge at at.X and its vertical
	// center at at.Y — not the baseline, because every widget wants its
	// label centered in a box. Nothing may be painted outside clip.
	Draw(cv *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect)
}
