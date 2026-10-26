//go:build linux || darwin

// The system-directory refusals: places homeskillet never acts on, such as
// /etc, /usr, or a directory holding other users' homes, checked before a
// home, repository, prefix, or data directory is read or written.

package trust

import (
	"fmt"
	"path/filepath"
	"strings"
)

// systemTrees are directories that belong to the operating system, refused
// together with everything below them. homeskillet only ever works in a home
// directory, a configuration repository, and an installation prefix, and none
// of those belongs here. A path inside one comes from a mistake such as
// --home /etc or --prefix /usr/local, and acting on it as root could damage
// the system. Linux and macOS names share one list; a name that does not
// exist on a system matches nothing there.
//
// /var is listed only in part. /var/tmp, macOS's per-user temporary
// directories under /private/var/folders, and root's macOS home /var/root are
// legitimate places to work.
var systemTrees = []string{
	// Linux and other Unix systems.
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32",
	"/proc", "/run", "/sbin", "/sys", "/usr",
	"/var/cache", "/var/db", "/var/lib", "/var/log", "/var/mail", "/var/spool",
	// macOS, where /etc and /var resolve into /private.
	"/Applications", "/Library", "/System", "/cores",
	"/private/etc", "/private/var/db", "/private/var/log", "/private/var/spool",
}

// homeParents are directories that hold other directories, such as every
// user's home, rather than being anyone's home themselves. They are refused
// exactly; the homes below them are not. "/" in particular is the home of
// accounts that have none, such as nobody in many containers, so HOME=/ must
// never lead to linking files into the root directory.
var homeParents = []string{"/", "/home", "/Users", "/var/home"}

// CheckLocation refuses a path that homeskillet has no reason to act on:
// anything other than a clean absolute path, a system directory or anything
// below one (see systemTrees), or a directory that holds other users' homes
// (see homeParents). Callers apply it to the canonical home directory,
// repository, and installation directories before reading or changing
// anything there.
//
// Names are compared without regard to case, because macOS file systems are
// usually case-insensitive, so /system is /System there. On Linux this can
// only refuse a few more improbable paths.
func CheckLocation(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s is not a clean absolute path", printable(path))
	}
	for _, parent := range homeParents {
		if strings.EqualFold(path, parent) {
			return fmt.Errorf("refusing to act on %s, which holds other users' directories, not one user's files", path)
		}
	}
	for _, tree := range systemTrees {
		if within(tree, path) {
			return fmt.Errorf("refusing to act on %s, which is inside the system directory %s", printable(path), tree)
		}
	}
	return nil
}

// within reports whether path is tree or below it, ignoring case.
func within(tree, path string) bool {
	if len(path) < len(tree) || !strings.EqualFold(path[:len(tree)], tree) {
		return false
	}
	return len(path) == len(tree) || path[len(tree)] == '/'
}
