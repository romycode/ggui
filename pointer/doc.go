// Package pointer translates Wayland pointer input into semantic position,
// button, click and drag events.
//
// A [Pointer] calls its handlers synchronously from the wlcore dispatch
// goroutine. The first version does not interpret scrolling or touchpad
// gestures.
package pointer
