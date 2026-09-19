package widget

import "github.com/romycode/ggui/canvas"

// ButtonStyle is the look of a Button: one fill and label color per state,
// plus the outline. The zero value draws nothing visible, so start from
// [DefaultButtonStyle].
type ButtonStyle struct {
	// Fill is the resting background color.
	Fill canvas.Color
	// Label is the resting label color.
	Label canvas.Color
	// HoverFill replaces Fill while the pointer is over the button.
	HoverFill canvas.Color
	// PressedFill replaces Fill while the button is pressed.
	PressedFill canvas.Color
	// PressedLabel replaces Label while the button is pressed.
	PressedLabel canvas.Color
	// DisabledFill replaces every other fill while the button is disabled.
	DisabledFill canvas.Color
	// DisabledLabel replaces every other label color while the button is
	// disabled.
	DisabledLabel canvas.Color

	// BorderColor is the outline color.
	BorderColor canvas.Color
	// Border is the outline width in logical units. Zero draws no outline.
	Border float32
	// Corner is the corner radius in logical units.
	Corner float32

	// FocusRing is the color of the ring drawn while the button holds the
	// keyboard focus. It has to read against every fill above, because
	// focus is not one of the states: a focused button is also resting,
	// hovered or pressed.
	FocusRing canvas.Color
	// FocusRingWidth is the ring's width in logical units. Zero draws no
	// ring, which leaves a focused button looking exactly like an
	// unfocused one.
	FocusRingWidth float32
}

// DefaultButtonStyle returns a dark-theme style: a grey button that
// lightens on hover and turns accent blue while pressed.
func DefaultButtonStyle() ButtonStyle {
	return ButtonStyle{
		Fill:          canvas.Color{R: 0x3a, G: 0x40, B: 0x49, A: 0xff},
		Label:         canvas.Color{R: 0xe6, G: 0xe9, B: 0xef, A: 0xff},
		HoverFill:     canvas.Color{R: 0x46, G: 0x4e, B: 0x5a, A: 0xff},
		PressedFill:   canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff},
		PressedLabel:  canvas.Color{R: 0x10, G: 0x14, B: 0x1a, A: 0xff},
		DisabledFill:  canvas.Color{R: 0x2b, G: 0x30, B: 0x38, A: 0xff},
		DisabledLabel: canvas.Color{R: 0x6b, G: 0x72, B: 0x80, A: 0xff},
		BorderColor:   canvas.Color{R: 0x3a, G: 0x40, B: 0x49, A: 0xff},
		Border:        2,
		Corner:        8,

		// The ring is the label's near-white rather than the accent blue,
		// which is the pressed fill: an accent ring would disappear at
		// exactly the moment the user is acting on the button.
		FocusRing:      canvas.Color{R: 0xe6, G: 0xe9, B: 0xef, A: 0xff},
		FocusRingWidth: 2,
	}
}

// Button is a push button: it shows a label and calls OnClick when the user
// presses and releases the pointer over it, or activates it from the
// keyboard while it holds the focus.
//
// Activation follows the usual desktop rule. A press arms the button; the
// release fires it only if the pointer is still inside. Dragging off a
// pressed button and letting go cancels the click, and while the pointer is
// off the button it is drawn as not pressed, so the user can see that
// letting go there will do nothing. Space follows the same shape — it arms
// on the press and fires on the release, and Escape cancels — while Enter
// fires outright.
//
// Button implements [Focusable]. It never takes the focus itself; the
// caller gives it.
//
// The zero value is not useful; build one with [NewButton].
type Button struct {
	// Bounds is where the button sits, in logical units. The caller sets it
	// — typically every frame, from its own layout — and Draw and the
	// pointer methods read it.
	Bounds canvas.Rect
	// Label is the text shown centered in the button.
	Label string
	// Font draws and measures Label. A nil Font draws the button without a
	// label.
	Font Font
	// Style is the button's look. It may be changed at any time.
	Style ButtonStyle
	// OnClick is called when the button is activated, from PointerUp,
	// KeyUp or KeyDown depending on what activated it. It may be nil.
	OnClick func()
	// Disabled makes the button ignore presses and keys, refuse the focus,
	// and draw in its disabled colors.
	Disabled bool

	hover    bool // pointer is over Bounds
	armed    bool // a press started on the button and has not been released
	focused  bool // the caller has given this button the keyboard focus
	keyArmed bool // Space is down on the focused button
}

var _ Focusable = (*Button)(nil)

// NewButton returns a button with the given label, drawn with font and
// [DefaultButtonStyle]. Set Bounds and OnClick before use.
func NewButton(label string, font Font) *Button {
	return &Button{Label: label, Font: font, Style: DefaultButtonStyle()}
}

// Hovered reports whether the pointer is over the button.
func (b *Button) Hovered() bool { return b.hover }

// Pressed reports whether the button is showing as pressed: a press started
// on it and the pointer is still inside, or Space is held down on it. It is
// false while the pointer is dragged off a pressed button, even though a
// release back inside would still fire.
func (b *Button) Pressed() bool { return (b.armed && b.hover) || b.keyArmed }

// Focused reports whether the button holds the keyboard focus, as the
// caller last set it.
func (b *Button) Focused() bool { return b.focused }

// SetFocused gives the button the keyboard focus or takes it away, and
// reports whether its appearance changed. Losing the focus abandons a
// Space held down on it: the release will go to whatever is focused next.
//
// A disabled button refuses the focus, so a caller walking a focus order
// can offer it to each widget in turn and let them decline. Disabling a
// button that is already focused does not take the focus away — the caller
// owns where it goes — but the button stops drawing the ring, because a
// control that ignores the keyboard must not look like it is listening.
func (b *Button) SetFocused(focused bool) bool {
	if focused && b.Disabled {
		return false
	}
	before := b.visual()
	b.focused = focused
	if !focused {
		b.keyArmed = false
	}
	return b.visual() != before
}

// look is the button's fill state: the four mutually exclusive ways it can
// be colored.
type look uint8

const (
	lookRest look = iota
	lookHover
	lookPressed
	lookDisabled
)

func (b *Button) look() look {
	switch {
	case b.Disabled:
		return lookDisabled
	case b.Pressed():
		return lookPressed
	case b.hover:
		return lookHover
	}
	return lookRest
}

// visual is everything about the button a repaint would show. Every event
// method compares it before and after, so the "did anything change" they
// report is exactly "would a repaint look different" — a disabled button,
// whose appearance nothing can move, never asks for one.
//
// Focus is a field of its own rather than a fifth look because it is
// orthogonal: a focused button is still resting, hovered or pressed, and
// draws the matching fill under its ring.
type visual struct {
	look look
	// ring is whether the focus ring is on screen, not whether the button
	// holds the focus. A style with no ring makes focus invisible, and a
	// button that reported a change nobody could see would be lying about
	// the one thing these methods promise.
	ring bool
}

func (b *Button) visual() visual {
	return visual{look: b.look(), ring: b.ringVisible()}
}

// ringVisible is the single predicate behind both the change reporting and
// the drawing, so the two cannot drift into disagreeing about whether a
// focused button looks any different.
func (b *Button) ringVisible() bool {
	st := &b.Style
	return b.focused && !b.Disabled && st.FocusRingWidth > 0 && st.FocusRing.A > 0
}

// PointerMove tracks the pointer at (x, y) and reports whether the
// button's appearance changed.
//
// It must be fed motion even while a button is held: Wayland keeps
// delivering motion to the surface that received the press, and Pressed
// depends on it.
func (b *Button) PointerMove(x, y float32) bool {
	before := b.visual()
	b.hover = contains(b.Bounds, x, y)
	return b.visual() != before
}

// PointerLeave tells the button the pointer has left its surface. Any press
// in progress is abandoned — the release will go to another surface, so it
// could never complete here. It reports whether the appearance changed.
func (b *Button) PointerLeave() bool {
	before := b.visual()
	b.hover, b.armed = false, false
	return b.visual() != before
}

// PointerDown handles a primary-button press at (x, y). It arms the button
// if the press landed on it, and reports whether the appearance changed.
func (b *Button) PointerDown(x, y float32) bool {
	before := b.visual()
	b.hover = contains(b.Bounds, x, y)
	b.armed = b.hover && !b.Disabled
	return b.visual() != before
}

// PointerUp handles the release of the primary button at (x, y). If the
// button was armed and the release is inside it, OnClick is called. It
// reports whether the appearance changed.
func (b *Button) PointerUp(x, y float32) bool {
	if !b.armed {
		return false
	}
	before := b.visual()
	b.armed = false
	b.hover = contains(b.Bounds, x, y)

	if b.hover && !b.Disabled && b.OnClick != nil {
		b.OnClick()
	}
	return b.visual() != before
}

// KeyDown handles a key press and reports whether the appearance changed.
// A button that is not focused, or that is disabled, ignores every key —
// the caller may hand the same press to each widget it owns without first
// working out whose it is.
//
// [KeySpace] arms the button, which fires on the release; [KeyEscape]
// abandons that. [KeyEnter] fires OnClick here and now.
//
// Enter changes nothing visible, so it reports false even though it
// activated the button. The bool answers only "would a repaint look
// different", and a caller whose OnClick changed what is on screen repaints
// on that account, exactly as it does after a pointer click.
func (b *Button) KeyDown(k Key) bool {
	if !b.focused || b.Disabled {
		return false
	}
	before := b.visual()
	switch k {
	case KeySpace:
		b.keyArmed = true
	case KeyEscape:
		b.keyArmed = false
	case KeyEnter:
		if b.OnClick != nil {
			b.OnClick()
		}
	default:
		return false
	}
	return b.visual() != before
}

// KeyUp handles a key release and reports whether the appearance changed.
// Releasing [KeySpace] on an armed button fires OnClick; every other key is
// ignored, since nothing else latches.
func (b *Button) KeyUp(k Key) bool {
	if k != KeySpace || !b.keyArmed {
		return false
	}
	before := b.visual()
	b.keyArmed = false

	if !b.Disabled && b.OnClick != nil {
		b.OnClick()
	}
	return b.visual() != before
}

// Draw paints the button into cv. It allocates nothing beyond what Font
// does. Bounds must be a valid canvas rectangle: a negative size is
// recorded as a canvas error like any other bad argument.
func (b *Button) Draw(cv *canvas.Canvas) {
	st := &b.Style

	fill, label := st.Fill, st.Label
	switch b.look() {
	case lookDisabled:
		fill, label = st.DisabledFill, st.DisabledLabel
	case lookPressed:
		fill, label = st.PressedFill, st.PressedLabel
	case lookHover:
		fill = st.HoverFill
	}

	cv.FillRoundedRect(b.Bounds, st.Corner, fill)
	if st.Border > 0 {
		cv.StrokeRoundedRect(b.Bounds, st.Corner, st.Border, st.BorderColor)
	}
	if b.ringVisible() {
		// Inside the border, not around it. There is no layout engine
		// here, so a ring drawn outside Bounds would land on whatever the
		// caller put next to the button — and the caller has no way to
		// know it has to leave room.
		r := inset(b.Bounds, st.Border)
		cv.StrokeRoundedRect(r, max(st.Corner-st.Border, 0), st.FocusRingWidth, st.FocusRing)
	}

	if b.Font == nil || b.Label == "" {
		return
	}
	at := canvas.Point{
		X: b.Bounds.X + (b.Bounds.Width-b.Font.Measure(b.Label))/2,
		Y: b.Bounds.Y + b.Bounds.Height/2,
	}
	b.Font.Draw(cv, at, b.Label, label, b.Bounds)
}

// inset shrinks r by d on every side, clamping the result at zero rather
// than letting it go negative: a rectangle too small to inset collapses to
// nothing, which canvas skips, instead of becoming an error the caller
// never asked for.
func inset(r canvas.Rect, d float32) canvas.Rect {
	return canvas.Rect{
		X:      r.X + d,
		Y:      r.Y + d,
		Width:  max(r.Width-2*d, 0),
		Height: max(r.Height-2*d, 0),
	}
}

// contains reports whether a point is inside r. The far edges are
// exclusive, so two adjacent widgets can never both claim the same pixel.
func contains(r canvas.Rect, x, y float32) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}
