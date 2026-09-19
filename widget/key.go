package widget

// Key is a keyboard key named by what it does rather than by where it sits.
// This package depends only on canvas, so it has no keysyms and no
// keycodes: the caller translates, exactly as it already translates pointer
// positions into logical units.
//
// The set is deliberately small — a key no widget acts on has no constant
// here. Two of them, [KeyTab] and [KeyBacktab], move the focus instead of
// acting on a widget: no widget reacts to them, since none knows it has
// siblings, and they are meant for [Chain]; see [Focusable].
type Key uint8

const (
	// KeyNone is the zero value and no key at all. Widgets ignore it, so a
	// caller that could not map a keysym can pass it through unguarded.
	KeyNone Key = iota

	// KeySpace arms the focused widget, which activates when the key is
	// released — the same press-then-release rule the pointer follows.
	KeySpace

	// KeyEnter activates the focused widget on the press. Both Return and
	// the keypad's Enter belong here.
	KeyEnter

	// KeyEscape abandons an activation the keyboard has started, the way
	// dragging off a pressed widget abandons a pointer one.
	KeyEscape

	// KeyTab moves the focus to the next widget of a [Chain].
	KeyTab

	// KeyBacktab moves the focus to the previous widget of a [Chain].
	//
	// Which of the two a press is, is the caller's call and not something
	// this package can work out: it has no modifier state, and a shifted
	// Tab reaches a client either as the keysym ISO_Left_Tab or as plain
	// Tab with Shift held, depending on the keymap.
	KeyBacktab
)
