package canvas

import (
	"errors"
	"fmt"
)

// ErrInvalidArgument is the sentinel every argument-validation failure
// wraps. Match it with errors.Is; the wrapped error's message carries the
// operation, the argument and the constraint that was broken.
var ErrInvalidArgument = errors.New("canvas: invalid argument")

// argError reports a bad argument to a specific operation. It formats its
// own message instead of wrapping ErrInvalidArgument with %w because the
// sentinel already carries the "canvas: " prefix, and %w would print it
// twice. Unwrap keeps errors.Is working.
type argError struct {
	op     string
	arg    string
	detail string
}

func (e *argError) Error() string {
	return fmt.Sprintf("canvas: %s: invalid argument %q: %s", e.op, e.arg, e.detail)
}

func (e *argError) Unwrap() error { return ErrInvalidArgument }

// invalidArg builds the error every validation path returns. op is the
// exported method name ("New", "FillCircle"), arg names the offending
// argument as the caller wrote it ("radius", "buffer.Stride"), and detail
// states the constraint and the value received.
func invalidArg(op, arg, detail string) error {
	return &argError{op: op, arg: arg, detail: detail}
}
