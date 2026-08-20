package wlcore

// Interface describe una interfaz del protocolo para efectos de Bind: su
// nombre en el wire, la versión máxima que soporta este binding, y la
// factory que construye el tipo concreto a partir de un ProxyBase.
type Interface[T Proxy] struct {
	Name       string
	MaxVersion uint32
	New        func(ProxyBase) T
}

// Bind negocia min(version, iface.MaxVersion), registra el objeto ANTES de
// mandar el request (el servidor puede empezar a mandarle eventos en
// cuanto lo procese), y manda el bind crudo por Registry.bindRaw.
//
// Método genérico (Go 1.27): antes era función libre porque Go no
// admitía parámetros de tipo en métodos; ya no aplica esa restricción.
func (r *Registry) Bind[T Proxy](name, version uint32, iface Interface[T]) (T, error) {
	v := version
	if v > iface.MaxVersion {
		v = iface.MaxVersion
	}
	id := r.Conn().NewID()
	obj := iface.New(NewProxyBase(id, v, r.Conn()))
	r.Conn().Register(obj)

	if err := r.bindRaw(name, iface.Name, v, id); err != nil {
		var zero T
		return zero, err
	}
	return obj, nil
}
