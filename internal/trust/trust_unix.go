//go:build linux || darwin

// Ownership and permissions on Linux and macOS: the owner's user ID and the
// mode bits, compared with the effective user ID.

package trust

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// checkShared allows path to belong to root as well as to the current user,
// and to be writable by others only if it is a sticky directory.
func checkShared(path string, info os.FileInfo) error {
	if err := checkOwner(path, info); err != nil {
		return err
	}
	// The sticky bit stops others renaming or deleting a directory's entries,
	// which is what makes a shared ancestor like /tmp safe. On a regular file
	// it protects nothing: anyone with write permission can still change the
	// contents, so it excuses only directories.
	sticky := info.IsDir() && info.Mode()&os.ModeSticky != 0
	if info.Mode().Perm()&0o022 != 0 && !sticky {
		return writableError(path)
	}
	return nil
}

// checkOpenProgram applies checkShared to a program homeskillet is about to
// copy. info came from the open file, so it describes the bytes copied.
func checkOpenProgram(file *os.File, info os.FileInfo) error {
	return checkShared(file.Name(), info)
}

// checkOwner requires path, as described by info, to belong to the current
// user or root.
func checkOwner(path string, info os.FileInfo) error {
	owner, err := fileOwner(path, info)
	if err != nil {
		return err
	}
	if owner != 0 && owner != os.Geteuid() {
		return fmt.Errorf("%s is owned by another user", printable(path))
	}
	return nil
}

// fileOwner returns the user ID that owns path, as described by info. On Linux
// and macOS, file information from package os always carries a
// *syscall.Stat_t. The type is still checked so that anything else is an
// error, not a panic.
func fileOwner(path string, info os.FileInfo) (int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: file system reports no owner", printable(path))
	}
	return int(stat.Uid), nil
}

// checkPrivate requires path to belong to the current user and to be writable
// by nobody else. Group write is rejected even on systems with per-user groups,
// because homeskillet cannot cheaply prove that a group has no other members.
func checkPrivate(path string, info os.FileInfo) error {
	owner, err := fileOwner(path, info)
	if err != nil {
		return err
	}
	if owner != os.Geteuid() {
		return fmt.Errorf("%s is not owned by the current user", printable(path))
	}
	if info.Mode().Perm()&0o022 != 0 {
		return writableError(path)
	}
	return nil
}

// windowsDrive reports whether path is a Windows drive at one of WSL's
// default mount points, such as /mnt/c, or is below one.
func windowsDrive(path string) bool {
	rest, ok := strings.CutPrefix(path, "/mnt/")
	if !ok || rest == "" {
		return false
	}
	letter := rest[0]
	isLetter := 'a' <= letter && letter <= 'z' || 'A' <= letter && letter <= 'Z'
	return isLetter && (len(rest) == 1 || rest[1] == '/')
}

// writableError reports a path that others can write and says how to fix it.
// On WSL, Windows drives mounted under /mnt report every file as 0777 and
// silently ignore chmod unless mounted with the metadata option, so the usual
// advice would fail there and the repository belongs in the Linux filesystem.
func writableError(path string) error {
	if os.Getenv("WSL_DISTRO_NAME") != "" && windowsDrive(path) {
		return fmt.Errorf("%s is writable by group or others; on WSL, Windows drives ignore chmod, so clone the repository inside the Linux filesystem (for example under ~/)", printable(path))
	}
	return fmt.Errorf("%s is writable by group or others; run chmod go-w on it", printable(path))
}
