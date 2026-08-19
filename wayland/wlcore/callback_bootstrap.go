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

func (cb *Callback) SetListener(l CallbackListener) { cb.listener = l }

func (cb *Callback) clearListener() { cb.listener = CallbackListener{} }

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
