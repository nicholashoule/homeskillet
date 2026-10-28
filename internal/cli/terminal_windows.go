// Recognizing a terminal on Windows.

package cli

import (
	"os"
	"syscall"
)

// isTerminal reports whether file is a console, which GetConsoleMode alone
// accepts. Go describes a console and NUL alike, as character devices that
// are the same file, so the Linux and macOS test would take every console
// for NUL; NUL, pipes, and files all fail GetConsoleMode.
func isTerminal(file *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) == nil
}
