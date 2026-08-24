package canvas_test

import (
	"errors"
	"fmt"

	"github.com/romycode/ggui/canvas"
)

// The caller owns the pixels. Allocate them, describe them in a
// [canvas.Buffer], and draw.
func Example() {
	const w, h = 64, 32

	c, err := canvas.New(canvas.Buffer{
		Pixels: make([]uint32, w*h),
		Width:  w,
		Height: h,
		Stride: w,
	}, w, h, 1)
	if err != nil {
		panic(err)
	}

	c.Clear(canvas.Color{R: 0x20, G: 0x20, B: 0x20, A: 0xFF})
	c.FillRoundedRect(canvas.Rect{X: 8, Y: 8, Width: 48, Height: 16}, 4,
		canvas.Color{R: 0xFF, G: 0x80, B: 0x00, A: 0xFF})
	c.StrokeRect(canvas.Rect{X: 0, Y: 0, Width: w, Height: h}, 1,
		canvas.Color{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})

	// Errors accumulate on the canvas rather than being returned per
	// call; check once when the frame is done.
	fmt.Println(c.Err())
	// Output: <nil>
}

// A canvas takes its geometry in logical units and multiplies by the scale
// exactly once per shape, so the same drawing code serves every display
// density. The buffer is the physical size; the API is the logical one.
func ExampleNew_hiDPI() {
	// 400x300 logical units on a display at 2x.
	const logicalW, logicalH = 400, 300
	const scale = 2

	c, err := canvas.New(canvas.Buffer{
		Pixels: make([]uint32, logicalW*scale*logicalH*scale),
		Width:  logicalW * scale,
		Height: logicalH * scale,
		Stride: logicalW * scale,
	}, logicalW, logicalH, scale)
	if err != nil {
		panic(err)
	}

	// The same rectangle covers twice as many pixels per axis.
	c.FillRect(canvas.Rect{X: 10, Y: 10, Width: 100, Height: 50},
		canvas.Color{R: 0x00, G: 0x99, B: 0xFF, A: 0xFF})

	dmg, _ := c.Damage()
	fmt.Println(c.Width(), c.Height())
	fmt.Println(c.PixelWidth(), c.PixelHeight())
	fmt.Println(dmg.X, dmg.Y, dmg.Width, dmg.Height)
	// Output:
	// 400 300
	// 800 600
	// 20 20 200 100
}

// Damage is the union of everything actually written, in physical pixels
// — exactly what wl_surface.damage_buffer takes. Resetting it is the
// caller's job, after the commit.
func ExampleCanvas_Damage() {
	const w, h = 100, 100

	c, err := canvas.New(canvas.Buffer{
		Pixels: make([]uint32, w*h),
		Width:  w,
		Height: h,
		Stride: w,
	}, w, h, 1)
	if err != nil {
		panic(err)
	}

	if _, ok := c.Damage(); !ok {
		fmt.Println("nothing drawn yet")
	}

	c.FillRect(canvas.Rect{X: 10, Y: 10, Width: 20, Height: 20},
		canvas.Color{R: 0xFF, A: 0xFF})
	c.FillRect(canvas.Rect{X: 60, Y: 60, Width: 20, Height: 20},
		canvas.Color{B: 0xFF, A: 0xFF})

	// One rectangle is accumulated, not a list: two small changes in
	// opposite corners damage everything between them.
	dmg, _ := c.Damage()
	fmt.Println(dmg.X, dmg.Y, dmg.Width, dmg.Height)

	c.ResetDamage()
	_, ok := c.Damage()
	fmt.Println(ok)
	// Output:
	// nothing drawn yet
	// 10 10 70 70
	// false
}

// A fully transparent color writes nothing and does not extend the damage.
// It is a no-op, not an error.
func ExampleCanvas_Err() {
	const w, h = 32, 32

	c, err := canvas.New(canvas.Buffer{
		Pixels: make([]uint32, w*h),
		Width:  w,
		Height: h,
		Stride: w,
	}, w, h, 1)
	if err != nil {
		panic(err)
	}

	c.FillRect(canvas.Rect{X: 0, Y: 0, Width: 10, Height: 10}, canvas.Color{})
	_, drew := c.Damage()
	fmt.Println("drew:", drew, "err:", c.Err())

	// A negative dimension is a programming bug, so it poisons the
	// canvas: this call and every later one become no-ops.
	c.FillRect(canvas.Rect{X: 0, Y: 0, Width: -1, Height: 10},
		canvas.Color{R: 0xFF, A: 0xFF})
	c.Clear(canvas.Color{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})

	fmt.Println("errored:", c.Err() != nil)
	fmt.Println("invalid argument:", errors.Is(c.Err(), canvas.ErrInvalidArgument))
	// Output:
	// drew: false err: <nil>
	// errored: true
	// invalid argument: true
}

// Colors are straight (non-premultiplied) RGBA on the way in; the stored
// pixel is ARGB8888 with premultiplied alpha.
func ExampleColor() {
	c, err := canvas.New(canvas.Buffer{
		Pixels: make([]uint32, 4),
		Width:  2,
		Height: 2,
		Stride: 2,
	}, 2, 2, 1)
	if err != nil {
		panic(err)
	}

	c.Clear(canvas.Color{R: 0xFF, G: 0x00, B: 0x00, A: 0x80})
	fmt.Printf("%08X\n", c.Pixels()[0])
	// Output: 80800000
}
