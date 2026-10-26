//go:build darwin

// Detecting translation on macOS: whether an amd64 build runs under Rosetta 2
// on Apple silicon (see Detect).

package host

import "syscall"

// onArm64 reports whether this process runs translated on an Apple silicon
// Mac, as an amd64 build does under Rosetta 2. macOS sets the sysctl
// sysctl.proc_translated to 1 for a translated process and 0 for a native
// one; on Intel Macs, which have no Rosetta 2, it does not exist, which reads
// as native.
func onArm64() bool {
	translated, err := syscall.SysctlUint32("sysctl.proc_translated")
	return err == nil && translated == 1
}
