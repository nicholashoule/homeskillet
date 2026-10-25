package host

import (
	"runtime"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestDetect(t *testing.T) {
	platform, translated := Detect()
	build := Build()
	if build != (config.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}) {
		t.Fatalf("Build = %v", build)
	}
	if platform.OS != runtime.GOOS || platform.Arch == "" {
		t.Fatalf("Detect = %v", platform)
	}
	// Only an amd64 build on macOS or Windows can be translated, and then the
	// hardware is arm64; everywhere else the detected platform is the build's.
	if translated {
		if build.Arch != "amd64" || build.OS == "linux" || platform != (config.Platform{OS: build.OS, Arch: "arm64"}) {
			t.Fatalf("translated %v build detected as %v", build, platform)
		}
	} else if platform != build {
		t.Fatalf("native build %v detected as %v", build, platform)
	}
}
