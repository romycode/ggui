package goname

import "strings"

// Pascal converts snake_case (the naming style of the Wayland XML) to
// PascalCase: wl_shm_pool -> ShmPool (without the wl_ prefix, which the
// caller strips beforehand with StripPrefix).
func Pascal(snake string) string {
	return convert(snake, true)
}

// Camel is Pascal with the first letter lowercase: used for
// parameter/field names, never for type names.
//
// A result that matches a Go reserved word (e.g. the XML arg "interface",
// which isn't reserved in C but is in Go) can't be used as an identifier.
// keywordAliases renames the ones Wayland actually uses; anything else falls
// back to a trailing "_", the same convention the Wayland XML itself already
// uses for reserved words in other languages (e.g. "class_").
func Camel(snake string) string {
	c := convert(snake, false)
	if alias, ok := keywordAliases[c]; ok {
		return alias
	}
	if goKeywords[c] {
		return c + "_"
	}
	return c
}

// keywordAliases are the idiomatic Go spellings for reserved words that
// appear as <arg> names in the protocol XML. Today "interface" is the only
// one (wl_registry.bind and wl_registry.global): "iface" is what Go code
// conventionally calls it, and it is already what codegen names bindRaw's
// own parameter, so the generated Registry reads consistently. The generic
// "interface_" fallback would work but reads like an escape hatch in the
// one place a user of the binding actually sees it.
var keywordAliases = map[string]string{
	"interface": "iface",
}

// goKeywords are Go's reserved words (https://go.dev/ref/spec#Keywords).
var goKeywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
}

func convert(snake string, upperFirst bool) string {
	parts := strings.Split(snake, "_")
	var b strings.Builder
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		switch {
		case first && !upperFirst:
			// First component of a Camel: kept as-is, lowercase
			// (including "id" -> "id", never "ID").
			b.WriteString(p)
		case strings.ToLower(p) == "id":
			// "id" is an initialism in Go (https://go.dev/wiki/CodeReviewComments#initialisms):
			// it's always uppercase when it isn't the first component,
			// both in Pascal ("delete_id" -> "DeleteID") and in Camel
			// after the first component ("object_id" -> "objectID").
			// Without this special case the generated code would use
			// "DeleteId"/"objectId", which doesn't compile against the
			// contract documented in docs/wlcore.md (e.g.
			// DisplayListener.DeleteID).
			b.WriteString("ID")
		default:
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
		first = false
	}
	return b.String()
}

// StripPrefix removes the protocol prefix (wl_, xdg_...) from an
// interface's XML name, if it has one. Does nothing if the name doesn't
// start with that prefix.
func StripPrefix(xmlName, prefix string) string {
	return strings.TrimPrefix(xmlName, prefix)
}

// StripSuffix removes the trailing "_vN" of a versioned extension
// interface (zwp_tablet_v2 -> zwp_tablet, wp_fractional_scale_v1 ->
// wp_fractional_scale), if it has one. The version is part of the Go
// package name, not the type (two versions of the same extension are two
// packages) -- see waygenerator.md. Does nothing if the name doesn't end
// with that suffix.
func StripSuffix(xmlName, suffix string) string {
	return strings.TrimSuffix(xmlName, suffix)
}
