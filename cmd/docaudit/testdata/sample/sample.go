package sample

// Documented is a type with a doc comment.
type Documented struct {
	// Field carries one too.
	Field int
	Bare  int // a trailing comment also documents it
	Naked int
}

type Undocumented struct{}

// Method hangs off an exported type, so go doc shows it.
func (d *Documented) Method() {}

func (d *Documented) Undocumented() {}

// unexported is invisible to go doc, and so is everything on it.
type unexported struct{}

// Method is documented, but its receiver is not exported: not counted.
func (u *unexported) Method() {}

func (u *unexported) Bare() {}

// Generic exercises the type-parameter form of a receiver.
type Generic[T any] struct{}

func (g Generic[T]) Bare() {}

// Exported is a documented function.
func Exported() {}

func Bare() {}

// ErrOne and ErrTwo share the block's comment, which go doc renders for
// both.
var (
	ErrOne = 1
	ErrTwo = 2
)

const Unmentioned = 3

func unexportedFunc() {}
