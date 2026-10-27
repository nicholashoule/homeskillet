//go:build linux || darwin

// Recognizing a terminal on Linux and macOS.

package cli

import "os"

// isTerminal reports whether file is a terminal: a character device other
// than /dev/null. /dev/null is a character device too, but nobody reads it.
// Taken for a terminal, it would get the view, whose explained errors main
// does not print again, so homeskillet link >/dev/null would fail in silence.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, null)
}
