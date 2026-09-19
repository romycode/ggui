// Package eventloop keeps the UI off the Wayland socket, and the socket off
// the UI.
//
// A wlcore.Conn belongs to one goroutine, the one that reads the socket and
// dispatches what arrives. If that goroutine also paints, a slow frame delays
// keyboard and pointer input and the reply to xdg_wm_base.ping, and the UI
// only ever runs when the compositor sends something. This package splits the
// two: a Wayland goroutine that owns the connection, and a UI goroutine that
// owns widgets and canvas, talking only by messages.
//
// # Ownership
//
// Nothing here relaxes wlcore's contract. Conn, and every object created from
// it, is touched only by the Wayland goroutine. The UI never calls into
// wlcore; it queues a closure for the Wayland goroutine to run, and what the
// compositor says comes back to the UI as an event, not as a return value.
// The reverse holds too: the Wayland goroutine never waits on the UI.
//
// The one piece both goroutines touch is the queue between them, which
// carries its own lock and whose insertions never block.
package eventloop
