package symbols

import (
	"github.com/romycode/ggui/cmd/waygenerator/internal/goname"
	"github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel"
)

// packageOf is the manifest file -> Go package mapping. wlr-layer-shell.xml
// is reserved for when that phase gets generated; xmlmodel.ParseAll doesn't
// read it yet, so that branch is never exercised at this phase.
var packageOf = map[string]string{
	"wayland.xml":             "wlcore",
	"xdg-shell.xml":           "xdgshell",
	"wlr-layer-shell.xml":     "wlrlayershell",
	"viewporter.xml":          "viewporter",
	"fractional-scale-v1.xml": "fractionalscale",
	"tablet-v2.xml":           "tablet",
}

// prefixOf is the protocol prefix stripped from the XML name to derive the
// Go name.
var prefixOf = map[string]string{
	"wlcore":          "wl_",
	"xdgshell":        "xdg_",
	"wlrlayershell":   "wlr_",
	"viewporter":      "wp_",
	"fractionalscale": "wp_",
	"tablet":          "zwp_",
}

// suffixOf is the trailing "_vN" stripped from the XML name after the
// prefix, for versioned extensions (zwp_tablet_v2 -> tablet, not tablet_v2
// -- the version is part of the package name, not the type; see
// waygenerator.md). Absent or empty if the protocol doesn't version its
// interface names (xdg-shell, viewporter).
var suffixOf = map[string]string{
	"fractionalscale": "_v1",
	"tablet":          "_v2",
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
		suffix := suffixOf[pkg]
		for _, iface := range p.Interfaces {
			name := goname.StripSuffix(goname.StripPrefix(iface.Name, prefix), suffix)
			goType := goname.Pascal(name)
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
