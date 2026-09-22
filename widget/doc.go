// Package widget holds reusable UI controls drawn with the canvas package.
//
// A widget is a small retained struct: it keeps the state that outlives a
// frame (hover, pressed, focused) and nothing else. It does not own a
// window, an event loop or a layout engine. The caller places it by setting
// Bounds, feeds it pointer events in the same logical units the canvas
// draws in, and asks it to draw into a canvas it already has.
//
// # Events
//
// Every event method returns whether something visible changed, so the
// caller can skip a repaint for the common case — a pointer moving inside
// a widget that is already hovered. Widgets never repaint themselves.
//
// # Focus
//
// A widget that takes the keyboard implements [Focusable] and is driven
// with [Key] values, which name keys by what they do because this package
// has no keysyms to name them by. A widget is told whether it is focused
// and never asks, so that it can be written without knowing its siblings:
// see [Focusable]. [Chain] is what knows them — it walks a tab order and
// keeps at most one widget focused — and a caller with focus rules of its
// own can leave it out and call SetFocused itself.
//
// # Text
//
// canvas fills shapes and has no text, and this package deliberately does
// not pick a font for the caller: a widget that shows text takes a [Font].
// That keeps rasterizer dependencies out of the library; a caller who has
// one adapts it to the interface.
//
// [TextField] is the one widget that both measures and scrolls text. It
// asks its Font only for the substring it shows, and only when the text,
// the caret or its width changed, so a frame of a field holding a hundred
// thousand characters costs what a frame of an empty one does.
//
// # Concurrency
//
// Widgets are not safe for concurrent use. They are driven from the same
// goroutine that pumps the Wayland connection, like everything else here.
package widget
