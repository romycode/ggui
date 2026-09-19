package eventloop

import (
	"math"

	"github.com/romycode/ggui/canvas"
)

// This file is what the window shows before the application's UI exists, so
// it imports canvas and math and nothing else. Fonts are the slow thing an
// application waits on, and a loader that needed one could not appear before
// the wait it is there to cover. A test holds the imports to that.

var (
	loaderBackground = canvas.Color{R: 0x1e, G: 0x1f, B: 0x24, A: 0xff}
	loaderDot        = canvas.Color{R: 0xe8, G: 0xea, B: 0xed, A: 0xff}
	failedBackground = canvas.Color{R: 0x2a, G: 0x1c, B: 0x1e, A: 0xff}
	failedMark       = canvas.Color{R: 0xe5, G: 0x6b, B: 0x6f, A: 0xff}
)

const (
	loaderDots = 10
	// loaderStepMs is how long the bright head of the spinner stays on one
	// dot. A full turn is loaderDots * loaderStepMs, about 0.8s.
	loaderStepMs = 80
	// loaderMinSize is the smallest logical side worth drawing a spinner in.
	// Below it the dots would be under a pixel and the shapes would only
	// risk degenerate geometry.
	loaderMinSize = 16
)

// PaintLoader draws the loading screen: a spinner on a plain background.
// width and height are the canvas's logical size and t is the compositor's
// millisecond clock, the one a frame callback delivers. The same t always
// gives the same frame.
//
// It draws no text, so it does not wait for a font, and it does not
// allocate, so it costs nothing per frame however long the application takes
// to start.
func PaintLoader(cv *canvas.Canvas, width, height float32, t uint32) {
	cv.Clear(loaderBackground)

	side := min(width, height)
	if side < loaderMinSize {
		return
	}
	DrawSpinner(cv, canvas.Point{X: width / 2, Y: height / 2}, side*0.12, t, loaderDot)
}

// DrawSpinner draws a ring of dots with a bright head that travels round it,
// centered at center with the given ring radius. color is the head's color
// and the tail fades out behind it by scaling color.A. It draws onto
// whatever is already there, which is what lets a busy indicator sit inside
// a real UI. t is the compositor's millisecond clock; the same t always gives
// the same frame, and nothing is allocated.
//
// A radius that is not positive draws nothing.
func DrawSpinner(cv *canvas.Canvas, center canvas.Point, radius float32, t uint32, color canvas.Color) {
	if !(radius > 0) {
		return
	}
	dotRadius := radius * 0.17

	head := int(t/loaderStepMs) % loaderDots
	for i := 0; i < loaderDots; i++ {
		// How many steps behind the head this dot is. The head is the
		// brightest and the tail fades out behind it.
		age := (head - i + loaderDots) % loaderDots
		alpha := 255 - age*(255-40)/loaderDots

		angle := 2*math.Pi*float64(i)/loaderDots - math.Pi/2
		sin, cos := math.Sincos(angle)
		c := color
		c.A = uint8(int(color.A) * alpha / 255)
		cv.FillCircle(canvas.Point{
			X: center.X + radius*float32(cos),
			Y: center.Y + radius*float32(sin),
		}, dotRadius, c)
	}
}

// PaintFailed draws the screen for an application that could not start: a
// circle with a cross, on a background that is not the loader's. Like the
// loader it needs no font. An application that has one may write the reason
// over it; the mark is there for when it does not.
func PaintFailed(cv *canvas.Canvas, width, height float32) {
	cv.Clear(failedBackground)

	side := min(width, height)
	if side < loaderMinSize {
		return
	}
	radius := side * 0.14
	stroke := max(radius*0.14, 1)
	cx, cy := width/2, height/2
	arm := radius * 0.42

	cv.StrokeCircle(canvas.Point{X: cx, Y: cy}, radius, stroke, failedMark)
	cv.Line(canvas.Point{X: cx - arm, Y: cy - arm}, canvas.Point{X: cx + arm, Y: cy + arm}, stroke, canvas.LineCapRound, failedMark)
	cv.Line(canvas.Point{X: cx - arm, Y: cy + arm}, canvas.Point{X: cx + arm, Y: cy - arm}, stroke, canvas.LineCapRound, failedMark)
}
