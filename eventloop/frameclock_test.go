package eventloop

import (
	"math/rand"
	"testing"
)

// step is one thing that happens to the clock, or a question put to it.
type step struct {
	do   string // invalidate, animate, still, free, starve, done, painted
	want *bool  // when set, ShouldPaint must equal it after do
}

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

func apply(c *FrameClock, do string) {
	switch do {
	case "invalidate":
		c.Invalidate()
	case "animate":
		c.SetAnimating(true)
	case "still":
		c.SetAnimating(false)
	case "free":
		c.SetBufferFree(true)
	case "starve":
		c.SetBufferFree(false)
	case "done":
		c.FrameDone()
	case "painted":
		c.Painted()
	case "":
	default:
		panic("unknown step " + do)
	}
}

func TestFrameClockSequences(t *testing.T) {
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name:  "a fresh clock has nothing to paint",
			steps: []step{{"", no()}},
		},
		{
			name: "an invalidated idle clock paints at once, then waits for the compositor",
			steps: []step{
				{"invalidate", yes()},
				{"painted", no()},
				{"", no()},
			},
		},
		{
			name: "an invalidate while a frame is in flight waits for FrameDone",
			steps: []step{
				{"invalidate", yes()},
				{"painted", no()},
				{"invalidate", no()},
				{"done", yes()},
			},
		},
		{
			name: "painting consumes the invalidate: no FrameDone repaint without a new reason",
			steps: []step{
				{"invalidate", yes()},
				{"painted", no()},
				{"done", no()},
			},
		},
		{
			name: "an animating clock keeps painting on every FrameDone",
			steps: []step{
				{"animate", yes()},
				{"painted", no()},
				{"done", yes()},
				{"painted", no()},
				{"done", yes()},
			},
		},
		{
			name: "an animation that stops is not painted again",
			steps: []step{
				{"animate", yes()},
				{"painted", no()},
				{"still", no()},
				{"done", no()},
			},
		},
		{
			name: "without a free buffer nothing paints, and the wish survives until one is free",
			steps: []step{
				{"starve", no()},
				{"invalidate", no()},
				{"", no()},
				{"free", yes()},
			},
		},
		{
			name: "a starved animating clock resumes when a buffer frees",
			steps: []step{
				{"animate", yes()},
				{"painted", no()},
				{"starve", no()},
				{"done", no()},
				{"free", yes()},
			},
		},
		{
			name: "a FrameDone with no frame in flight invents no work",
			steps: []step{
				{"done", no()},
				{"done", no()},
			},
		},
		{
			name: "a static UI costs nothing after its last paint",
			steps: []step{
				{"invalidate", yes()},
				{"painted", no()},
				{"done", no()},
				{"", no()},
				{"done", no()},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c FrameClock
			for i, s := range tt.steps {
				apply(&c, s.do)
				if s.want != nil && c.ShouldPaint() != *s.want {
					t.Fatalf("step %d (%q): ShouldPaint = %v, want %v", i, s.do, c.ShouldPaint(), *s.want)
				}
			}
		})
	}
}

// The property the whole design leans on: with one frame in flight at a
// time, a UI can never run ahead of a slow compositor. Whatever order
// things happen in, ShouldPaint stays false from a Painted until the
// FrameDone that answers it.
func TestFrameClockNeverAllowsTwoFramesInFlight(t *testing.T) {
	ops := []string{"invalidate", "animate", "still", "free", "starve", "done"}

	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var c FrameClock
		inFlight := false

		for i := 0; i < 20_000; i++ {
			op := ops[rng.Intn(len(ops))]
			apply(&c, op)
			if op == "done" {
				inFlight = false
			}

			// The UI's own loop: paint whenever the clock says to.
			if c.ShouldPaint() {
				if inFlight {
					t.Fatalf("seed %d op %d (%s): ShouldPaint is true with a frame still in flight", seed, i, op)
				}
				c.Painted()
				inFlight = true
			}
		}
	}
}

// After a paint the clock owes nothing: the next paint needs a new reason.
// This is what keeps an idle window at zero CPU.
func TestFrameClockPaintedClearsTheInvalidate(t *testing.T) {
	var c FrameClock
	c.Invalidate()
	c.Painted()
	c.FrameDone()

	if c.ShouldPaint() {
		t.Fatal("the clock wants to paint again with nothing invalidated and nothing animating")
	}
}
