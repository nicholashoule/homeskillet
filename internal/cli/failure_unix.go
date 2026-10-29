//go:build linux || darwin

// Recognizing a full disk on Linux and macOS.

package cli

import (
	"errors"
	"syscall"
)

// diskFull reports whether err comes from a write the disk had no room for.
func diskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}
