// Package wlcore is a Wayland client runtime written in pure Go, plus the
// bindings for the core wayland.xml protocol.
//
// There is no cgo and no libwayland-client. [Connect] dials the compositor
// socket named by $WAYLAND_DISPLAY under $XDG_RUNTIME_DIR and speaks the
// wire protocol directly.
//
// Files ending in .gen.go are produced by waygenerator from
// protocols/wayland.xml and must not be edited by hand. Everything else --
// the connection, the wire encoder and decoder, object lifecycle, [Fixed]
// -- is hand-written, and is the contract every generated package depends
// on.
//
// # Pumping
//
// Nothing arrives until someone reads the socket. [Conn.Dispatch] reads
// once and dispatches every complete message that came in; [Conn.Run]
// loops until the connection dies; [Conn.Roundtrip] sends a
// wl_display.sync and pumps until its callback fires, which is how a
// client waits for the compositor to have processed everything sent so
// far.
//
// The whole Conn API -- requests, SetListener, dispatching -- must be used
// from a single goroutine. That contract is documented, not enforced: no
// mutex guards the object map or the read buffers. Reentrant dispatch is
// also forbidden, so a listener must not call Roundtrip.
//
// # Events
//
// Each proxy takes a listener that is a struct of function fields, not a
// channel. A nil field means the event is ignored. Listeners must be
// installed before the first Dispatch, or early events are lost --
// including wl_display.error, which is why [Conn.OnError] carries the same
// warning.
//
// # Errors
//
// A protocol error is terminal. It is reported to the [Conn.OnError]
// callback, stored as a [ProtocolError], and closes the connection:
// [Conn.Done] unblocks and [Conn.Err] holds the first error, which
// [Conn.Close] will not overwrite.
//
// # File descriptors
//
// Events carrying a file descriptor hand ownership to the listener, which
// must close it. Descriptors received but never consumed are closed by
// [Conn.Run] on the way out; a caller pumping by hand with Dispatch calls
// [Conn.DrainFDs] instead.
//
// # Binding globals
//
// Globals are discovered through wl_registry and bound with
// [Registry.Bind], which takes the typed descriptor a generated package
// exports -- [CompositorInterface], [ShmInterface], and the equivalents in
// the extension packages -- and returns the concrete proxy type.
package wlcore
