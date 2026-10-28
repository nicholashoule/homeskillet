// Removing a link on Windows.

package link

import (
	"os"
	"syscall"
)

// unlink removes the file symlink at path. DeleteFile, like unlink(2), never
// removes a directory, so a directory that replaced the link after Prepare
// looked is left alone, with an error, instead of deleted; and it removes a
// symlink itself, never the file it points to.
func unlink(path string) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err == nil {
		err = syscall.DeleteFile(name)
	}
	if err != nil {
		return &os.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}
