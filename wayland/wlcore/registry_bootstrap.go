package wlcore

import "fmt"

// Registry (wl_registry). Fichero PROVISIONAL: se sustituye por salida de
// waygenerator cuando exista.
const opReqRegistryBind = 0

const (
	opEvtRegistryGlobal       = 0
	opEvtRegistryGlobalRemove = 1
)

type RegistryListener struct {
	Global       func(name uint32, iface string, version uint32)
	GlobalRemove func(name uint32)
}

type Registry struct {
	ProxyBase
	listener RegistryListener
}

func (r *Registry) SetListener(l RegistryListener) { r.listener = l }

func (r *Registry) clearListener() { r.listener = RegistryListener{} }

// bindRaw manda el request bind. new_id sin atributo interface se
// serializa como tres valores — nombre de interfaz, versión, id — no un
// u32 suelto.
func (r *Registry) bindRaw(name uint32, iface string, version, newID uint32) error {
	e := NewEncoder().Uint32(name).String(iface).Uint32(version).ID(newID)
	return r.Conn().Send(r.ID(), opReqRegistryBind, e)
}

func (r *Registry) Dispatch(opcode uint16, d *Decoder) error {
	switch opcode {
	case opEvtRegistryGlobal:
		name := d.Uint32()
		iface := d.String()
		version := d.Uint32()
		if err := d.Err(); err != nil {
			return err
		}
		if r.listener.Global != nil {
			r.listener.Global(name, iface, version)
		}
	case opEvtRegistryGlobalRemove:
		name := d.Uint32()
		if err := d.Err(); err != nil {
			return err
		}
		if r.listener.GlobalRemove != nil {
			r.listener.GlobalRemove(name)
		}
	default:
		return fmt.Errorf("wlcore: opcode %d desconocido en wl_registry", opcode)
	}
	return nil
}
