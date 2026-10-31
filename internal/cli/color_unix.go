//go:build linux || darwin

// Color codes on Linux and macOS terminals.

package cli

import "os"

// enableColor reports whether the terminal file takes color codes, which
// every terminal on Linux and macOS does but a dumb one (see colorFor).
func enableColor(*os.File) bool {
	return true
}
