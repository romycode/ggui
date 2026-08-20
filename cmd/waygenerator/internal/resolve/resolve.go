package resolve

import (
	"sort"

	"github.com/romycode/ggui/cmd/waygenerator/internal/goname"
	"github.com/romycode/ggui/cmd/waygenerator/internal/symbols"
	"github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel"
)

type Kind int

const (
	KindPrimitive Kind = iota
	KindFixed
	KindObject
	KindObjectDyn
	KindEnum
	KindNewIDStatic
)

type GoType struct {
	Kind       Kind
	TypeString string // expresión Go completa: "uint32", "*string", "*Surface", "SeatCapability"...
	ObjGoType  string // nombre desnudo (sin *) para Object/NewIDStatic/Enum; vacío en los demás casos
	AllowNull  bool
}

type ResolvedArg struct {
	XMLName string
	GoName  string
	IsFD    bool
	Type    GoType
}

type ResolvedRequest struct {
	XMLName    string
	GoName     string
	Since      int
	Destructor bool
	BindLike   bool
	Args       []ResolvedArg
	Returns    *GoType
}

type ResolvedEvent struct {
	XMLName  string
	GoName   string
	Since    int
	FDOwning bool
	Args     []ResolvedArg
}

type ResolvedEnumEntry struct {
	GoName string
	Value  string
}

type ResolvedEnum struct {
	GoName   string
	Bitfield bool
	Entries  []ResolvedEnumEntry
}

type ResolvedInterface struct {
	XMLName        string
	GoPackage      string
	GoType         string
	Recv           string
	MaxVersion     int
	HasEvents      bool
	PublicListener bool
	Requests       []ResolvedRequest
	Events         []ResolvedEvent
	Enums          []ResolvedEnum
}

type Model struct {
	Interfaces []ResolvedInterface
}

func Resolve(protos []xmlmodel.Protocol, table symbols.Table) (Model, error) {
	var m Model
	for _, p := range protos {
		for _, iface := range p.Interfaces {
			self := table[iface.Name]
			ri := ResolvedInterface{
				XMLName:        iface.Name,
				GoPackage:      self.GoPackage,
				GoType:         self.GoType,
				Recv:           strings_ToLowerFirst(self.GoType),
				MaxVersion:     iface.Version,
				HasEvents:      len(iface.Events) > 0,
				PublicListener: iface.Name != "wl_display",
			}
			for _, r := range iface.Requests {
				rr, err := resolveRequest(r, table, self)
				if err != nil {
					return Model{}, err
				}
				ri.Requests = append(ri.Requests, rr)
			}
			for _, ev := range iface.Events {
				re, err := resolveEvent(ev, table, self)
				if err != nil {
					return Model{}, err
				}
				ri.Events = append(ri.Events, re)
			}
			for _, en := range iface.Enums {
				ri.Enums = append(ri.Enums, resolveEnum(en, self.Enums))
			}
			m.Interfaces = append(m.Interfaces, ri)
		}
	}
	sort.Slice(m.Interfaces, func(i, j int) bool {
		return m.Interfaces[i].XMLName < m.Interfaces[j].XMLName
	})
	return m, nil
}

// resolveRequest resuelve un <request>. self es la symbols.Entry de la
// interfaz que declara este request -- hace falta para resolver un enum=
// sin punto (enum de la propia interfaz), que no puede buscarse en table
// por nombre porque no tiene el nombre XML de la interfaz delante.
func resolveRequest(r xmlmodel.Request, table symbols.Table, self symbols.Entry) (ResolvedRequest, error) {
	rr := ResolvedRequest{
		XMLName:    r.Name,
		GoName:     goname.Pascal(r.Name),
		Since:      r.Since,
		Destructor: r.Type == "destructor",
	}
	for _, a := range r.Args {
		if a.Type == "new_id" {
			if a.Interface == "" {
				rr.BindLike = true
				continue // el new_id dinámico no es un Arg: bindRaw lo maneja aparte
			}
			t := GoType{Kind: KindNewIDStatic, ObjGoType: table[a.Interface].GoType, TypeString: "*" + table[a.Interface].GoType}
			rr.Returns = &t
			continue
		}
		ra, err := resolveArg(a, table, self)
		if err != nil {
			return ResolvedRequest{}, err
		}
		rr.Args = append(rr.Args, ra)
	}
	return rr, nil
}

func resolveEvent(ev xmlmodel.Event, table symbols.Table, self symbols.Entry) (ResolvedEvent, error) {
	re := ResolvedEvent{XMLName: ev.Name, GoName: goname.Pascal(ev.Name), Since: ev.Since}
	for _, a := range ev.Args {
		ra, err := resolveArg(a, table, self)
		if err != nil {
			return ResolvedEvent{}, err
		}
		if ra.IsFD {
			re.FDOwning = true
		}
		re.Args = append(re.Args, ra)
	}
	return re, nil
}

func resolveArg(a xmlmodel.Arg, table symbols.Table, self symbols.Entry) (ResolvedArg, error) {
	ra := ResolvedArg{XMLName: a.Name, GoName: goname.Camel(a.Name)}
	switch a.Type {
	case "int":
		ra.Type = GoType{Kind: KindPrimitive, TypeString: "int32"}
	case "uint":
		if a.Enum != "" {
			owner, enumName := splitEnumRef(a.Enum)
			entry := self
			if owner != "" {
				entry = table[owner]
			}
			ra.Type = GoType{Kind: KindEnum, ObjGoType: entry.Enums[enumName].GoName, TypeString: entry.Enums[enumName].GoName}
		} else {
			ra.Type = GoType{Kind: KindPrimitive, TypeString: "uint32"}
		}
	case "fixed":
		ra.Type = GoType{Kind: KindFixed, TypeString: "Fixed"}
	case "string":
		if a.AllowNull {
			ra.Type = GoType{Kind: KindPrimitive, TypeString: "*string", AllowNull: true}
		} else {
			ra.Type = GoType{Kind: KindPrimitive, TypeString: "string"}
		}
	case "array":
		ra.Type = GoType{Kind: KindPrimitive, TypeString: "[]byte"}
	case "fd":
		ra.IsFD = true
		ra.Type = GoType{Kind: KindPrimitive, TypeString: "int"}
	case "object":
		if a.Interface == "" {
			ra.Type = GoType{Kind: KindObjectDyn, TypeString: "uint32"}
		} else {
			entry := table[a.Interface]
			ra.Type = GoType{Kind: KindObject, ObjGoType: entry.GoType, TypeString: "*" + entry.GoType, AllowNull: a.AllowNull}
		}
	case "new_id":
		// Solo llega aquí un new_id de <event>: resolveRequest intercepta
		// los new_id de <request> antes de llamar a resolveArg (se
		// convierten en Returns o en BindLike), pero resolveEvent no tiene
		// ese caso especial porque un evento no tiene Returns -- su new_id
		// es un Arg normal, tipado como objeto estático.
		entry := table[a.Interface]
		ra.Type = GoType{Kind: KindNewIDStatic, ObjGoType: entry.GoType, TypeString: "*" + entry.GoType}
	}
	return ra, nil
}

// splitEnumRef separa un enum= como "wl_shm.format" en (interfaz, nombre).
// Si no lleva punto, owner viene vacío y el llamante usa self (la propia
// interfaz) en vez de buscar en table -- table[""] sería la Entry cero,
// sin enums, y perdería la referencia.
func splitEnumRef(ref string) (owner, name string) {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '.' {
			return ref[:i], ref[i+1:]
		}
	}
	return "", ref
}

func resolveEnum(en xmlmodel.Enum, info map[string]symbols.EnumInfo) ResolvedEnum {
	re := ResolvedEnum{GoName: info[en.Name].GoName, Bitfield: en.Bitfield}
	for _, e := range en.Entries {
		re.Entries = append(re.Entries, ResolvedEnumEntry{
			GoName: re.GoName + goname.Pascal(e.Name),
			Value:  e.Value,
		})
	}
	return re
}

func strings_ToLowerFirst(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b[:1])
}
