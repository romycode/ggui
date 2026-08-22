// Package keysymdata parses the libxkbcommon keysym definitions used by keysymgen.
package keysymdata

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	defineRE            = regexp.MustCompile(`^#define\s+XKB_KEY_([A-Za-z0-9_]+)\s+0x([0-9A-Fa-f]+)(?:\s+/\*(.*?)\*/)?\s*$`)
	unicodeAnnotationRE = regexp.MustCompile(`(?:^|[[:space:]])([<(]?U\+.*)$`)
	unicodeCodePointRE  = regexp.MustCompile(`^U\+([0-9A-F]{4,6})(?:\s|$)`)

	// libxkbcommon 1.13.2's xkb_keysym_to_utf32 uses the modern mathematical
	// angle brackets for these legacy keysyms, while xkbcommon-keysyms.h still
	// carries obsolete U+2329/U+232A annotations. Keep the runtime-compatible
	// exceptions explicit and cover the complete generated table with an oracle.
	libxkbcommonRuneOverrides = map[uint32]rune{
		0x0abc: '\u27e8',
		0x0abe: '\u27e9',
	}
)

// Tables contains the names, canonical names, and legacy Unicode mappings
// parsed from a libxkbcommon keysym header.
type Tables struct {
	Names          map[string]uint32
	CanonicalNames map[uint32]string
	Runes          map[uint32]rune
}

type definition struct {
	name                  string
	value                 uint32
	r                     rune
	hasRune               bool
	deprecated            bool
	explicitNonDeprecated bool
}

// Parse reads libxkbcommon XKB_KEY_ definitions from r.
func Parse(r io.Reader) (Tables, error) {
	tables := Tables{
		Names:          make(map[string]uint32),
		CanonicalNames: make(map[uint32]string),
		Runes:          make(map[uint32]rune),
	}
	unicodeMappings := make(map[uint32]rune)
	canonicalNonDeprecated := make(map[uint32]bool)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lineNumber := 0
	definitions := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if !strings.HasPrefix(line, "#define XKB_KEY_") {
			continue
		}

		definition, err := parseDefinition(line, lineNumber)
		if err != nil {
			return Tables{}, err
		}
		if r, ok := libxkbcommonRuneOverrides[definition.value]; ok {
			definition.r = r
			definition.hasRune = true
		}
		definitions++

		if err := addDefinition(&tables, unicodeMappings, canonicalNonDeprecated, definition); err != nil {
			return Tables{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Tables{}, fmt.Errorf("keysymgen: scan header: %w", err)
	}
	if definitions == 0 {
		return Tables{}, fmt.Errorf("keysymgen: no keysym definitions")
	}

	return tables, nil
}

func parseDefinition(line string, lineNumber int) (definition, error) {
	matches := defineRE.FindStringSubmatch(line)
	if matches == nil {
		return definition{}, fmt.Errorf("keysymgen: invalid keysym definition on line %d", lineNumber)
	}

	value, err := strconv.ParseUint(matches[2], 16, 32)
	if err != nil {
		return definition{}, fmt.Errorf("keysymgen: invalid keysym definition on line %d: %w", lineNumber, err)
	}

	comment := strings.TrimSpace(matches[3])
	parsed := definition{
		name:                  matches[1],
		value:                 uint32(value),
		deprecated:            strings.HasPrefix(comment, "deprecated"),
		explicitNonDeprecated: strings.HasPrefix(comment, "non-deprecated alias"),
	}

	annotation, ok := findUnicodeAnnotation(comment)
	if !ok {
		return parsed, nil
	}
	if strings.HasPrefix(annotation, "(U+") {
		parsed.deprecated = true
	}

	codePointText, ok := unicodeCodePoint(annotation)
	if !ok {
		return definition{}, fmt.Errorf("keysymgen: invalid Unicode annotation on line %d", lineNumber)
	}

	codePoint, err := strconv.ParseUint(codePointText, 16, 32)
	if err != nil || !utf8.ValidRune(rune(codePoint)) {
		return definition{}, fmt.Errorf("keysymgen: invalid Unicode annotation on line %d", lineNumber)
	}
	parsed.r = rune(codePoint)
	parsed.hasRune = true

	return parsed, nil
}

func addDefinition(tables *Tables, unicodeMappings map[uint32]rune, canonicalNonDeprecated map[uint32]bool, parsed definition) error {
	if value, ok := tables.Names[parsed.name]; ok && value != parsed.value {
		return fmt.Errorf("keysymgen: name %s has conflicting values", parsed.name)
	}
	tables.Names[parsed.name] = parsed.value

	if parsed.hasRune {
		if r, ok := unicodeMappings[parsed.value]; ok && r != parsed.r {
			return fmt.Errorf("keysymgen: keysym %#x has conflicting Unicode mappings", parsed.value)
		}
		unicodeMappings[parsed.value] = parsed.r
		if !algorithmicRune(parsed.value) {
			tables.Runes[parsed.value] = parsed.r
		}
	}

	if _, ok := tables.CanonicalNames[parsed.value]; !ok {
		tables.CanonicalNames[parsed.value] = parsed.name
	}
	if canonicalNonDeprecated[parsed.value] && !parsed.explicitNonDeprecated {
		parsed.deprecated = true
	}
	if !parsed.deprecated && !canonicalNonDeprecated[parsed.value] {
		tables.CanonicalNames[parsed.value] = parsed.name
		canonicalNonDeprecated[parsed.value] = true
	}

	return nil
}

func findUnicodeAnnotation(comment string) (string, bool) {
	matches := unicodeAnnotationRE.FindStringSubmatch(comment)
	if matches == nil {
		return "", false
	}
	return matches[1], true
}

func unicodeCodePoint(annotation string) (string, bool) {
	switch {
	case strings.HasPrefix(annotation, "<U+"):
		if !strings.HasSuffix(annotation, ">") {
			return "", false
		}
		annotation = annotation[1 : len(annotation)-1]
	case strings.HasPrefix(annotation, "(U+"):
		if !strings.HasSuffix(annotation, ")") {
			return "", false
		}
		annotation = annotation[1 : len(annotation)-1]
	case strings.HasPrefix(annotation, "U+"):
		if strings.HasSuffix(annotation, ">") || strings.HasSuffix(annotation, ")") {
			return "", false
		}
	default:
		return "", false
	}

	matches := unicodeCodePointRE.FindStringSubmatch(annotation)
	if matches == nil {
		return "", false
	}
	return matches[1], true
}

func algorithmicRune(value uint32) bool {
	return value&0xff000000 == 0x01000000 ||
		(value >= 0x20 && value <= 0x7e) ||
		(value >= 0xa0 && value <= 0xff)
}
