package xmlmodel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

type Protocol struct {
	Name       string      `xml:"name,attr"`
	Interfaces []Interface `xml:"interface"`
	File       string      `xml:"-"`
}

type Interface struct {
	Name     string    `xml:"name,attr"`
	Version  int       `xml:"version,attr"`
	Requests []Request `xml:"request"`
	Events   []Event   `xml:"event"`
	Enums    []Enum    `xml:"enum"`
	Line     int       `xml:"-"`
}

type Request struct {
	Name  string `xml:"name,attr"`
	Type  string `xml:"type,attr"`
	Since int    `xml:"since,attr"`
	Args  []Arg  `xml:"arg"`
}

type Event struct {
	Name  string `xml:"name,attr"`
	Type  string `xml:"type,attr"`
	Since int    `xml:"since,attr"`
	Args  []Arg  `xml:"arg"`
}

type Arg struct {
	Name      string `xml:"name,attr"`
	Type      string `xml:"type,attr"`
	Interface string `xml:"interface,attr"`
	Enum      string `xml:"enum,attr"`
	AllowNull bool   `xml:"allow-null,attr"`
}

type Enum struct {
	Name     string  `xml:"name,attr"`
	Bitfield bool    `xml:"bitfield,attr"`
	Entries  []Entry `xml:"entry"`
}

type Entry struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// manifest fichero -> nombre de protocolo, en el orden en que se procesan.
// xdg-shell.xml y wlr-layer-shell.xml se añaden cuando esas fases empiecen;
// listarlos aquí ahora, sin consumirlos, no compra nada.
var manifest = []string{"wayland.xml"}

// ParseAll lee los ficheros del manifiesto interno desde dir y los
// deserializa. No interpreta nada del contenido: los únicos errores
// posibles aquí son "el fichero no existe" o "el XML no es XML".
func ParseAll(dir string) ([]Protocol, error) {
	protos := make([]Protocol, 0, len(manifest))
	for _, name := range manifest {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("xmlmodel: leyendo %s: %w", path, err)
		}
		var p Protocol
		if err := xml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("xmlmodel: %s: XML inválido: %w", path, err)
		}
		p.File = name
		for i := range p.Interfaces {
			p.Interfaces[i].Line = interfaceLine(data, p.Interfaces[i].Name)
			normalizeSince(&p.Interfaces[i])
		}
		protos = append(protos, p)
	}
	return protos, nil
}

func normalizeSince(iface *Interface) {
	for i := range iface.Requests {
		if iface.Requests[i].Since == 0 {
			iface.Requests[i].Since = 1
		}
	}
	for i := range iface.Events {
		if iface.Events[i].Since == 0 {
			iface.Events[i].Since = 1
		}
	}
}

// interfaceLine busca `<interface name="ifaceName"` en el fuente crudo y
// cuenta saltos de línea hasta ahí. Devuelve 0 si no la encuentra (no
// debería pasar: el nombre viene de haber parseado ese mismo interface).
func interfaceLine(data []byte, ifaceName string) int {
	needle := []byte(`<interface name="` + ifaceName + `"`)
	idx := bytes.Index(data, needle)
	if idx < 0 {
		return 0
	}
	return bytes.Count(data[:idx], []byte("\n")) + 1
}
