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

func NewProxyBase(id, version uint32, c *Conn) ProxyBase {
	return ProxyBase{id: id, version: version, conn: c}
}

func (p *ProxyBase) ID() uint32      { return p.id }
func (p *ProxyBase) Version() uint32 { return p.version }

func (p *ProxyBase) clearListener() {
	if p.OnClear != nil {
		p.OnClear()
	}
}

// Conn() is exported on purpose: extension packages can't touch the
// unexported field.
func (p *ProxyBase) Conn() *Conn { return p.conn }
