//go:build !windows

// Whether this process may make symlinks: always, on every system but
// Windows.

package host

// CheckSymlinks returns nil: any user may make symlinks on Linux and macOS.
// On Windows it says how to enable them when they are not allowed.
func CheckSymlinks() error {
	return nil
}
