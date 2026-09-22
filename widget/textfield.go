package widget

import "github.com/romycode/ggui/canvas"

// TextFieldStyle is the look of a [TextField]: the colors of the box and
// of what sits in it, plus the geometry the text is laid out against. The
// zero value draws nothing visible, so start from [DefaultTextFieldStyle].
type TextFieldStyle struct {
	// Fill is the background color.
	Fill canvas.Color
	// Text is the color of the field's contents.
	Text canvas.Color
	// Placeholder is the color of [TextField.Placeholder], shown while the
	// field is empty and unfocused.
	Placeholder canvas.Color
	// Caret is the caret's color. A transparent one hides the caret, and
	// then focusing the field reports no repaint, because none would look
	// different.
	Caret canvas.Color
	// DisabledFill replaces Fill while the field is disabled.
	DisabledFill canvas.Color
	// DisabledText replaces Text while the field is disabled.
	DisabledText canvas.Color

	// BorderColor is the resting outline color.
	BorderColor canvas.Color
	// FocusBorder replaces BorderColor while the field holds the focus and
	// is not disabled. Unlike [Button], a field shows the focus by its own
	// outline rather than by a ring: it has no fill states of its own for
	// a ring to have to read against.
	FocusBorder canvas.Color
	// Border is the outline width in logical units. Zero draws no outline.
	Border float32
	// Corner is the corner radius in logical units.
	Corner float32
	// Padding is the gap between the border and the text, in logical units.
	Padding float32
	// CaretWidth is the caret's width in logical units. Zero draws no
	// caret.
	CaretWidth float32
}

// DefaultTextFieldStyle returns a dark-theme style to go with
// [DefaultButtonStyle]: a sunken box that outlines in accent blue while it
// holds the focus.
func DefaultTextFieldStyle() TextFieldStyle {
	return TextFieldStyle{
		Fill:         canvas.Color{R: 0x2b, G: 0x30, B: 0x38, A: 0xff},
		Text:         canvas.Color{R: 0xe6, G: 0xe9, B: 0xef, A: 0xff},
		Placeholder:  canvas.Color{R: 0x6b, G: 0x72, B: 0x80, A: 0xff},
		Caret:        canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff},
		DisabledFill: canvas.Color{R: 0x24, G: 0x28, B: 0x2f, A: 0xff},
		DisabledText: canvas.Color{R: 0x6b, G: 0x72, B: 0x80, A: 0xff},
		BorderColor:  canvas.Color{R: 0x3a, G: 0x40, B: 0x49, A: 0xff},
		FocusBorder:  canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff},
		Border:       2,
		Corner:       8,
		Padding:      12,
		CaretWidth:   2,
	}
}

// TextField is a single-line text field: it shows one line of text with a
// caret the user moves freely, scrolls horizontally when the text does not
// fit, and shows Placeholder while it is empty and unfocused.
//
// TextField implements [Focusable]. It never takes the focus itself; the
// caller gives it, and a click is three calls in the caller's code:
//
//	if contains(field.Bounds, x, y) {
//	    changed := chain.Focus(field)
//	    changed = field.PointerDown(x, y) || changed
//	    if changed { redraw() }
//	}
//
// The caret does not blink by itself — this package has no clock. The
// application flips it with [TextField.SetCaretVisible] from a timer of
// its own; every edit, caret move and click turns it back on, which is the
// reset a user expects while typing.
//
// The caret moves by Unicode characters and not by grapheme clusters: a
// lone combining mark or a composed emoji takes more than one step.
//
// Font must be set before the field holds any text: the view caches
// measurements, and replacing the font afterwards is not noticed. Build a
// field with [NewTextField].
type TextField struct {
	// Bounds is where the field sits, in logical units. The caller sets it
	// — typically every frame, from its own layout. A width the caller
	// changed is noticed by the next call that reads it.
	Bounds canvas.Rect
	// Font measures and draws the text. A nil Font edits and draws the box
	// as usual but shows no text: with no widths, the anchor stays at the
	// start and the caret at the origin.
	Font Font
	// Style is the field's look. It may be changed at any time.
	Style TextFieldStyle
	// Placeholder is shown, in the placeholder color, while the field is
	// empty and unfocused.
	Placeholder string
	// OnChange is called with the new text after an edit the user made —
	// typing, Backspace, Delete. SetText does not call it: that is the
	// program's own change, and a listener that wrote back would loop. It
	// may be nil.
	OnChange func(text string)
	// OnSubmit is called with the current text when [KeyEnter] reaches the
	// focused field. It may be nil.
	OnSubmit func(text string)
	// Disabled makes the field ignore keys, text and clicks, refuse the
	// focus, and draw in its disabled colors.
	Disabled bool

	ed editor
	vw view
	// focused is whether the caller has given this field the keyboard
	// focus.
	focused bool
	// caretOn is the blink phase the application last set.
	caretOn bool
}

var _ Focusable = (*TextField)(nil)

// NewTextField returns an empty field with the given placeholder, drawn
// with font and [DefaultTextFieldStyle]. Set Bounds before use.
func NewTextField(placeholder string, font Font) *TextField {
	return &TextField{
		Placeholder: placeholder,
		Font:        font,
		Style:       DefaultTextFieldStyle(),
		caretOn:     true,
	}
}

// Text returns the field's contents. It allocates nothing: the string is
// rebuilt when the text changes, not when it is read.
func (t *TextField) Text() string { return t.ed.text }

// Caret returns the caret's position as a byte offset into
// [TextField.Text], always on a character boundary.
func (t *TextField) Caret() int { return t.ed.caret }

// Focused reports whether the field holds the keyboard focus, as the
// caller last set it.
func (t *TextField) Focused() bool { return t.focused }

// SetText replaces the contents with s, dropping its control characters
// and replacing invalid UTF-8 with U+FFFD, and puts the caret at the end.
// It reports whether a repaint would look different. It does not call
// OnChange.
func (t *TextField) SetText(s string) bool {
	t.ensure()
	before := t.visual()
	t.ed.setText(s)
	// The old anchor means nothing against new text; start from the left
	// and let the rules scroll to wherever the caret ended up.
	t.vw.anchor = 0
	t.sync()
	return t.visual() != before
}

// SetCaret moves the caret to offset, clamped into the text and rounded
// backwards to the character boundary at or before it, and reports whether
// a repaint would look different.
func (t *TextField) SetCaret(offset int) bool {
	t.ensure()
	before := t.visual()
	if t.ed.setCaret(offset) {
		t.sync()
	}
	return t.visual() != before
}

// Insert puts already-composed text at the caret and reports whether a
// repaint would look different. Control characters are dropped and invalid
// UTF-8 becomes U+FFFD, so a caller may feed it whatever its composer
// produced. OnChange is called if the text changed.
//
// A field that is not focused, or that is disabled, ignores it, so the
// caller may hand every keystroke to each widget it owns without first
// working out whose it is.
func (t *TextField) Insert(text string) bool {
	if !t.focused || t.Disabled {
		return false
	}
	t.ensure()
	before := t.visual()
	if !t.ed.insert(text) {
		return t.visual() != before
	}
	t.caretOn = true
	t.sync()

	changed := t.visual() != before
	if t.OnChange != nil {
		t.OnChange(t.ed.text)
	}
	return changed
}

// KeyDown handles a key press and reports whether a repaint would look
// different. A field that is not focused, or that is disabled, ignores
// every key.
//
// [KeyLeft], [KeyRight], [KeyHome] and [KeyEnd] move the caret;
// [KeyBackspace] and [KeyDelete] edit and call OnChange; [KeyEnter] calls
// OnSubmit. Text arrives through [TextField.Insert] and not from here, so
// [KeySpace] is ignored like every other key the field does not act on,
// and nothing latches — a key repeat is harmless.
//
// Enter reports false even though it submitted: the bool answers only
// "would a repaint look different", and a caller whose OnSubmit changed
// the screen repaints on that account, exactly as it does after a click.
func (t *TextField) KeyDown(k Key) bool {
	if !t.focused || t.Disabled {
		return false
	}
	if k == KeyEnter {
		if t.OnSubmit != nil {
			t.OnSubmit(t.ed.text)
		}
		return false
	}

	t.ensure()
	before := t.visual()

	acted, edited := false, false
	switch k {
	case KeyLeft:
		acted = t.ed.left()
	case KeyRight:
		acted = t.ed.right()
	case KeyHome:
		acted = t.ed.home()
	case KeyEnd:
		acted = t.ed.end()
	case KeyBackspace:
		acted, edited = t.ed.backspace(), true
	case KeyDelete:
		acted, edited = t.ed.delete(), true
	default:
		return false
	}
	if !acted {
		return t.visual() != before
	}
	t.caretOn = true
	t.sync()

	changed := t.visual() != before
	if edited && t.OnChange != nil {
		t.OnChange(t.ed.text)
	}
	return changed
}

// KeyUp handles a key release and reports whether a repaint would look
// different. Nothing latches in a text field, so it never does anything;
// it is here because [Focusable] asks for it.
func (t *TextField) KeyUp(Key) bool { return false }

// SetFocused gives the field the keyboard focus or takes it away, and
// reports whether its appearance changed. Gaining the focus always leaves
// the caret showing, so a field the application had blinked off does not
// come back invisible.
//
// A disabled field refuses the focus, so a caller walking a focus order
// can offer it to each widget in turn and let them decline.
func (t *TextField) SetFocused(focused bool) bool {
	if focused && t.Disabled {
		return false
	}
	before := t.visual()
	if focused && !t.focused {
		t.caretOn = true
	}
	t.focused = focused
	return t.visual() != before
}

// SetCaretVisible sets the caret's blink phase and reports whether a
// repaint would look different. On a field that is not focused it stores
// the value and reports false — there is no caret to show — and focusing
// the field turns it back on anyway.
func (t *TextField) SetCaretVisible(v bool) bool {
	before := t.visual()
	t.caretOn = v
	return t.visual() != before
}

// PointerDown handles a primary-button press at (x, y) and reports whether
// a repaint would look different. A press outside Bounds is not this
// field's — pressing the button next to it must not move this caret — and
// a disabled field ignores every press.
//
// It puts the caret where the click landed and nothing else: it does not
// focus the field, because a widget that focused itself would leave two of
// them focused at once. It works whether or not the field is focused,
// since the application focuses and clicks on the same press.
func (t *TextField) PointerDown(x, y float32) bool {
	if t.Disabled || !contains(t.Bounds, x, y) {
		return false
	}
	t.ensure()
	before := t.visual()
	t.caretOn = true
	t.ed.setCaret(t.vw.hit(t.Font, t.ed.text, x-t.textX()))
	t.sync()
	return t.visual() != before
}

// Draw paints the field into cv. It measures nothing and allocates
// nothing: the visible run is a substring of the cached text, and its
// geometry was computed when the text last changed.
//
// Bounds must be a valid canvas rectangle: a negative size is recorded as
// a canvas error like any other bad argument. A valid one never produces
// an invalid inner rectangle — a field too small to hold its border and
// padding draws its box and nothing else.
func (t *TextField) Draw(cv *canvas.Canvas) {
	t.ensure()
	st := &t.Style

	fill, text := st.Fill, st.Text
	outline := st.BorderColor
	if t.Disabled {
		fill, text = st.DisabledFill, st.DisabledText
	} else if t.focused {
		outline = st.FocusBorder
	}

	cv.FillRoundedRect(t.Bounds, st.Corner, fill)
	if st.Border > 0 {
		cv.StrokeRoundedRect(t.Bounds, st.Corner, st.Border, outline)
	}

	innerW := t.vw.innerW
	if !(innerW > 0) {
		return
	}
	border, padding := t.metrics()
	x := t.textX()
	// The clip is the text area, full height between the borders: the
	// visible run is cut here and not by Font.Draw, which the Font
	// contract does not promise will clip at all.
	clip := canvas.Rect{
		X:      x,
		Y:      t.Bounds.Y + border,
		Width:  innerW,
		Height: max(t.Bounds.Height-2*border, 0),
	}

	if t.Font != nil {
		at := canvas.Point{X: x, Y: t.Bounds.Y + t.Bounds.Height/2}
		if t.placeholderShown() {
			// Unlike the text below, Placeholder is not fitted to the
			// visible run: doing that would mean measuring here, and Draw
			// measures nothing. clip is what keeps it from painting past
			// the text area; Placeholder is the application's own short,
			// static string, not something that grows with typing.
			t.Font.Draw(cv, at, t.Placeholder, st.Placeholder, clip)
		} else if run := t.visibleText(); run != "" {
			t.Font.Draw(cv, at, run, text, clip)
		}
	}

	if t.caretShown() {
		cv.FillRect(canvas.Rect{
			X:      x + t.vw.caretX,
			Y:      t.Bounds.Y + border + padding/2,
			Width:  st.CaretWidth,
			Height: max(t.Bounds.Height-2*(border+padding/2), 0),
		}, st.Caret)
	}
}

// fieldVisual is everything about the field a repaint would show. Every
// method compares it before and after, so the "did anything change" they
// report is exactly "would a repaint look different" — and adding state
// later (hover, a selection) does not mean rewriting a rule per method.
type fieldVisual struct {
	// version stands for the text itself: the same version is the same
	// bytes.
	version uint64
	// anchor and caretX are where the visible run starts and where the
	// caret sits within it. caretX is zero while the caret is not shown,
	// so a caret nobody can see cannot report a move.
	anchor int
	caretX float32
	// innerW is the text area's width, tracked here for completeness. In
	// practice every method that reads geometry calls ensure() before
	// taking this snapshot, so innerW has already caught up with any width
	// change by the time before and after are compared — a resize is
	// visible through anchor and caretX instead.
	innerW float32
	// caret, placeholder and focus are what is on screen rather than what
	// is set: a caret the style hides, or a placeholder an empty field is
	// too focused to show, must not be claimed as a repaint.
	caret       bool
	placeholder bool
	focus       bool
	disabled    bool
}

// visual snapshots the field's current fieldVisual, for a caller to compare
// against a snapshot taken before some change: t.visual() != before is the
// one predicate every exported method's returned bool comes from.
func (t *TextField) visual() fieldVisual {
	v := fieldVisual{
		version:     t.ed.version,
		anchor:      t.vw.anchor,
		innerW:      t.vw.innerW,
		caret:       t.caretShown(),
		placeholder: t.placeholderShown(),
		focus:       t.focused && !t.Disabled,
		disabled:    t.Disabled,
	}
	if v.caret {
		v.caretX = t.vw.caretX
	}
	return v
}

// caretShown is the single predicate behind both the drawing and the
// change reporting, so the two cannot drift into disagreeing about whether
// the caret is there.
func (t *TextField) caretShown() bool {
	st := &t.Style
	return t.focused && t.caretOn && !t.Disabled && st.CaretWidth > 0 && st.Caret.A > 0
}

// placeholderShown is the same for the placeholder, which stands in for
// the text while the field is empty and nobody is typing in it.
func (t *TextField) placeholderShown() bool {
	return t.ed.text == "" && !t.focused && t.Placeholder != ""
}

// metrics returns the border and padding the geometry is built from, with
// anything negative or not a number taken as zero.
func (t *TextField) metrics() (border, padding float32) {
	return budget(t.Style.Border), budget(t.Style.Padding)
}

// textX is the left edge of the text area, where the first visible
// character starts and which caretX is measured from.
func (t *TextField) textX() float32 {
	border, padding := t.metrics()
	return t.Bounds.X + border + padding
}

// innerWidth is the width of the text area: Bounds less the border and the
// padding on both sides, and never negative.
func (t *TextField) innerWidth() float32 {
	border, padding := t.metrics()
	return budget(t.Bounds.Width - 2*(border+padding))
}

// visibleText is the run this frame draws: a substring of the cached text,
// which costs nothing to take.
func (t *TextField) visibleText() string {
	s := t.ed.text
	a := min(max(t.vw.anchor, 0), len(s))
	e := min(max(t.vw.visEnd, a), len(s))
	return s[a:e]
}

// ensure re-applies the view rules if the text area's width has changed
// since they last ran. Bounds and Style are public fields the caller
// rewrites whenever it likes, so this is how a resize reaches the view;
// every method that reads the geometry calls it first. The rules cost what
// is on screen, so the frame a field is resized in is no dearer than any
// other.
func (t *TextField) ensure() {
	if t.innerWidth() != t.vw.innerW {
		t.sync()
	}
}

// sync applies the view rules to the current text, caret and width.
func (t *TextField) sync() {
	innerW := t.innerWidth()
	// The caret is kept inside the area less its own width, so that a
	// caret at the end of the visible run is drawn inside the field and
	// not on its border.
	t.vw.sync(t.Font, t.ed.text, t.ed.caret, budget(innerW-budget(t.Style.CaretWidth)), innerW)
}
