package goname

import "strings"

// Pascal convierte snake_case (el estilo de nombres del XML de Wayland) a
// PascalCase: wl_shm_pool -> ShmPool (sin el prefijo wl_, que quita el
// llamante con StripPrefix antes).
func Pascal(snake string) string {
	return convert(snake, true)
}

// Camel es Pascal con la primera letra en minúscula: usado para nombres de
// parámetro/campo, nunca para nombres de tipo.
//
// Si el resultado coincide con una palabra reservada de Go (p.ej. el arg XML
// "interface", que no es reservado en C pero sí en Go), se le añade un "_"
// final para evitar un identificador inválido -- mismo convenio que ya usa
// el propio XML de Wayland para palabras reservadas en otros lenguajes
// (p.ej. "class_").
func Camel(snake string) string {
	c := convert(snake, false)
	if goKeywords[c] {
		return c + "_"
	}
	return c
}

// goKeywords son las palabras reservadas de Go (https://go.dev/ref/spec#Keywords).
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
			// Primer componente de un Camel: va tal cual, en minúscula
			// (incluido "id" -> "id", nunca "ID").
			b.WriteString(p)
		case strings.ToLower(p) == "id":
			// "id" es un initialism en Go (https://go.dev/wiki/CodeReviewComments#initialisms):
			// va siempre en mayúsculas cuando no es el primer componente,
			// tanto en Pascal ("delete_id" -> "DeleteID") como en Camel tras
			// el primer componente ("object_id" -> "objectID"). Sin este
			// caso especial el código generado usaría "DeleteId"/"objectId",
			// que no compila contra el contrato documentado en
			// docs/wlcore.md (p.ej. DisplayListener.DeleteID).
			b.WriteString("ID")
		default:
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
		first = false
	}
	return b.String()
}

// StripPrefix quita el prefijo de protocolo (wl_, xdg_...) del nombre XML de
// una interfaz, si lo tiene. No hace nada si el nombre no empieza por ese
// prefijo.
func StripPrefix(xmlName, prefix string) string {
	return strings.TrimPrefix(xmlName, prefix)
}
