package main

import (
	"fmt"
	"os"

	"github.com/romycode/ggui/cmd/keysymgen/internal/keysymdata"
)

const (
	defaultInput  = "third_party/libxkbcommon/xkbcommon-keysyms.h"
	defaultOutput = "keyboard/keysyms.gen.go"
)

func main() {
	if err := run(defaultInput, defaultOutput); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inputPath, outputPath string) error {
	return keysymdata.Emit(inputPath, outputPath)
}
