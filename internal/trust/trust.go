//go:build linux || darwin

// Package trust decides whether files in a configuration repository, and the
// directories and programs homeskillet installs itself from and into, are
// safe to act on.
//
// Linked files run with the user's privileges: ~/.bashrc is shell code, and
// many other dotfiles name programs to execute. Setup steps are commands from
// the repository outright. Anyone who can modify the repository can therefore
// run code as the user, so these checks refuse a repository that someone else
// could modify, following the rules OpenSSH's StrictModes applies to ~/.ssh.
//
// Ownership is compared with the effective user ID, the identity the kernel
// uses for permission checks. Under sudo that is root, so a repository owned by
// the invoking user is rejected.
package trust

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// CheckRepository checks the repository directory and every directory above
// it (see CheckDirectory), and its manifest if it has one. repo must be
// absolute with symlinks resolved.
func CheckRepository(repo string) error {
	if err := CheckDirectory(repo); err != nil {
		return err
	}
	return checkManifest(repo)
}

// CheckDirectory requires dir to be owned by the current user and writable by
// nobody else, and every directory above it to be owned by that user or root
// and writable by nobody else unless it is sticky. dir must be absolute with
// symlinks resolved.
//
// Ancestors matter because a user who can write to /a can rename /a/repo away
// and put their own directory in its place, and existing links would then point
// into it. An ancestor may belong to root, who could do anything anyway. It may
// be writable by others only if it is sticky, like /tmp, because the sticky bit
// stops users from renaming entries they do not own.
func CheckDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if err := checkPrivate(dir, info); err != nil {
		return err
	}
	for dir != filepath.Dir(dir) {
		dir = filepath.Dir(dir)
		info, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if err := checkShared(dir, info); err != nil {
			return err
		}
	}
	return nil
}

// CheckProgram requires a program that homeskillet is about to copy to be a
// regular file owned by the current user or root and writable by nobody else.
// info must come from the open file, not from its path, so the check applies
// to exactly the bytes that are then read. A program owned by root is as
// trustworthy as the system it came with; one owned by another user is not,
// and under sudo that includes the invoking user.
func CheckProgram(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", printable(path))
	}
	return checkShared(path, info)
}

// checkShared allows path to belong to root as well as to the current user,
// and to be writable by others only if it is a sticky directory.
func checkShared(path string, info os.FileInfo) error {
	owner, err := fileOwner(path, info)
	if err != nil {
		return err
	}
	if owner != 0 && owner != os.Geteuid() {
		return fmt.Errorf("%s is owned by another user", printable(path))
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return writableError(path)
	}
	return nil
}

// checkManifest holds the manifest to the rules for a linked source: it must
// resolve inside the repository, and every path to it must be private. A
// manifest symlinked into a directory that others can write would otherwise
// let them swap in their own entries and setup steps, even though the file it
// points at is private.
//
// The manifest is optional (see config.Load). Its absence cannot be forged:
// deleting or renaming it needs write access to the repository directory,
// which CheckRepository has already checked.
func checkManifest(repo string) error {
	manifest := filepath.Join(repo, config.FileName)
	if _, err := os.Lstat(manifest); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		return err
	}
	if !Within(repo, resolved) {
		return fmt.Errorf("%s resolves outside the repository", printable(manifest))
	}
	if err := CheckPath(repo, manifest); err != nil {
		return err
	}
	if resolved != manifest {
		return CheckPath(repo, resolved)
	}
	return nil
}

// CheckPath requires each component of path below root to be private; path
// must be inside root. Symlinks are skipped because their own permission bits
// are never used. What protects a symlink is the directory that holds it, and
// that directory is checked. Callers check a symlink's resolved path
// separately.
func CheckPath(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	current := root
	for component := range strings.SplitSeq(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if err := checkPrivate(current, info); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckTree requires everything under root, root included, to be private. A
// setup step can read, source, or execute any file in the repository, so for
// setup it is not enough to check only the manifest and the files it names.
// Symlinks are skipped for the reason given in CheckPath.
func CheckTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return checkPrivate(path, info)
	})
}

// Within reports whether path is root itself or lies below it. It compares
// path strings only, so both must be clean and absolute, with symlinks already
// resolved wherever the answer guards a filesystem change.
func Within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(relative)
}

// fileOwner returns the user ID that owns path, as described by info. On Linux
// and macOS, the only systems this package builds for, file information from
// package os always carries a *syscall.Stat_t. The type is still checked so
// that anything else is an error, not a panic.
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

// windowsDrive matches WSL's default mount points for Windows drives.
var windowsDrive = regexp.MustCompile(`^/mnt/[A-Za-z](/|$)`)

// writableError reports a path that others can write and says how to fix it.
// On WSL, Windows drives mounted under /mnt report every file as 0777 and
// silently ignore chmod unless mounted with the metadata option, so the usual
// advice would fail there and the repository belongs in the Linux filesystem.
func writableError(path string) error {
	if os.Getenv("WSL_DISTRO_NAME") != "" && windowsDrive.MatchString(path) {
		return fmt.Errorf("%s is writable by group or others; on WSL, Windows drives ignore chmod, so clone the repository inside the Linux filesystem (for example under ~/)", printable(path))
	}
	return fmt.Errorf("%s is writable by group or others; run chmod go-w on it", printable(path))
}

// printable quotes a path that contains control or formatting characters.
// CheckTree reports arbitrary file names from the repository, and printing
// one raw could let it rewrite the terminal line that explains the error.
func printable(path string) string {
	if config.Printable(path) {
		return path
	}
	return strconv.Quote(path)
}
