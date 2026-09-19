package text

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font/opentype"
)

// Style selects one of the four classic faces of a family.
type Style uint8

const (
	// Regular is the upright, normal-weight face.
	Regular Style = iota
	// Bold is the upright, bold face.
	Bold
	// Italic is the normal-weight face slanted, including obliques.
	Italic
	// BoldItalic is the bold face slanted.
	BoldItalic
)

// String returns the style's name as fonts spell it.
func (s Style) String() string {
	switch s {
	case Regular:
		return "Regular"
	case Bold:
		return "Bold"
	case Italic:
		return "Italic"
	case BoldItalic:
		return "Bold Italic"
	}
	return fmt.Sprintf("Style(%d)", uint8(s))
}

// ErrNotFound is wrapped by every lookup that matched no installed font.
var ErrNotFound = errors.New("text: no matching system font")

// defaultFamilies is the sans-serif preference list used when the caller
// names no family, most preferred first. It is a list of families commonly
// installed on Linux desktops; the first one present wins.
var defaultFamilies = []string{
	"Inter",
	"Noto Sans",
	"DejaVu Sans",
	"Liberation Sans",
	"Cantarell",
	"Adwaita Sans",
	"Ubuntu",
	"Roboto",
	"Arimo",
}

// maxDepth bounds directory recursion. Font trees are shallow, and symlinked
// directories are followed, so a cycle has to terminate somewhere.
const maxDepth = 12

// Find returns the first installed font whose family is one of families, in
// preference order, and whose style is style. A family earlier in the list
// beats a later one wherever on disk they are; among fonts matching the same
// family, the one in the earlier directory wins, so a user's own fonts
// override the system's.
//
// Family matching ignores case and repeated spaces. It fails with an error
// wrapping [ErrNotFound] when nothing matches.
func Find(style Style, families ...string) (*opentype.Font, error) {
	return FindContext(context.Background(), style, families...)
}

// FindContext is Find with cancellation. Cancellation is checked while
// walking directories and before loading the selected font.
func FindContext(ctx context.Context, style Style, families ...string) (*opentype.Font, error) {
	return findInContext(ctx, fontDirs(), style, families)
}

// findIn is Find over an explicit list of directories.
func findIn(dirs []string, style Style, families []string) (*opentype.Font, error) {
	return findInContext(context.Background(), dirs, style, families)
}

func findInContext(ctx context.Context, dirs []string, style Style, families []string) (*opentype.Font, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := searchContext(ctx, dirs, style, families)
	if err != nil {
		return nil, err
	}
	f, err := c.load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

// candidate is a font file that matched, and which font in it.
type candidate struct {
	path string
	// index is the font's position in a collection file, or -1 for a plain
	// font file.
	index int
	// pref is the position of the matched family in the caller's list;
	// lower is better.
	pref int
}

// search walks dirs and returns the best match without loading it.
func search(dirs []string, style Style, families []string) (candidate, error) {
	return searchContext(context.Background(), dirs, style, families)
}

func searchContext(ctx context.Context, dirs []string, style Style, families []string) (candidate, error) {
	if len(families) == 0 {
		return candidate{}, fmt.Errorf("%w: no family given", ErrNotFound)
	}

	want := make([]string, len(families))
	for i, f := range families {
		want[i] = normalize(f)
	}

	best := candidate{pref: len(want)}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return candidate{}, err
		}
		stopped := walkFontsContext(ctx, dir, 0, func(path string) bool {
			if c, ok := matchFile(path, want, style, best.pref); ok {
				best = c
			}
			// Nothing beats the first choice, so stop scanning.
			return best.pref == 0
		})
		if err := ctx.Err(); err != nil {
			return candidate{}, err
		}
		if stopped {
			break
		}
	}

	if best.pref == len(want) {
		return candidate{}, fmt.Errorf("%w: %s %s", ErrNotFound, strings.Join(families, ", "), style)
	}
	return best, nil
}

// load reads the matched font fully. Discovery only touched the name
// tables; the font that gets used needs its glyph data, and retains the
// bytes for as long as it lives.
func (c candidate) load() (*opentype.Font, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	if c.index < 0 {
		return opentype.Parse(data)
	}
	col, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	return col.Font(c.index)
}

// walkFonts calls visit for every font file under dir, in directory order,
// following symlinks. It stops and returns true as soon as visit does.
// Unreadable directories are skipped: a font directory the user cannot read
// is not an error, it is a directory with no fonts they can use.
func walkFonts(dir string, depth int, visit func(path string) bool) bool {
	return walkFontsContext(context.Background(), dir, depth, visit)
}

func walkFontsContext(ctx context.Context, dir string, depth int, visit func(path string) bool) bool {
	if ctx.Err() != nil {
		return false
	}
	if depth > maxDepth {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			return false
		}
		path := filepath.Join(dir, e.Name())

		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			st, err := os.Stat(path)
			if err != nil {
				continue // dangling link
			}
			isDir = st.IsDir()
		}

		switch {
		case isDir:
			if walkFontsContext(ctx, path, depth+1, visit) {
				return true
			}
		case isFontFile(e.Name()):
			if visit(path) {
				return true
			}
		}
	}
	return false
}

// isFontFile reports whether name has a font-file extension.
func isFontFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc", ".otc":
		return true
	}
	return false
}

// matchFile reports the best match in one file that beats bound. A file
// that will not open or parse is not a match, and not an error either:
// system font directories routinely hold files that are not fonts we can
// read.
//
// Whether a file is a collection is decided by its magic number, not its
// extension: some distributions ship collections as .ttf.
func matchFile(path string, want []string, style Style, bound int) (candidate, bool) {
	f, err := os.Open(path)
	if err != nil {
		return candidate{}, false
	}
	defer f.Close()

	bases, collection, err := fontOffsets(f)
	if err != nil {
		return candidate{}, false
	}

	best, found := candidate{}, false
	for i, base := range bases {
		n, err := readNames(f, base)
		if err != nil {
			continue
		}
		if pref := matchNames(n, want, style); pref < bound {
			index := -1
			if collection {
				index = i
			}
			best, found, bound = candidate{path: path, index: index, pref: pref}, true, pref
		}
	}
	return best, found
}

// matchNames returns the index into want of the best family a font's names
// match at style, or len(want) if none does.
//
// Fonts with more than four styles per family carry the real family in the
// typographic names and a legacy pair that packs the weight into the family
// ("Inter Light" / "Regular"); either pair may be what the caller means.
func matchNames(n names, want []string, style Style) int {
	best := len(want)
	for _, pair := range [2][2]string{
		{n.typographicFamily, n.typographicSubfamily},
		{n.family, n.subfamily},
	} {
		if pair[0] == "" || !styleMatches(style, pair[1]) {
			continue
		}
		family := normalize(pair[0])
		for i, w := range want[:best] {
			if w == family {
				best = i
				break
			}
		}
	}
	return best
}

// styleMatches reports whether a font's subfamily name denotes style.
func styleMatches(style Style, subfamily string) bool {
	name := strings.ToLower(strings.ReplaceAll(subfamily, " ", ""))
	switch style {
	case Regular:
		return name == "regular" || name == "book" || name == "roman" || name == "normal"
	case Bold:
		return name == "bold"
	case Italic:
		return name == "italic" || name == "oblique"
	case BoldItalic:
		return name == "bolditalic" || name == "boldoblique"
	}
	return false
}

// normalize folds a family name for comparison.
func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// fontDirs lists the directories to scan, highest priority first: the
// user's own fonts, then the system's, in the order the XDG base directory
// specification gives.
func fontDirs() []string {
	var dirs []string

	home, _ := os.UserHomeDir()

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if dataHome != "" {
		dirs = append(dirs, filepath.Join(dataHome, "fonts"))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".fonts"))
	}

	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, d := range filepath.SplitList(dataDirs) {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "fonts"))
		}
	}
	return dirs
}
