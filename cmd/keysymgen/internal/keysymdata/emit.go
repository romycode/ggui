package keysymdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Emit generates Go source from inputPath and atomically writes it to outputPath.
func Emit(inputPath, outputPath string) (err error) {
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("keysymgen: open input %q: %w", inputPath, err)
	}

	generated, generateErr := Generate(input)
	if closeErr := input.Close(); closeErr != nil {
		closeErr = fmt.Errorf("keysymgen: close input %q: %w", inputPath, closeErr)
		if generateErr != nil {
			return errors.Join(generateErr, closeErr)
		}
		return closeErr
	}
	if generateErr != nil {
		return generateErr
	}

	outputDir := filepath.Dir(outputPath)
	temporary, err := os.CreateTemp(outputDir, ".keysymgen-*")
	if err != nil {
		return fmt.Errorf("keysymgen: create temporary output in %q: %w", outputDir, err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if temporaryName == "" {
			return
		}
		if removeErr := os.Remove(temporaryName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			removeErr = fmt.Errorf("keysymgen: remove temporary output %q: %w", temporaryName, removeErr)
			if err == nil {
				err = removeErr
			} else {
				err = errors.Join(err, removeErr)
			}
		}
	}()

	if err := temporary.Chmod(0o644); err != nil {
		return closeTemporary(temporary, temporaryName, fmt.Errorf("keysymgen: set mode on temporary output %q: %w", temporaryName, err))
	}
	if _, err := temporary.Write(generated); err != nil {
		return closeTemporary(temporary, temporaryName, fmt.Errorf("keysymgen: write temporary output %q: %w", temporaryName, err))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("keysymgen: close temporary output %q: %w", temporaryName, err)
	}
	if err := os.Rename(temporaryName, outputPath); err != nil {
		return fmt.Errorf("keysymgen: rename temporary output %q to %q: %w", temporaryName, outputPath, err)
	}
	temporaryName = ""

	return nil
}

func closeTemporary(temporary *os.File, temporaryName string, cause error) error {
	if err := temporary.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("keysymgen: close temporary output %q: %w", temporaryName, err))
	}
	return cause
}
