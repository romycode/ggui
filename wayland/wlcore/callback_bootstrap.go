package wlcore

import "fmt"

// Callback (wl_callback). Fichero PROVISIONAL: se sustituye por salida de
// waygenerator cuando exista. No lleva requests, solo el evento done, y se
// autodestruye tras done (el servidor manda su delete_id igualmente).
const opEvtCallbackDone = 0

type CallbackListener struct {
	Done func(callbackData uint32)
}

type Callback struct {
	ProxyBase
	listener CallbackListener
}

var _ Proxy = (*Callback)(nil)

// newCallback es el constructor que emitirá el generador para cada tipo: monta
// el ProxyBase y engancha OnClear, que es lo que ejecuta el clearListener()
// promocionado desde ProxyBase cuando Conn.destroy limpia el objeto.
func newCallback(id, version uint32, c *Conn) *Callback {
	cb := &Callback{ProxyBase: NewProxyBase(id, version, c)}
	cb.OnClear = func() { cb.listener = CallbackListener{} }
	return cb
}

func (cb *Callback) SetListener(l CallbackListener) { cb.listener = l }

// Dispatch: convención de wlcore.md para eventos con fd — si el campo del
// listener es nil, el fd decodificado hay que cerrarlo con DropFD(fd) en vez
// de dejarlo colgando, que si no se filtra un descriptor por evento. Ninguno
// de los eventos de los tres ficheros bootstrap lleva fds, así que hoy no
// aplica en ningún sitio; queda escrito aquí para quien saque la plantilla del
// generador de estos ficheros.
func (cb *Callback) Dispatch(opcode uint16, d *Decoder) error {
	switch opcode {
	case opEvtCallbackDone:
		data := d.Uint32()
		if err := d.Err(); err != nil {
			return err
		}
		if cb.listener.Done != nil {
			cb.listener.Done(data)
		}
	default:
		return fmt.Errorf("wlcore: opcode %d desconocido en wl_callback", opcode)
	}
	return nil
}
