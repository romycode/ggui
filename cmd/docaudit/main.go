// Command docaudit reports how much of the exported surface carries a doc
// comment, per package, so a gap in "go doc" shows up as a number instead
// of being noticed by whoever reads the package next.
//
// It counts what go doc shows: exported types, functions, methods on
// exported types, struct fields, constants and variables. A trailing
// comment counts, and so does a comment on the enclosing const or var
// block -- both are what go doc renders.
//
// Usage:
//
//	go run ./cmd/docaudit            # table for the current module
//	go run ./cmd/docaudit -v         # plus every undocumented symbol
//	go run ./cmd/docaudit -min 83    # exit 1 below that overall percentage
//
// Full coverage is not the goal, and -min defaults to off for that reason.
// Some symbols are better left alone: an enum entry whose XML carries no
// summary, or Point.X on a type whose own comment already gives the units.
// A comment restating the identifier makes go doc worse, and this tool
// cannot tell the difference -- read the list, do not chase the number.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		root    = flag.String("root", ".", "directory to scan")
		verbose = flag.Bool("v", false, "list every undocumented symbol")
		min     = flag.Int("min", 0, "exit 1 if overall coverage is below this percentage")
	)
	flag.Parse()

	pkgs, err := Scan(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "docaudit:", err)
		os.Exit(2)
	}

	var totalDoc, totalMissing int
	fmt.Printf("%-44s %8s %8s %6s\n", "PACKAGE", "DOCUMENTED", "MISSING", "%")
	for _, p := range pkgs {
		if p.Documented+len(p.Missing) == 0 {
			continue // nothing exported: not a gap, just not a surface
		}
		note := ""
		if p.Generated {
			note = "  (generated)"
		}
		fmt.Printf("%-44s %8d %8d %5d%%%s\n", p.Dir, p.Documented, len(p.Missing), p.Percent(), note)
		totalDoc += p.Documented
		totalMissing += len(p.Missing)
	}
	overall := 100
	if totalDoc+totalMissing > 0 {
		overall = 100 * totalDoc / (totalDoc + totalMissing)
	}
	fmt.Printf("%-44s %8d %8d %5d%%\n", "TOTAL", totalDoc, totalMissing, overall)

	if *verbose {
		for _, p := range pkgs {
			if len(p.Missing) == 0 {
				continue
			}
			fmt.Printf("\n%s\n", p.Dir)
			for _, s := range p.Missing {
				fmt.Printf("  %s:%d %s %s\n", s.File, s.Line, s.Kind, s.Name)
			}
		}
	}

	if *min > 0 && overall < *min {
		fmt.Fprintf(os.Stderr, "docaudit: coverage %d%% is below the required %d%%\n", overall, *min)
		os.Exit(1)
	}
}
