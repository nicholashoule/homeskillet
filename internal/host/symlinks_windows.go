// Whether this process may make symlinks, which Windows allows only in
// Developer Mode or to an elevated administrator.

package host

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// CheckSymlinks returns an error saying how to let this process make
// symlinks when Windows would refuse it one, and nil when it would not.
// Callers check before changing anything, so a link that cannot be made
// never leaves half of its work done. It tries once per process (see
// ProbeSymlinks).
var CheckSymlinks = sync.OnceValue(ProbeSymlinks)

// ProbeSymlinks is CheckSymlinks without remembering the answer, for a caller
// that checks again after Developer Mode may have been turned on, as
// homeskillet prepare does. It tries, in a directory of its own under the
// temporary directory, which it removes again.
func ProbeSymlinks() error {
	dir, err := os.MkdirTemp("", "homeskillet-symlink-")
	if err != nil {
		return err
	}
	err = os.Symlink(dir, filepath.Join(dir, "probe"))
	if errors.Is(err, syscall.ERROR_PRIVILEGE_NOT_HELD) {
		err = errors.New("homeskillet links with symlinks, which Windows allows only in Developer Mode\n" +
			"  turn it on in Settings, System, For developers (start ms-settings:developers opens that page),\n" +
			"  then run the command again; nothing has changed")
	}
	return errors.Join(err, os.RemoveAll(dir))
}
