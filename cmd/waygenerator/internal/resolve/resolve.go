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
	TypeString string // full Go expression: "uint32", "*string", "*Surface", "SeatCapability"...
	ObjGoType  string // bare name (no *) for Object/NewIDStatic/Enum; empty in all other cases
	// ObjGoPackage is ObjGoType's Go package, only for Object/NewIDStatic
	// (a reference to another interface). Empty for Enum -- an enum isn't
	// a dependency between interface packages, it's its own named type --
	// and in all other cases. Needed because two interfaces in different
	// packages can share a GoType once the protocol prefix is stripped
	// (wl_surface and xdg_surface are both "Surface"): without the
	// package, a check that only looks at ObjGoType can't tell which one
	// is really being referenced.
	ObjGoPackage string
	AllowNull    bool
}

type ResolvedArg struct {
	XMLName string
	GoName  string
	IsFD    bool
	Type    GoType
	Summary string // <arg>'s summary=; documents the parameter in the parent symbol's comment
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
	Doc        string // <description>'s body
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
	Summary string // <entry>'s summary=
}

type ResolvedEnum struct {
	XMLName  string
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
	lineOf := make(map[string]int) // XMLName -> line, for error messages
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
					return Model{}, fmt.Errorf("resolve: %s:%d: interface %q: %w", fileOf[iface.Name], lineOf[iface.Name], iface.Name, err)
				}
				ri.Requests = append(ri.Requests, rr)
			}
			for _, ev := range iface.Events {
				re, err := resolveEvent(ev, table, table[iface.Name])
				if err != nil {
					return Model{}, fmt.Errorf("resolve: %s:%d: interface %q: %w", fileOf[iface.Name], lineOf[iface.Name], iface.Name, err)
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

	if err := checkInvariants(m, lineOf, fileOf); err != nil {
		return Model{}, err
	}

	sort.Slice(m.Interfaces, func(i, j int) bool {
		return m.Interfaces[i].XMLName < m.Interfaces[j].XMLName
	})
	return m, nil
}

// checkInvariants runs the spec's 3 invariants. Aborts on the first one
// that fails, with an error citing file, line and interface.
func checkInvariants(m Model, lineOf map[string]int, fileOf map[string]string) error {
	if err := checkDAG(m, lineOf, fileOf); err != nil {
		return err
	}
	if err := checkNameCollisions(m, lineOf, fileOf); err != nil {
		return err
	}
	if err := checkNoFDInNewIDReachable(m, lineOf, fileOf); err != nil {
		return err
	}
	return nil
}

// checkDAG: no wlcore interface may reference (via object or new_id with
// interface=) an interface from a package other than its own or wlcore --
// wlcore is the root, it never depends on an extension. Vacuous at this
// phase (only wlcore exists), it still runs so it doesn't need to be added
// later when xdgshell/wlrlayershell arrive.
func checkDAG(m Model, lineOf map[string]int, fileOf map[string]string) error {
	for _, iface := range m.Interfaces {
		if iface.GoPackage != "wlcore" {
			continue
		}
		for _, r := range iface.Requests {
			if r.Returns != nil {
				if err := checkRefPackage(iface, *r.Returns, lineOf, fileOf); err != nil {
					return err
				}
			}
			for _, a := range r.Args {
				if err := checkRefPackage(iface, a.Type, lineOf, fileOf); err != nil {
					return err
				}
			}
		}
		for _, ev := range iface.Events {
			for _, a := range ev.Args {
				if err := checkRefPackage(iface, a.Type, lineOf, fileOf); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkRefPackage compares the reference's package (ObjGoPackage, already
// resolved against the symbol table in resolveArg/resolveRequest) against
// "wlcore" directly -- it never looks up the bare ObjGoType across the
// whole table, because two interfaces in different packages can share a
// GoType once the prefix is stripped (wl_surface and xdg_surface are both
// "Surface") and that lookup would confuse which one is really being
// referenced. An empty ObjGoPackage (KindPrimitive, KindFixed,
// KindObjectDyn, KindEnum) isn't a reference to another interface --
// there's nothing to check.
func checkRefPackage(iface ResolvedInterface, ref GoType, lineOf map[string]int, fileOf map[string]string) error {
	if ref.ObjGoPackage == "" || ref.ObjGoPackage == "wlcore" {
		return nil
	}
	return fmt.Errorf("resolve: %s:%d: interface %q (wlcore) references %q, which lives in package %q -- wlcore cannot depend on an extension",
		fileOf[iface.XMLName], lineOf[iface.XMLName], iface.XMLName, ref.ObjGoType, ref.ObjGoPackage)
}

// checkNameCollisions: within the same package, two interfaces can't end
// up with the same GoType.
func checkNameCollisions(m Model, lineOf map[string]int, fileOf map[string]string) error {
	seen := make(map[string]string) // "package/GoType" -> XMLName that used it first
	for _, iface := range m.Interfaces {
		key := iface.GoPackage + "/" + iface.GoType
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("resolve: %s:%d: interface %q collides with %q -- both produce the Go type %q in package %q",
				fileOf[iface.XMLName], lineOf[iface.XMLName], iface.XMLName, prev, iface.GoType, iface.GoPackage)
		}
		seen[key] = iface.XMLName
	}
	return nil
}

// checkNoFDInNewIDReachable: if an interface X appears as a new_id in some
// event (of any interface), X can't have an event with an fd arg -- the
// object gets registered and forgotten (Destroy() removes it without a
// delete_id), and an fd in transit toward an already-removed id would
// desync the whole connection's fd queue.
func checkNoFDInNewIDReachable(m Model, lineOf map[string]int, fileOf map[string]string) error {
	reachable := make(map[string]bool) // "package/GoType" -> reachable as new_id in an event
	for _, iface := range m.Interfaces {
		for _, ev := range iface.Events {
			for _, a := range ev.Args {
				if a.Type.Kind == KindNewIDStatic {
					reachable[a.Type.ObjGoPackage+"/"+a.Type.ObjGoType] = true
				}
			}
		}
	}
	for _, iface := range m.Interfaces {
		if !reachable[iface.GoPackage+"/"+iface.GoType] {
			continue
		}
		for _, ev := range iface.Events {
			if ev.FDOwning {
				return fmt.Errorf("resolve: %s:%d: interface %q is reachable as new_id in an event and its own event %q carries an fd arg -- the fd would be lost with no zombie object to consume it",
					fileOf[iface.XMLName], lineOf[iface.XMLName], iface.XMLName, ev.XMLName)
			}
		}
	}
	return nil
}

// resolveRequest resolves a <request>. self is the symbols.Entry of the
// interface declaring this request -- needed to resolve a dotless enum=
// (an enum of the interface itself), which can't be looked up in table by
// name because it doesn't carry the interface's XML name in front.
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
				continue // a dynamic new_id isn't an Arg: bindRaw handles it separately
			}
			if table[a.Interface].GoType == "" {
				return ResolvedRequest{}, fmt.Errorf("request %q: %w", r.Name, fmt.Errorf("arg %q: %w", a.Name, fmt.Errorf("interface %q not found", a.Interface)))
			}
			t := GoType{Kind: KindNewIDStatic, ObjGoType: table[a.Interface].GoType, ObjGoPackage: table[a.Interface].GoPackage, TypeString: "*" + table[a.Interface].GoType}
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
				return ResolvedArg{}, fmt.Errorf("interface %q not found", a.Interface)
			}
			ra.Type = GoType{Kind: KindObject, ObjGoType: entry.GoType, ObjGoPackage: entry.GoPackage, TypeString: "*" + entry.GoType, AllowNull: a.AllowNull}
		}
	case "new_id":
		// Only an <event>'s new_id reaches here: resolveRequest intercepts
		// a <request>'s new_id before calling resolveArg (they become
		// Returns or BindLike), but resolveEvent has no such special case
		// because an event has no Returns -- its new_id is a normal Arg,
		// typed as a static object.
		entry := table[a.Interface]
		if entry.GoType == "" {
			return ResolvedArg{}, fmt.Errorf("interface %q not found", a.Interface)
		}
		ra.Type = GoType{Kind: KindNewIDStatic, ObjGoType: entry.GoType, ObjGoPackage: entry.GoPackage, TypeString: "*" + entry.GoType}
	default:
		return ResolvedArg{}, fmt.Errorf("unknown XML type %q", a.Type)
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
		return GoType{}, fmt.Errorf("enum %q not found", ref)
	}
	return GoType{Kind: KindEnum, ObjGoType: goName, TypeString: goName}, nil
}

// splitEnumRef splits an enum= like "wl_shm.format" into (interface, name).
// If it carries no dot, owner comes back empty and the caller uses self
// (the interface itself) instead of looking it up in table -- table[""]
// would be the zero Entry, with no enums, and would lose the reference.
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
		XMLName:  en.Name,
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
