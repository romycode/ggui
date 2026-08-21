package canvas

import (
	"errors"
	"math"
	"testing"
)

func TestNewValid(t *testing.T) {
	// 800x600 logical at 1.5 => 1200x900 physical, stride padded to 1216.
	px := make([]uint32, 1216*900)
	c, err := New(Buffer{Pixels: px, Width: 1200, Height: 900, Stride: 1216}, 800, 600, 1.5)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if c.Width() != 800 || c.Height() != 600 {
		t.Errorf("logical size = %dx%d, want 800x600", c.Width(), c.Height())
	}
	if c.PixelWidth() != 1200 || c.PixelHeight() != 900 {
		t.Errorf("physical size = %dx%d, want 1200x900", c.PixelWidth(), c.PixelHeight())
	}
	if c.Scale() != 1.5 {
		t.Errorf("Scale() = %v, want 1.5", c.Scale())
	}
	if c.Stride() != 1216 {
		t.Errorf("Stride() = %d, want 1216", c.Stride())
	}
}

func TestNewBorrowsBufferWithoutCopying(t *testing.T) {
	px := make([]uint32, 64*64)
	c, err := New(Buffer{Pixels: px, Width: 64, Height: 64, Stride: 64}, 64, 64, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.Pixels()
	if len(got) != len(px) || cap(got) != cap(px) {
		t.Fatalf("Pixels() len/cap = %d/%d, want %d/%d", len(got), cap(got), len(px), cap(px))
	}
	// Same backing array: writing through one is visible through the other.
	px[10] = 0xDEADBEEF
	if got[10] != 0xDEADBEEF {
		t.Error("Pixels() returned a copy, want the borrowed slice")
	}
}

func TestNewAcceptsSubPixelRoundingDifference(t *testing.T) {
	// 801 logical at 1.5 is 1201.5 physical; both 1201 and 1202 are within
	// the one-pixel tolerance the spec grants platform rounding policies.
	// The height is exact (10 * 1.5 = 15) so only the width is under test.
	for _, pw := range []int{1201, 1202} {
		px := make([]uint32, pw*15)
		_, err := New(Buffer{Pixels: px, Width: pw, Height: 15, Stride: pw}, 801, 10, 1.5)
		if err != nil {
			t.Errorf("New with physical width %d: unexpected error: %v", pw, err)
		}
	}
}

func TestNewRejectsIncoherentPhysicalSize(t *testing.T) {
	// 800 logical at 1.5 is 1200; 1400 is far outside the tolerance and must
	// not be accepted just because the buffer is big enough.
	px := make([]uint32, 1400*900)
	_, err := New(Buffer{Pixels: px, Width: 1400, Height: 900, Stride: 1400}, 800, 600, 1.5)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New with oversized buffer: err = %v, want ErrInvalidArgument", err)
	}
}

func TestNewRejectsShortBuffer(t *testing.T) {
	// Needs (Height-1)*Stride + Width = 9*64 + 64 = 640 elements.
	px := make([]uint32, 639)
	_, err := New(Buffer{Pixels: px, Width: 64, Height: 10, Stride: 64}, 64, 10, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	want := `canvas: New: invalid argument "buffer.Pixels": buffer is too short (need 640, got 639)`
	if err.Error() != want {
		t.Errorf("err =\n  %q\nwant\n  %q", err, want)
	}
}

func TestNewAcceptsBufferWithoutTrailingPadding(t *testing.T) {
	// The last row needs no padding after it: (10-1)*64 + 60 = 636 suffices
	// even though Stride*Height would be 640.
	px := make([]uint32, 636)
	if _, err := New(Buffer{Pixels: px, Width: 60, Height: 10, Stride: 64}, 60, 10, 1); err != nil {
		t.Errorf("New: unexpected error: %v", err)
	}
}

func TestNewRejectsBadArguments(t *testing.T) {
	ok := Buffer{Pixels: make([]uint32, 64*64), Width: 64, Height: 64, Stride: 64}
	cases := []struct {
		name   string
		buffer Buffer
		w, h   int
		scale  float32
	}{
		{"zero logical width", ok, 0, 64, 1},
		{"zero logical height", ok, 64, 0, 1},
		{"negative logical width", ok, -64, 64, 1},
		{"zero scale", ok, 64, 64, 0},
		{"negative scale", ok, 64, 64, -1},
		{"NaN scale", ok, 64, 64, float32(math.NaN())},
		{"Inf scale", ok, 64, 64, float32(math.Inf(1))},
		{"zero physical width", Buffer{Pixels: ok.Pixels, Width: 0, Height: 64, Stride: 64}, 64, 64, 1},
		{"zero physical height", Buffer{Pixels: ok.Pixels, Width: 64, Height: 0, Stride: 64}, 64, 64, 1},
		{"negative physical width", Buffer{Pixels: ok.Pixels, Width: -64, Height: 64, Stride: 64}, 64, 64, 1},
		{"stride below width", Buffer{Pixels: ok.Pixels, Width: 64, Height: 64, Stride: 63}, 64, 64, 1},
		{"nil pixels", Buffer{Pixels: nil, Width: 64, Height: 64, Stride: 64}, 64, 64, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.buffer, tc.w, tc.h, tc.scale)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
			if c != nil {
				t.Error("New returned a non-nil Canvas alongside an error")
			}
		})
	}
}

func TestNewRejectsOverflowingGeometry(t *testing.T) {
	// (Height-1)*Stride + Width overflows int on 32-bit and is astronomically
	// larger than any real slice on 64-bit; either way it must be rejected
	// as a length problem, never panic and never wrap around to something
	// small enough to pass the check.
	const huge = math.MaxInt32
	c, err := New(Buffer{Pixels: make([]uint32, 16), Width: huge, Height: huge, Stride: huge}, huge, huge, 1)
	if err == nil {
		t.Fatal("New accepted overflowing geometry")
	}
	if c != nil {
		t.Error("New returned a non-nil Canvas alongside an error")
	}
}

func TestNewStrideEqualToWidthIsValid(t *testing.T) {
	px := make([]uint32, 32*16)
	if _, err := New(Buffer{Pixels: px, Width: 32, Height: 16, Stride: 32}, 32, 16, 1); err != nil {
		t.Errorf("New with compact buffer: %v", err)
	}
}
