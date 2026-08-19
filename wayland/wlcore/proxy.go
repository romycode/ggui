package wlcore

// Proxy es lo que satisface cualquier objeto del protocolo, a mano o
// generado.
type Proxy interface {
	ID() uint32
	// Dispatch devuelve error si el mensaje viene malformado. No es
	// recuperable: el stream queda desalineado, así que el llamante
	// cierra la conexión.
	Dispatch(opcode uint16, d *Decoder) error
	// clearListener quita el listener puesto por SetListener; cada tipo
	// generado lo implementa poniendo su campo listener a su cero.
	clearListener()
}

type ProxyBase struct {
	id      uint32
	version uint32
	conn    *Conn
}

func NewProxyBase(id, version uint32, c *Conn) ProxyBase {
	return ProxyBase{id: id, version: version, conn: c}
}

func (p *ProxyBase) ID() uint32      { return p.id }
func (p *ProxyBase) Version() uint32 { return p.version }

// Conn() es exportado a propósito: los paquetes de extensión no pueden
// tocar el campo no exportado.
func (p *ProxyBase) Conn() *Conn { return p.conn }
