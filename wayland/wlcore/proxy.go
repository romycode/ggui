package wlcore

// Proxy is what any protocol object satisfies, hand-written or generated.
type Proxy interface {
	ID() uint32
	// Dispatch returns an error if the message comes in malformed. It's
	// not recoverable: the stream is left misaligned, so the caller
	// closes the connection.
	Dispatch(opcode uint16, d *Decoder) error
	// clearListener removes the listener set by SetListener. It stays
	// unexported — a normal caller can't clear anyone else's listener —
	// but *ProxyBase implements it once, so any type that embeds
	// ProxyBase inherits it through promotion and satisfies Proxy, even
	// from another package (xdgshell, wlrlayershell). What each type
	// contributes is its OnClear.
	clearListener()
}

// ProxyBase carries what every protocol object has -- its id, the version
// it was created with, and the connection it belongs to -- plus the hook
// that clears its listener. Generated types embed it, and that embedding is
// what makes them satisfy [Proxy], including from another package: it
// supplies ID and the unexported clearListener through promotion.
type ProxyBase struct {
	id      uint32
	version uint32
	conn    *Conn

	// OnClear is what clearListener() runs: the concrete type's
	// constructor (generated code) sets it to a closure that zeroes its
	// listener field. nil means "this type has no listener". Exported
	// because generated code needs to be able to set it from outside
	// wlcore and it has to behave identically inside and outside; setting
	// it is the constructor's job, nobody else touches it.
	OnClear func()
}

// NewProxyBase builds the base of an object that has already been assigned
// id on c. Generated constructors call it. The two-step shape -- base
// first, concrete type second -- is what lets [Registry.Bind] construct an
// object of a package wlcore does not import, through [Interface.New].
func NewProxyBase(id, version uint32, c *Conn) ProxyBase {
	return ProxyBase{id: id, version: version, conn: c}
}

// ID returns the object id the compositor addresses this object by on the
// wire.
func (p *ProxyBase) ID() uint32 { return p.id }

// Version returns the version this object was created with: negotiated
// against the compositor for a global bound through [Registry.Bind],
// inherited from the parent for an object a request created. Generated
// requests check it before sending anything the compositor is too old to
// understand.
func (p *ProxyBase) Version() uint32 { return p.version }

func (p *ProxyBase) clearListener() {
	if p.OnClear != nil {
		p.OnClear()
	}
}

// Conn() is exported on purpose: extension packages can't touch the
// unexported field.
func (p *ProxyBase) Conn() *Conn { return p.conn }
