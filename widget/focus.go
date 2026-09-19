package widget

// Focusable is a widget that can hold the keyboard focus and be driven from
// [Key] events.
//
// The focus meant here is the client's own. Wayland gives a *surface* the
// keyboard; which control inside that surface owns it is entirely the
// caller's business, and this package does not decide it. A widget is told
// whether it is focused and never asks — that is what lets one be written,
// and tested, without knowing its siblings exist.
//
// The caller therefore owns the focus rules: which widget starts focused,
// what a press moves it to, what a Tab key does, and that at most one
// widget holds it. [Button.PointerDown] in particular does not focus the
// button it lands on, because a widget that focused itself would leave two
// of them focused at once. [Chain] is a ready-made arbiter for the usual
// rules, which a caller takes or replaces.
type Focusable interface {
	// Focused reports whether the widget holds the focus.
	Focused() bool

	// SetFocused gives the widget the focus or takes it away, and reports
	// whether the widget's appearance changed. Taking it away abandons any
	// activation the keyboard had started: the release will go elsewhere,
	// so it could never complete here.
	//
	// A widget may refuse the focus — a disabled one does — in which case
	// Focused stays false.
	SetFocused(focused bool) bool

	// KeyDown handles a key press and reports whether the appearance
	// changed. A widget that is not focused ignores it.
	KeyDown(k Key) bool

	// KeyUp handles a key release and reports whether the appearance
	// changed.
	KeyUp(k Key) bool
}
