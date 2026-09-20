// Package window opens a Wayland window and keeps it painted, on top of
// package eventloop.
//
// An application calls [Run] with a [Config] and an init function, and writes
// nothing else about Wayland: Run binds the globals, opens the surface with
// its xdg role, answers the configure handshake, keeps a pool of shm buffers
// and drives the frame clock.
//
// # Startup
//
// The window opens before the application is ready. init runs on a goroutine
// of its own while the window is already on screen showing a loader, and
// returns the [Content] — the callbacks that paint it and receive its input —
// which is installed when it is done. An application never sees the loader,
// the phases, the surface or the scale: it paints in logical units into the
// canvas it is handed.
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
