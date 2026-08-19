package wlcore

// Proxy es lo que satisface cualquier objeto del protocolo, a mano o
// generado.
type Proxy interface {
	ID() uint32
	// Dispatch devuelve error si el mensaje viene malformado. No es
	// recuperable: el stream queda desalineado, así que el llamante
	// cierra la conexión.
	Dispatch(opcode uint16, d *Decoder) error
	// clearListener quita el listener puesto por SetListener. Sigue sin
	// exportarse — un llamante normal no puede limpiar el listener de
	// nadie — pero la implementa *ProxyBase una sola vez, así que
	// cualquier tipo que embeba ProxyBase la hereda promocionada y
	// satisface Proxy, también desde otro paquete (xdgshell,
	// wlrlayershell). Lo que cada tipo aporta es su OnClear.
	clearListener()
}

type ProxyBase struct {
	id      uint32
	version uint32
	conn    *Conn

	// OnClear es lo que ejecuta clearListener(): el constructor del tipo
	// concreto (código generado) le pone una closure que deja su campo
	// listener a su cero. nil significa "este tipo no tiene listener".
	// Exportado porque el código generado tiene que poder fijarlo desde
	// fuera de wlcore y tiene que ser idéntico dentro y fuera; fijarlo es
	// cosa del constructor, nadie más lo toca.
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

// Conn() es exportado a propósito: los paquetes de extensión no pueden
// tocar el campo no exportado.
func (p *ProxyBase) Conn() *Conn { return p.conn }
