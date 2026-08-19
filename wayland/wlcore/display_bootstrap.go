package wlcore

import "fmt"

// Display (wl_display), el objeto 1. Fichero PROVISIONAL: se sustituye por
// salida de waygenerator cuando exista.
const (
	opReqDisplaySync        = 0
	opReqDisplayGetRegistry = 1

	opEvtDisplayError    = 0
	opEvtDisplayDeleteID = 1
)

// DisplayListener no lo debe usar el usuario directamente: Connect() lo
// engancha internamente para el reciclado de ids y la detección de errores
// de protocolo. La API pública es Conn.OnError (Task 12).
type DisplayListener struct {
	Error    func(objectID, code uint32, msg string)
	DeleteID func(id uint32)
}

type Display struct {
	ProxyBase
	listener DisplayListener
}

func (d *Display) SetListener(l DisplayListener) { d.listener = l }

func (d *Display) clearListener() { d.listener = DisplayListener{} }

func (d *Display) Sync() (*Callback, error) {
	id := d.Conn().NewID()
	cb := &Callback{ProxyBase: NewProxyBase(id, d.Version(), d.Conn())}
	d.Conn().Register(cb)

	e := NewEncoder().ID(id)
	if err := d.Conn().Send(d.ID(), opReqDisplaySync, e); err != nil {
		return nil, err
	}
	return cb, nil
}

func (d *Display) GetRegistry() (*Registry, error) {
	id := d.Conn().NewID()
	reg := &Registry{ProxyBase: NewProxyBase(id, d.Version(), d.Conn())}
	d.Conn().Register(reg)

	e := NewEncoder().ID(id)
	if err := d.Conn().Send(d.ID(), opReqDisplayGetRegistry, e); err != nil {
		return nil, err
	}
	return reg, nil
}

func (d *Display) Dispatch(opcode uint16, dec *Decoder) error {
	switch opcode {
	case opEvtDisplayError:
		objectID := dec.ID()
		code := dec.Uint32()
		msg := dec.String()
		if err := dec.Err(); err != nil {
			return err
		}
		if d.listener.Error != nil {
			d.listener.Error(objectID, code, msg)
		}
	case opEvtDisplayDeleteID:
		id := dec.ID()
		if err := dec.Err(); err != nil {
			return err
		}
		if d.listener.DeleteID != nil {
			d.listener.DeleteID(id)
		}
	default:
		return fmt.Errorf("wlcore: opcode %d desconocido en wl_display", opcode)
	}
	return nil
}
