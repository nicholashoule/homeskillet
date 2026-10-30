//go:build linux || darwin

// Recognizing root on Linux and macOS.

package cli

import "os"

// rootRefusal explains why homeskillet will not run as root without
// --allow-root.
const rootRefusal = "refusing to run as root; pass --allow-root to manage root's own dotfiles"

// privileged reports whether homeskillet runs as root (see runsAsRoot). Tests
// replace it to cover the refusal without root.
var privileged = runsAsRoot

// runsAsRoot reports whether homeskillet runs as root: an effective user ID
// of 0, as under sudo.
func runsAsRoot() bool {
	return os.Geteuid() == 0
}

// pathAdvice says how to put a directory on PATH.
func pathAdvice(string) string {
	return "add it in your shell startup file"
}

// translator names what runs an amd64 build on Arm64 hardware. Of Linux and
// macOS, only macOS detects it (see host.Detect): Rosetta 2.
func translator() string {
	return "Rosetta"
}
