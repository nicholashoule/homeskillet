//go:build linux || darwin

// What install says on Linux and macOS when the directory it put the command
// in is not on PATH: each shell's startup file sets PATH, and homeskillet
// edits none of them, so it says what to add.

package cli

import (
	"fmt"
	"io"
)

// addToPath writes the note that dir, where install put the command, is not
// on PATH, with how to put it there.
func addToPath(dir, _ string, _ bool, output io.Writer) error {
	_, err := fmt.Fprintf(output, notOnPathLine, dir, pathAdvice(dir))
	return err
}

// pathStep is prepare's next step while dir, where the command is, is not on
// PATH.
func pathStep(dir, _ string) string {
	return displayPath(dir) + " is not on PATH; " + pathAdvice(dir) + "."
}
