package text

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
)

// Reading four strings out of a font is all discovery needs, and doing it
// by hand is what keeps a scan of hundreds of fonts fast: a full parse
// (sfnt.ParseReaderAt) reads and decodes tables — glyph maps, metrics —
// that matching never looks at, and costs about 0.3 ms per file. Reading
// the table directory, then the one name table, is a few short reads.

var errBadFont = errors.New("text: not a usable font file")

// names are the name-table strings matching compares. Any may be empty.
type names struct {
	family, subfamily                       string // name IDs 1 and 2
	typographicFamily, typographicSubfamily string // name IDs 16 and 17
}

const (
	// maxTables bounds the table directory we are willing to read. Real
	// fonts have a couple dozen tables.
	maxTables = 512
	// maxNameTable bounds the name table. They run to tens of kilobytes
	// when a font names itself in many languages.
	maxNameTable = 1 << 20
	// maxCollection bounds the fonts in one collection file.
	maxCollection = 1024
)

// fontOffsets returns where each font in r starts: one offset for a plain
// font, one per member for a collection, which is also reported.
func fontOffsets(r io.ReaderAt) (bases []int64, collection bool, err error) {
	var magic [4]byte
	if err := readFull(r, magic[:], 0); err != nil {
		return nil, false, err
	}
	if string(magic[:]) != "ttcf" {
		return []int64{0}, false, nil
	}

	// TTC header: tag, version, numFonts, then that many offsets.
	var head [12]byte
	if err := readFull(r, head[:], 0); err != nil {
		return nil, false, err
	}
	n := int(binary.BigEndian.Uint32(head[8:]))
	if n == 0 || n > maxCollection {
		return nil, false, errBadFont
	}
	offsets := make([]byte, 4*n)
	if err := readFull(r, offsets, 12); err != nil {
		return nil, false, err
	}
	bases = make([]int64, n)
	for i := range bases {
		bases[i] = int64(binary.BigEndian.Uint32(offsets[4*i:]))
	}
	return bases, true, nil
}

// readNames reads the name-table strings of the font whose table directory
// starts at base. Table offsets are from the start of the file even inside
// a collection, so base only locates the directory.
func readNames(r io.ReaderAt, base int64) (names, error) {
	// Offset table: sfnt version, numTables, and three search hints.
	var head [12]byte
	if err := readFull(r, head[:], base); err != nil {
		return names{}, err
	}
	numTables := int(binary.BigEndian.Uint16(head[4:]))
	if numTables == 0 || numTables > maxTables {
		return names{}, errBadFont
	}

	dir := make([]byte, 16*numTables)
	if err := readFull(r, dir, base+12); err != nil {
		return names{}, err
	}

	var offset, length uint32
	for i := range numTables {
		rec := dir[16*i:]
		if string(rec[:4]) == "name" {
			offset = binary.BigEndian.Uint32(rec[8:])
			length = binary.BigEndian.Uint32(rec[12:])
			break
		}
	}
	if length < 6 || length > maxNameTable {
		return names{}, errBadFont
	}

	table := make([]byte, length)
	if err := readFull(r, table, int64(offset)); err != nil {
		return names{}, err
	}
	return parseNames(table), nil
}

// Slots in a parsed name table for the IDs we keep.
const (
	slotFamily = iota
	slotSubfamily
	slotTypographicFamily
	slotTypographicSubfamily
	numSlots
)

// slotOf maps a name ID to its slot, or -1 for one we do not keep.
func slotOf(id uint16) int {
	switch id {
	case 1:
		return slotFamily
	case 2:
		return slotSubfamily
	case 16:
		return slotTypographicFamily
	case 17:
		return slotTypographicSubfamily
	}
	return -1
}

// parseNames decodes the records of a name table that it needs. A record
// that points outside the table is skipped rather than failing the font:
// fonts in the wild are sloppy, and one bad record should not hide the good
// ones.
func parseNames(table []byte) names {
	count := int(binary.BigEndian.Uint16(table[2:]))
	stringsAt := int(binary.BigEndian.Uint16(table[4:]))

	var (
		found  [numSlots]string
		scores [numSlots]int
	)
	for i := range count {
		rec := 6 + 12*i
		if rec+12 > len(table) {
			break
		}
		platform := binary.BigEndian.Uint16(table[rec:])
		language := binary.BigEndian.Uint16(table[rec+4:])
		slot := slotOf(binary.BigEndian.Uint16(table[rec+6:]))
		size := int(binary.BigEndian.Uint16(table[rec+8:]))
		at := stringsAt + int(binary.BigEndian.Uint16(table[rec+10:]))
		if slot < 0 || at+size > len(table) {
			continue
		}

		// Prefer US English on Windows, then any Windows or Unicode name,
		// then Mac English. Other languages are never what a caller types.
		var score int
		switch {
		case platform == 3 && language == 0x409:
			score = 3
		case platform == 3 || platform == 0:
			score = 2
		case platform == 1 && language == 0:
			score = 1
		default:
			continue
		}
		if score <= scores[slot] {
			continue
		}

		raw := table[at : at+size]
		if platform == 1 {
			found[slot] = decodeMacRoman(raw)
		} else {
			found[slot] = decodeUTF16BE(raw)
		}
		scores[slot] = score
	}

	return names{
		family:               found[slotFamily],
		subfamily:            found[slotSubfamily],
		typographicFamily:    found[slotTypographicFamily],
		typographicSubfamily: found[slotTypographicSubfamily],
	}
}

// decodeUTF16BE decodes the encoding Windows and Unicode platform names use.
func decodeUTF16BE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// decodeMacRoman decodes a Mac platform name. Family and style names are
// ASCII in practice, so the bytes are taken as they are; a name outside
// ASCII will not match anything a caller types, which is the safe failure.
func decodeMacRoman(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// readFull fills buf from r at offset, treating a short read as an error
// whatever the reader chooses to report at end of file.
func readFull(r io.ReaderAt, buf []byte, offset int64) error {
	n, err := r.ReadAt(buf, offset)
	if n == len(buf) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return err
}
