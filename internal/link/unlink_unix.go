//go:build linux || darwin

// Removing a link on Linux and macOS.

package link

import (
	"os"
	"syscall"
)

// unlink removes the symlink at path. Unlike os.Remove, it never removes a
// directory, so a directory that replaced the link after Prepare looked is
// left alone, with an error, instead of deleted.
func unlink(path string) error {
	if err := syscall.Unlink(path); err != nil {
		return &os.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}
