//go:build !linux && !darwin && !windows

// Command homeskillet runs on Linux, macOS, and Windows only: its ownership
// checks read each system's own file metadata. On any other system the
// program builds from this file instead, so that someone who runs, say, a
// FreeBSD build learns why it does nothing, rather than finding that it will
// not compile.
package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	_, _ = fmt.Fprintln(os.Stderr, unsupported(runtime.GOOS))
	os.Exit(1)
}

// unsupported explains that goos is not supported.
func unsupported(goos string) string {
	return "homeskillet: " + goos + " isn't supported at this time; homeskillet runs on Linux, macOS, and Windows"
}
