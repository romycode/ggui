package wlcore

// Interface describes a protocol interface for Bind purposes: its name on
// the wire, the max version this binding supports, and the factory that
// builds the concrete type from a ProxyBase.
type Interface[T Proxy] struct {
	// Name is the interface name as it travels on the wire, the same
	// string wl_registry.global announces.
	Name string
	// MaxVersion is the highest version this binding implements. Bind
	// negotiates it down to whatever the compositor advertises; it is
	// never what gets sent verbatim.
	MaxVersion uint32
	// New builds the concrete type from a base. It is the generated
	// constructor, and it is how wlcore constructs objects belonging to
	// packages it does not import.
	New func(ProxyBase) T
}

// Bind negotiates min(version, iface.MaxVersion), registers the object
// BEFORE sending the request (the server can start sending it events as
// soon as it processes it), and sends the raw bind via Registry.bindRaw.
//
// Generic method (Go 1.27): it used to be a free function because Go
// didn't allow type parameters on methods; that restriction no longer
// applies.
func (r *Registry) Bind[T Proxy](name, version uint32, iface Interface[T]) (T, error) {
	v := min(version, iface.MaxVersion)
	id := r.Conn().NewID()
	obj := iface.New(NewProxyBase(id, v, r.Conn()))
	r.Conn().Register(obj)

	if err := r.bindRaw(name, iface.Name, v, id); err != nil {
		var zero T
		return zero, err
	}
	return obj, nil
}
