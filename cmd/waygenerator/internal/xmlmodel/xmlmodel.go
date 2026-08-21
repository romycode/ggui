package xmlmodel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Protocol struct {
	Name       string      `xml:"name,attr"`
	Interfaces []Interface `xml:"interface"`
	File       string      `xml:"-"`
}

type Interface struct {
	Name        string      `xml:"name,attr"`
	Version     int         `xml:"version,attr"`
	Description Description `xml:"description"`
	Requests    []Request   `xml:"request"`
	Events      []Event     `xml:"event"`
	Enums       []Enum      `xml:"enum"`
	Line        int         `xml:"-"`
}

type Request struct {
	Name        string      `xml:"name,attr"`
	Type        string      `xml:"type,attr"`
	Since       int         `xml:"since,attr"`
	Description Description `xml:"description"`
	Args        []Arg       `xml:"arg"`
}

type Event struct {
	Name        string      `xml:"name,attr"`
	Type        string      `xml:"type,attr"`
	Since       int         `xml:"since,attr"`
	Description Description `xml:"description"`
	Args        []Arg       `xml:"arg"`
}

// Description is the optional <description summary="..."> hanging off
// interface/request/event/enum: summary is one line, Body the long text
// (possibly several paragraphs, indented exactly as it came from the XML --
// it's up to the consumer to decide how to clean it up). Zero values when
// the XML doesn't carry a <description>, not a parse error.
type Description struct {
	Summary string `xml:"summary,attr"`
	Body    string `xml:",chardata"`
}

type Arg struct {
	Name      string `xml:"name,attr"`
	Type      string `xml:"type,attr"`
	Interface string `xml:"interface,attr"`
	Enum      string `xml:"enum,attr"`
	AllowNull bool   `xml:"allow-null,attr"`
	Summary   string `xml:"summary,attr"`
}

type Enum struct {
	Name        string      `xml:"name,attr"`
	Bitfield    bool        `xml:"bitfield,attr"`
	Description Description `xml:"description"`
	Entries     []Entry     `xml:"entry"`
}

type Entry struct {
	Name    string `xml:"name,attr"`
	Value   string `xml:"value,attr"`
	Summary string `xml:"summary,attr"`
}

// manifest file -> protocol name, in the order they're processed.
// xdg-shell.xml and wlr-layer-shell.xml get added when those phases start;
// listing them here now, without consuming them, buys nothing.
var manifest = []string{
	"wayland.xml",
	"xdg-shell.xml",
	"viewporter.xml",
	"fractional-scale-v1.xml",
	"tablet-v2.xml",
	"cursor-shape-v1.xml",
}

// ParseAll reads the internal manifest's files from dir and deserializes
// them. It doesn't interpret any of the content: the only possible errors
// here are "the file doesn't exist" or "the XML isn't XML".
func ParseAll(dir string) ([]Protocol, error) {
	protos := make([]Protocol, 0, len(manifest))
	for _, name := range manifest {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("xmlmodel: reading %s: %w", path, err)
		}
		var p Protocol
		if err := xml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("xmlmodel: %s: invalid XML: %w", path, err)
		}
		p.File = name
		for i := range p.Interfaces {
			p.Interfaces[i].Line = interfaceLine(data, p.Interfaces[i].Name)
			normalizeSince(&p.Interfaces[i])
			normalizeSummaries(&p.Interfaces[i])
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

// normalizeSummaries collapses the whitespace of every summary= attribute
// (and each <description>'s summary) to single spaces. encoding/xml doesn't
// do the attribute-value normalization the XML spec requires: a
// summary="..." that the XML author wrapped across several lines for
// readability arrives here with the literal newline and following
// indentation still inside the string -- harmless for <description>'s body
// (deliberately multiline, cleaned up by the renderer), but a summary gets
// dumped into a single-line Go comment (renderDocComment, enum entries): an
// unescaped "\n" there splits the comment into two physical lines and the
// second stops starting with "//", invalid Go.
func normalizeSummaries(iface *Interface) {
	collapse := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	iface.Description.Summary = collapse(iface.Description.Summary)
	for i := range iface.Requests {
		iface.Requests[i].Description.Summary = collapse(iface.Requests[i].Description.Summary)
		for j := range iface.Requests[i].Args {
			iface.Requests[i].Args[j].Summary = collapse(iface.Requests[i].Args[j].Summary)
		}
	}
	for i := range iface.Events {
		iface.Events[i].Description.Summary = collapse(iface.Events[i].Description.Summary)
		for j := range iface.Events[i].Args {
			iface.Events[i].Args[j].Summary = collapse(iface.Events[i].Args[j].Summary)
		}
	}
	for i := range iface.Enums {
		iface.Enums[i].Description.Summary = collapse(iface.Enums[i].Description.Summary)
		for j := range iface.Enums[i].Entries {
			iface.Enums[i].Entries[j].Summary = collapse(iface.Enums[i].Entries[j].Summary)
		}
	}
}

// interfaceLine looks for `<interface name="ifaceName"` in the raw source
// and counts newlines up to it. Returns 0 if not found (shouldn't happen:
// the name comes from having parsed that same interface).
func interfaceLine(data []byte, ifaceName string) int {
	needle := []byte(`<interface name="` + ifaceName + `"`)
	before, _, ok := bytes.Cut(data, needle)
	if !ok {
		return 0
	}
	return bytes.Count(before, []byte("\n")) + 1
}
