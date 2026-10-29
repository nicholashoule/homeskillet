// Recognizing a full disk on Windows.

package cli

import (
	"errors"
	"syscall"
)

// diskFull reports whether err comes from a write the disk had no room for:
// Windows's ERROR_HANDLE_DISK_FULL or ERROR_DISK_FULL.
func diskFull(err error) bool {
	return errors.Is(err, syscall.Errno(39)) || errors.Is(err, syscall.Errno(112))
}
