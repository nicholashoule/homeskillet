// Describing the operating system for a person, as homeskillet term reports
// it in a bug report.

package host

// System is the operating system as a person reads it. Detection is best
// effort and changes nothing: a field it cannot read is "".
type System struct {
	// Name is the release, such as Fedora Linux 44 (Workstation Edition),
	// macOS 15.1, or Windows 11 Pro 24H2.
	Name string
	// Kernel is the kernel's release on Linux and macOS, and the build on
	// Windows.
	Kernel string
	// WSL is the WSL distribution, when Linux runs under WSL.
	WSL string
	// Container is the container runtime, such as docker or podman, when
	// Linux runs in a container.
	Container string
}
