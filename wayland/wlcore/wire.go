package wlcore

import "encoding/binary"

// Encoder serializa argumentos Wayland al wire format. No sabe nada de
// mensajes (objectID, opcode, header) — solo primitivas del wire. El
// ensamblado del header es responsabilidad de Conn.Send.
//
// Invariante que sostiene todo el padding: e.buf mide un múltiplo de 4 al
// entrar a cada método. Uint32/ID/Int32/Fixed escriben siempre 4 bytes
// exactos, así que la mantienen sola; String/Array/StringOpt la restauran
// ellos mismos rellenando hasta el siguiente múltiplo de 4.
type Encoder struct {
	buf []byte
}

func NewEncoder() *Encoder { return &Encoder{} }

func (e *Encoder) Uint32(v uint32) *Encoder {
	e.buf = binary.NativeEndian.AppendUint32(e.buf, v)
	return e
}

func (e *Encoder) ID(id uint32) *Encoder  { return e.Uint32(id) }
func (e *Encoder) Int32(v int32) *Encoder { return e.Uint32(uint32(v)) }
func (e *Encoder) Fixed(v Fixed) *Encoder { return e.Uint32(uint32(v)) }

func (e *Encoder) String(s string) *Encoder {
	e.Uint32(uint32(len(s) + 1)) // la longitud incluye el nul
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
	return e
}

// StringOpt es el string con allow-null="true": el wire format del string
// nulo es longitud 0 y cero bytes de datos, ni nul ni padding.
func (e *Encoder) StringOpt(s *string) *Encoder {
	if s == nil {
		return e.Uint32(0)
	}
	return e.String(*s)
}

func (e *Encoder) Array(data []byte) *Encoder {
	e.Uint32(uint32(len(data)))
	e.buf = append(e.buf, data...)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
	return e
}

func (e *Encoder) Bytes() []byte { return e.buf }
