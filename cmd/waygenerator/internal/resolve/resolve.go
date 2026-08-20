package resolve

import (
	"fmt"
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
	Summary string // summary= del <arg>; documenta el parámetro en el comentario del símbolo padre
}

type ResolvedRequest struct {
	XMLName    string
	GoName     string
	Since      int
	Destructor bool
	BindLike   bool
	Args       []ResolvedArg
	Returns    *GoType
	Summary    string // <description summary="...">
	Doc        string // cuerpo de <description>
}

type ResolvedEvent struct {
	XMLName    string
	GoName     string
	Since      int
	FDOwning   bool
	Destructor bool
	Args       []ResolvedArg
	Summary    string
	Doc        string
}

type ResolvedEnumEntry struct {
	GoName  string
	Value   string
	Summary string // summary= del <entry>
}

type ResolvedEnum struct {
	GoName   string
	Bitfield bool
	Entries  []ResolvedEnumEntry
	Summary  string
	Doc      string
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
	Summary        string
	Doc            string
}

type Model struct {
	Interfaces []ResolvedInterface
}

func Resolve(protos []xmlmodel.Protocol, table symbols.Table) (Model, error) {
	var m Model
	lineOf := make(map[string]int) // XMLName -> línea, para los mensajes de error
	fileOf := make(map[string]string)
	for _, p := range protos {
		for _, iface := range p.Interfaces {
			lineOf[iface.Name] = iface.Line
			fileOf[iface.Name] = p.File

			ri := ResolvedInterface{
				XMLName:        iface.Name,
				GoPackage:      table[iface.Name].GoPackage,
				GoType:         table[iface.Name].GoType,
				Recv:           strings_ToLowerFirst(table[iface.Name].GoType),
				MaxVersion:     iface.Version,
				HasEvents:      len(iface.Events) > 0,
				PublicListener: iface.Name != "wl_display",
				Summary:        iface.Description.Summary,
				Doc:            iface.Description.Body,
			}
			for _, r := range iface.Requests {
				rr, err := resolveRequest(r, table, table[iface.Name])
				if err != nil {
					return Model{}, fmt.Errorf("resolve: %s:%d: interfaz %q: %w", fileOf[iface.Name], lineOf[iface.Name], iface.Name, err)
				}
				ri.Requests = append(ri.Requests, rr)
			}
			for _, ev := range iface.Events {
				re, err := resolveEvent(ev, table, table[iface.Name])
				if err != nil {
					return Model{}, fmt.Errorf("resolve: %s:%d: interfaz %q: %w", fileOf[iface.Name], lineOf[iface.Name], iface.Name, err)
				}
				ri.Events = append(ri.Events, re)
			}
			enumInfo := table[iface.Name].Enums
			for _, en := range iface.Enums {
				ri.Enums = append(ri.Enums, resolveEnum(en, enumInfo))
			}
			m.Interfaces = append(m.Interfaces, ri)
		}
	}

	if err := checkInvariants(m, table, lineOf, fileOf); err != nil {
		return Model{}, err
	}

	sort.Slice(m.Interfaces, func(i, j int) bool {
		return m.Interfaces[i].XMLName < m.Interfaces[j].XMLName
	})
	return m, nil
}

// checkInvariants corre las 3 invariantes del spec. Aborta en la primera
// que falle, con un error que cita fichero, línea e interfaz.
func checkInvariants(m Model, table symbols.Table, lineOf map[string]int, fileOf map[string]string) error {
	if err := checkDAG(m, table, lineOf, fileOf); err != nil {
		return err
	}
	if err := checkNameCollisions(m, lineOf, fileOf); err != nil {
		return err
	}
	if err := checkNoFDInNewIDReachable(m, table, lineOf, fileOf); err != nil {
		return err
	}
	return nil
}

// checkDAG: ninguna interfaz de wlcore puede referenciar (via object o
// new_id con interface=) una interfaz de un paquete que no sea el propio o
// wlcore -- wlcore es la raíz, nunca depende de una extensión. Vacuous en
// esta fase (solo existe wlcore), corre igual para no tener que añadirla
// cuando lleguen xdgshell/wlrlayershell.
func checkDAG(m Model, table symbols.Table, lineOf map[string]int, fileOf map[string]string) error {
	for _, iface := range m.Interfaces {
		if iface.GoPackage != "wlcore" {
			continue
		}
		for _, r := range iface.Requests {
			if r.Returns != nil && r.Returns.ObjGoType != "" {
				if err := checkRefPackage(iface, r.Returns.ObjGoType, table, lineOf, fileOf); err != nil {
					return err
				}
			}
			for _, a := range r.Args {
				if a.Type.ObjGoType != "" {
					if err := checkRefPackage(iface, a.Type.ObjGoType, table, lineOf, fileOf); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func checkRefPackage(iface ResolvedInterface, refGoType string, table symbols.Table, lineOf map[string]int, fileOf map[string]string) error {
	for _, e := range table {
		if e.GoType == refGoType && e.GoPackage != "wlcore" {
			return fmt.Errorf("resolve: %s:%d: interfaz %q (wlcore) referencia %q, que vive en el paquete %q -- wlcore no puede depender de una extensión",
				fileOf[iface.XMLName], lineOf[iface.XMLName], iface.XMLName, refGoType, e.GoPackage)
		}
	}
	return nil
}

// checkNameCollisions: dentro de un mismo paquete, dos interfaces no
// pueden acabar en el mismo GoType.
func checkNameCollisions(m Model, lineOf map[string]int, fileOf map[string]string) error {
	seen := make(map[string]string) // "paquete/GoType" -> XMLName que lo usó primero
	for _, iface := range m.Interfaces {
		key := iface.GoPackage + "/" + iface.GoType
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("resolve: %s:%d: interfaz %q colisiona con %q -- ambas producen el tipo Go %q en el paquete %q",
				fileOf[iface.XMLName], lineOf[iface.XMLName], iface.XMLName, prev, iface.GoType, iface.GoPackage)
		}
		seen[key] = iface.XMLName
	}
	return nil
}

// checkNoFDInNewIDReachable: si una interfaz X aparece como new_id en
// algún evento (de cualquier interfaz), X no puede tener un evento con arg
// fd -- el objeto se registra y se olvida (Destroy() lo borra sin
// delete_id), y un fd en tránsito hacia un id ya borrado desincronizaría
// la cola de fds de toda la conexión.
func checkNoFDInNewIDReachable(m Model, table symbols.Table, lineOf map[string]int, fileOf map[string]string) error {
	reachable := make(map[string]bool) // GoType -> alcanzable como new_id en evento
	for _, iface := range m.Interfaces {
		for _, ev := range iface.Events {
			for _, a := range ev.Args {
				if a.Type.Kind == KindNewIDStatic {
					reachable[a.Type.ObjGoType] = true
				}
			}
		}
	}
	for _, iface := range m.Interfaces {
		if !reachable[iface.GoType] {
			continue
		}
		for _, ev := range iface.Events {
			if ev.FDOwning {
				return fmt.Errorf("resolve: %s:%d: interfaz %q es alcanzable como new_id en un evento y su propio evento %q lleva un arg fd -- el fd se perdería sin un objeto zombi que lo consuma",
					fileOf[iface.XMLName], lineOf[iface.XMLName], iface.XMLName, ev.XMLName)
			}
		}
	}
	return nil
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
		Summary:    r.Description.Summary,
		Doc:        r.Description.Body,
	}
	for _, a := range r.Args {
		if a.Type == "new_id" {
			if a.Interface == "" {
				rr.BindLike = true
				continue // el new_id dinámico no es un Arg: bindRaw lo maneja aparte
			}
			if table[a.Interface].GoType == "" {
				return ResolvedRequest{}, fmt.Errorf("request %q: %w", r.Name, fmt.Errorf("arg %q: %w", a.Name, fmt.Errorf("interfaz %q no encontrada", a.Interface)))
			}
			t := GoType{Kind: KindNewIDStatic, ObjGoType: table[a.Interface].GoType, TypeString: "*" + table[a.Interface].GoType}
			rr.Returns = &t
			continue
		}
		ra, err := resolveArg(a, table, self)
		if err != nil {
			return ResolvedRequest{}, fmt.Errorf("request %q: %w", r.Name, fmt.Errorf("arg %q: %w", a.Name, err))
		}
		rr.Args = append(rr.Args, ra)
	}
	return rr, nil
}

func resolveEvent(ev xmlmodel.Event, table symbols.Table, self symbols.Entry) (ResolvedEvent, error) {
	re := ResolvedEvent{
		XMLName:    ev.Name,
		GoName:     goname.Pascal(ev.Name),
		Since:      ev.Since,
		Destructor: ev.Type == "destructor",
		Summary:    ev.Description.Summary,
		Doc:        ev.Description.Body,
	}
	for _, a := range ev.Args {
		ra, err := resolveArg(a, table, self)
		if err != nil {
			return ResolvedEvent{}, fmt.Errorf("event %q: %w", ev.Name, fmt.Errorf("arg %q: %w", a.Name, err))
		}
		if ra.IsFD {
			re.FDOwning = true
		}
		re.Args = append(re.Args, ra)
	}
	return re, nil
}

func resolveArg(a xmlmodel.Arg, table symbols.Table, self symbols.Entry) (ResolvedArg, error) {
	ra := ResolvedArg{XMLName: a.Name, GoName: goname.Camel(a.Name), Summary: a.Summary}
	switch a.Type {
	case "int":
		if a.Enum != "" {
			t, err := resolveEnumType(a.Enum, table, self)
			if err != nil {
				return ResolvedArg{}, err
			}
			ra.Type = t
		} else {
			ra.Type = GoType{Kind: KindPrimitive, TypeString: "int32"}
		}
	case "uint":
		if a.Enum != "" {
			t, err := resolveEnumType(a.Enum, table, self)
			if err != nil {
				return ResolvedArg{}, err
			}
			ra.Type = t
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
			if entry.GoType == "" {
				return ResolvedArg{}, fmt.Errorf("interfaz %q no encontrada", a.Interface)
			}
			ra.Type = GoType{Kind: KindObject, ObjGoType: entry.GoType, TypeString: "*" + entry.GoType, AllowNull: a.AllowNull}
		}
	case "new_id":
		// Solo llega aquí un new_id de <event>: resolveRequest intercepta
		// los new_id de <request> antes de llamar a resolveArg (se
		// convierten en Returns o en BindLike), pero resolveEvent no tiene
		// ese caso especial porque un evento no tiene Returns -- su new_id
		// es un Arg normal, tipado como objeto estático.
		entry := table[a.Interface]
		if entry.GoType == "" {
			return ResolvedArg{}, fmt.Errorf("interfaz %q no encontrada", a.Interface)
		}
		ra.Type = GoType{Kind: KindNewIDStatic, ObjGoType: entry.GoType, TypeString: "*" + entry.GoType}
	default:
		return ResolvedArg{}, fmt.Errorf("tipo XML %q desconocido", a.Type)
	}
	return ra, nil
}

func resolveEnumType(ref string, table symbols.Table, self symbols.Entry) (GoType, error) {
	owner, enumName := splitEnumRef(ref)
	entry := self
	if owner != "" {
		entry = table[owner]
	}
	goName := entry.Enums[enumName].GoName
	if goName == "" {
		return GoType{}, fmt.Errorf("enum %q no encontrado", ref)
	}
	return GoType{Kind: KindEnum, ObjGoType: goName, TypeString: goName}, nil
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
	re := ResolvedEnum{
		GoName:   info[en.Name].GoName,
		Bitfield: en.Bitfield,
		Summary:  en.Description.Summary,
		Doc:      en.Description.Body,
	}
	for _, e := range en.Entries {
		re.Entries = append(re.Entries, ResolvedEnumEntry{
			GoName:  re.GoName + goname.Pascal(e.Name),
			Value:   e.Value,
			Summary: e.Summary,
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
