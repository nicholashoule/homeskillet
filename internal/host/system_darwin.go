//go:build darwin

// Describing macOS: its product version and the kernel's release, through
// sysctl.

package host

import "syscall"

// DescribeSystem returns this Mac's release, as macOS and its product
// version, and the kernel's release.
func DescribeSystem() System {
	var system System
	if version, err := syscall.Sysctl("kern.osproductversion"); err == nil {
		system.Name = "macOS " + version
	}
	if release, err := syscall.Sysctl("kern.osrelease"); err == nil {
		system.Kernel = release
	}
	return system
}
