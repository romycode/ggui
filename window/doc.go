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
// # Input, focus and size
//
// Key, pointer, focus and resize callbacks run on the UI goroutine, in the
// order the compositor produced them. Keys and pointer events that arrive
// while the application is still loading are dropped, not kept, but focus is
// remembered: when the [Content] is installed it is told the window's current
// size with OnResize, then OnKeyboardFocus(true) and OnPointerFocus(true) for
// the devices that already have focus. After that OnResize runs whenever the
// logical size changes, before the next Paint at that size, and the focus
// callbacks on every change. A configure whose width or height is zero leaves
// that dimension as it was, as the xdg_shell protocol says a client should.
//
// # Closing and failure
//
// A window closes when the compositor asks for it or the application calls
// [Window.Close], from any goroutine; [Run] then returns nil and the context
// from [Window.Context] is cancelled. Run waits for the UI goroutine but never
// for init, which may be parked in application code: init is told to stop
// through the context, and whatever it returns late is dropped.
//
// If init returns an error, or panics, the window stays open showing the
// failure screen, and Run returns that error when the window is closed; the
// panic is logged with its stack. It takes priority over how the connection
// ended. The window's own buffers can fail too: while the application is still
// loading that is a failed start; once it runs, a size the buffers cannot be
// built for is refused and the window carries on at the size it had, and a
// buffer that cannot be created closes the window with the error.
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
