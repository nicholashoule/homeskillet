//go:build !linux && !darwin

// Command homeskillet runs on Linux and macOS only: its ownership checks read
// Unix file metadata. On any other system the program builds from this file
// instead, so that someone who runs, say, a Windows build learns why it does
// nothing and where to run homeskillet instead, rather than finding that it
// will not compile.
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
	if goos == "windows" {
		return "homeskillet: Windows isn't supported at this time; run homeskillet inside WSL, such as Ubuntu on WSL, instead"
	}
	return "homeskillet: " + goos + " isn't supported at this time; homeskillet runs on Linux and macOS"
}
