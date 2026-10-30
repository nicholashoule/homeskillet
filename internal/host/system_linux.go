//go:build linux

// Describing Linux: its release from os-release, the kernel's release, and
// whether it runs under WSL or in a container.

package host

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/safefile"
)

// maxReleaseFile bounds what DescribeSystem reads of os-release and the
// kernel's release, each a few hundred bytes.
const maxReleaseFile = 64 << 10

// DescribeSystem returns this Linux's release, from /etc/os-release or
// /usr/lib/os-release as systemd documents them, the kernel's release, the
// WSL distribution from WSL_DISTRO_NAME, and a container runtime from the
// files Docker and Podman leave, or the container variable systemd and
// Podman set.
func DescribeSystem() System {
	var system System
	for _, name := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if data, err := safefile.Read(name, maxReleaseFile); err == nil {
			system.Name = osRelease(string(data))
			break
		}
	}
	if data, err := safefile.Read("/proc/sys/kernel/osrelease", maxReleaseFile); err == nil {
		system.Kernel = strings.TrimSpace(string(data))
	}
	system.WSL = os.Getenv("WSL_DISTRO_NAME")
	for name, runtime := range map[string]string{"/.dockerenv": "docker", "/run/.containerenv": "podman"} {
		if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) && err == nil {
			system.Container = runtime
		}
	}
	if system.Container == "" {
		system.Container = os.Getenv("container")
	}
	return system
}

// osRelease returns PRETTY_NAME from an os-release file, without its quotes,
// or NAME and VERSION when it has none.
func osRelease(text string) string {
	values := map[string]string{}
	for line := range strings.Lines(text) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = strings.Trim(value, `"'`)
		}
	}
	if values["PRETTY_NAME"] != "" {
		return values["PRETTY_NAME"]
	}
	return strings.TrimSpace(values["NAME"] + " " + values["VERSION"])
}
