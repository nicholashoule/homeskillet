// Package host detects the platform homeskillet runs on: its operating
// system and processor architecture.
//
// Detection is best effort. A program knows the platform it was built for,
// but it can run elsewhere. macOS runs amd64 builds on Apple silicon through
// Rosetta 2, and Windows on Arm runs x64 builds through emulation; both are
// detected. Linux can run foreign binaries through emulators such as QEMU,
// which is not. So the result is a default, and --platform overrides it.
//
// Windows itself is not supported yet: a Windows build only says so (see
// main_unsupported.go). The detection here is ready for when it is.
package host

import (
	"runtime"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// Detect returns the platform of the machine homeskillet runs on, and
// whether this program runs translated: an amd64 build on Arm64 hardware,
// under Rosetta 2 on a Mac or emulation on Windows. Where translation cannot
// be detected, the program's own platform is reported and translated is
// false.
func Detect() (platform config.Platform, translated bool) {
	platform = Build()
	if platform.Arch == "amd64" && onArm64() {
		return config.Platform{OS: platform.OS, Arch: "arm64"}, true
	}
	return platform, false
}

// Build returns the platform this program was built for.
func Build() config.Platform {
	return config.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}
