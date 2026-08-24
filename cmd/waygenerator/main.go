// Command waygenerator turns the Wayland protocol XML files in protocols/
// into the Go bindings under wayland/, one .gen.go file per interface.
//
// It takes no flags: the input and output directories are fixed, because
// the whole point is that `make generate-protocols` is reproducible.
// wayland.xml becomes package wlcore alongside the hand-written runtime;
// every other protocol becomes a sibling package under wayland/.
//
// The work runs in four passes. Parse the XML, build a symbol table of
// every interface across every protocol, resolve references against it
// while checking that the core never depends on an extension, and emit.
// The table is what lets one protocol name a type from another, and the
// dependency check is what keeps wlcore free of imports back out to the
// generated packages.
//
// Hand-written files in the output directories are left alone: nothing is
// deleted, and only <type>.gen.go is written. Existing generated files for
// an interface that disappeared upstream are not cleaned up either --
// remove those by hand.
//
// See docs/waygenerator.md for the naming rules, the XML-to-Go type
// mapping and the contract with the runtime.
package main

import (
	"fmt"
	"os"

	"github.com/romycode/ggui/cmd/waygenerator/internal/codegen"
	"github.com/romycode/ggui/cmd/waygenerator/internal/resolve"
	"github.com/romycode/ggui/cmd/waygenerator/internal/symbols"
	"github.com/romycode/ggui/cmd/waygenerator/internal/xmlmodel"
)

func main() {
	if err := run("protocols", "wayland/wlcore"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run chains the 4 passes. Kept separate from main so it can be tested
// without invoking the binary or depending on the test process's working
// directory.
func run(protocolsDir, outDir string) error {
	protos, err := xmlmodel.ParseAll(protocolsDir)
	if err != nil {
		return err
	}
	table := symbols.Build(protos)
	model, err := resolve.Resolve(protos, table)
	if err != nil {
		return err
	}
	return codegen.Emit(model, outDir)
}
