// Describing Windows: its edition, version, and build, from the registry.

package host

import (
	"strconv"
	"strings"
	"syscall"
)

// currentVersion is the registry key that names this Windows.
const currentVersion = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`

// DescribeSystem returns this Windows's edition and version, such as Windows
// 11 Pro 24H2, and its build. Windows 11 still names itself Windows 10 in
// ProductName, so a build from 22000 on, Windows 11's first, says 11.
func DescribeSystem() System {
	var system System
	name, _ := RegistryString(syscall.HKEY_LOCAL_MACHINE, currentVersion, "ProductName")
	build, _ := RegistryString(syscall.HKEY_LOCAL_MACHINE, currentVersion, "CurrentBuild")
	version, _ := RegistryString(syscall.HKEY_LOCAL_MACHINE, currentVersion, "DisplayVersion")
	if n, err := strconv.Atoi(build); err == nil && n >= 22000 {
		name = strings.Replace(name, "Windows 10", "Windows 11", 1)
	}
	system.Name = strings.TrimSpace(name + " " + version)
	if build != "" {
		system.Kernel = "build " + build
	}
	return system
}
