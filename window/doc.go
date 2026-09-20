// Package window opens a Wayland window and keeps it painted, on top of
// package eventloop.
//
// # Ownership
//
// Two goroutines share a window and never share its state. The Wayland
// goroutine, the one running eventloop.Loop.Run, owns the wlcore.Conn and
// every object made from it: it is the only one that makes requests. The UI
// goroutine owns the widgets, the canvases and the buffer pool's bookkeeping.
// The UI never calls into wlcore, apart from Done and Err on the connection;
// it queues a closure with Loop.Post for the Wayland goroutine to run. What
// the compositor says, and what a request produced, comes back with UI.Push
// or UI.Do, never as a return value.
package window
