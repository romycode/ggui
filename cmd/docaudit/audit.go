package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Symbol is one exported declaration and where it was found. Only the
// undocumented ones are kept: the documented ones are counted, not listed.
type Symbol struct {
	File string // path relative to the scanned root
	Line int    // 1-indexed line of the declaration
	Kind string // "type", "func", "method", "field" or "value"
	Name string // "Conn", "Conn.Register", "ProtocolError.Code"
}

// Package is the doc-comment coverage of the exported surface of one
// directory. Documented + len(Missing) is the whole exported surface, so a
// package with nothing exported reports zero of both.
type Package struct {
	Dir        string   // path relative to the scanned root
	Documented int      // exported declarations that carry a doc comment
	Missing    []Symbol // the ones that do not, in file and line order
	Generated  bool     // holds at least one *.gen.go
}

// Percent is the share of the exported surface that carries a doc comment,
// rounded down. A package with nothing exported is complete by definition
// and reports 100.
func (p Package) Percent() int {
	total := p.Documented + len(p.Missing)
	if total == 0 {
		return 100
	}
	return 100 * p.Documented / total
}

// Scan walks root and reports coverage per directory, sorted by path.
//
// Skipped: _test.go files, dotted directories, testdata, and any directory
// holding its own go.mod. That last rule is why a nested module -- a
// scratch benchmark, a vendored example -- does not show up as a package
// with terrible coverage: it is not part of this module at all.
func Scan(root string) ([]Package, error) {
	byDir := map[string]*Package{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "testdata" {
				return fs.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(rel)
		pkg := byDir[dir]
		if pkg == nil {
			pkg = &Package{Dir: dir}
			byDir[dir] = pkg
		}
		if strings.HasSuffix(path, ".gen.go") {
			pkg.Generated = true
		}
		return scanFile(path, rel, pkg)
	})
	if err != nil {
		return nil, err
	}
	pkgs := make([]Package, 0, len(byDir))
	for _, p := range byDir {
		sort.Slice(p.Missing, func(i, j int) bool {
			if p.Missing[i].File != p.Missing[j].File {
				return p.Missing[i].File < p.Missing[j].File
			}
			return p.Missing[i].Line < p.Missing[j].Line
		})
		pkgs = append(pkgs, *p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Dir < pkgs[j].Dir })
	return pkgs, nil
}

// scanFile adds one file's exported declarations to pkg. A file that does
// not parse is skipped rather than fatal: the point is a coverage report,
// and the compiler is the one that should complain about broken syntax.
func scanFile(path, rel string, pkg *Package) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil
	}
	record := func(kind, name string, doc *ast.CommentGroup, pos token.Pos) {
		if doc != nil && len(doc.List) > 0 {
			pkg.Documented++
			return
		}
		pkg.Missing = append(pkg.Missing, Symbol{
			File: rel, Line: fset.Position(pos).Line, Kind: kind, Name: name,
		})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				recv := receiverName(d.Recv)
				// An unexported receiver hides the method whatever its own
				// name is: go doc never reaches it.
				if !ast.IsExported(recv) || !ast.IsExported(d.Name.Name) {
					continue
				}
				record("method", recv+"."+d.Name.Name, d.Doc, d.Pos())
				continue
			}
			if ast.IsExported(d.Name.Name) {
				record("func", d.Name.Name, d.Doc, d.Pos())
			}
		case *ast.GenDecl:
			scanGenDecl(d, record)
		}
	}
	return nil
}

// scanGenDecl handles types, struct fields, constants and variables. A
// spec with no doc of its own inherits the GenDecl's: that is how a
// "// Foo ..." above a single-spec "type ( ... )" block reads in go doc.
func scanGenDecl(d *ast.GenDecl, record func(kind, name string, doc *ast.CommentGroup, pos token.Pos)) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			doc := s.Doc
			if doc == nil {
				doc = d.Doc
			}
			if !ast.IsExported(s.Name.Name) {
				continue
			}
			record("type", s.Name.Name, doc, s.Pos())
			st, ok := s.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			for _, fld := range st.Fields.List {
				fdoc := fld.Doc
				if fdoc == nil {
					fdoc = fld.Comment // a trailing comment documents it too
				}
				for _, n := range fld.Names {
					if !ast.IsExported(n.Name) {
						continue
					}
					record("field", s.Name.Name+"."+n.Name, fdoc, n.Pos())
				}
			}
		case *ast.ValueSpec:
			doc := s.Doc
			if doc == nil {
				doc = d.Doc
			}
			if doc == nil {
				doc = s.Comment
			}
			for _, n := range s.Names {
				if !ast.IsExported(n.Name) {
					continue
				}
				record("value", n.Name, doc, n.Pos())
			}
		}
	}
}

// receiverName is the type name a method hangs off, with the pointer and
// any type parameters stripped: both "func (c *Conn)" and
// "func (i Interface[T])" answer with the bare name.
func receiverName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	switch e := t.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr: // Interface[T]
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.IndexListExpr: // Interface[T, U]
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}
