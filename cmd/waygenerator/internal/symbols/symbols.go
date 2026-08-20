package symbols

import (
	"github.com/romycode/ggui/cmd/waygenerator/internal/goname"
	"github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel"
)

// packageOf es el manifiesto fichero -> paquete Go. xdg-shell.xml y
// wlr-layer-shell.xml están reservados para cuando esas fases se generen;
// xmlmodel.ParseAll no los lee todavía, así que esas ramas nunca se
// ejercitan en esta fase.
var packageOf = map[string]string{
	"wayland.xml":         "wlcore",
	"xdg-shell.xml":       "xdgshell",
	"wlr-layer-shell.xml": "wlrlayershell",
}

// prefixOf es el prefijo de protocolo que se quita del nombre XML para
// sacar el nombre Go. Solo wl_ se ejercita esta fase.
var prefixOf = map[string]string{
	"wlcore":        "wl_",
	"xdgshell":      "xdg_",
	"wlrlayershell": "wlr_",
}

type EnumInfo struct {
	GoName   string
	Bitfield bool
}

type Entry struct {
	XMLName    string
	GoPackage  string
	GoType     string
	MaxVersion int
	ReqOpcodes map[string]int
	EvtOpcodes map[string]int
	Enums      map[string]EnumInfo
}

type Table map[string]Entry

func Build(protos []xmlmodel.Protocol) Table {
	table := make(Table)
	for _, p := range protos {
		pkg := packageOf[p.File]
		prefix := prefixOf[pkg]
		for _, iface := range p.Interfaces {
			goType := goname.Pascal(goname.StripPrefix(iface.Name, prefix))
			e := Entry{
				XMLName:    iface.Name,
				GoPackage:  pkg,
				GoType:     goType,
				MaxVersion: iface.Version,
				ReqOpcodes: make(map[string]int, len(iface.Requests)),
				EvtOpcodes: make(map[string]int, len(iface.Events)),
				Enums:      make(map[string]EnumInfo, len(iface.Enums)),
			}
			for i, r := range iface.Requests {
				e.ReqOpcodes[r.Name] = i
			}
			for i, ev := range iface.Events {
				e.EvtOpcodes[ev.Name] = i
			}
			for _, en := range iface.Enums {
				e.Enums[en.Name] = EnumInfo{
					GoName:   goType + goname.Pascal(en.Name),
					Bitfield: en.Bitfield,
				}
			}
			table[iface.Name] = e
		}
	}
	return table
}
