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
func Camel(snake string) string {
	return convert(snake, false)
}

func convert(snake string, upperFirst bool) string {
	parts := strings.Split(snake, "_")
	var b strings.Builder
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		if first && !upperFirst {
			b.WriteString(p)
		} else {
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
