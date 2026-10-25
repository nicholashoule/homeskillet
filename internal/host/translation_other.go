//go:build !darwin && !windows

package host

// onArm64 reports false: homeskillet detects translation only on macOS and
// Windows. Elsewhere, such as Linux running an amd64 build through QEMU, the
// build's own architecture is reported, and --platform corrects it.
func onArm64() bool {
	return false
}
