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

// run encadena las 4 pasadas. Separado de main para poder probarlo sin
// invocar el binario ni depender del directorio de trabajo del proceso de
// test.
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
