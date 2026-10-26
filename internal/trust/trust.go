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
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%s is not an absolute path", printable(dir))
	}
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

// Canonical returns path, which must be absolute, with symlinks resolved in
// its longest existing prefix. The rest does not exist yet and is appended
// unchanged. Like the paths the checks here receive, a canonical path can be
// compared as a string, even for a directory homeskillet has yet to create.
func Canonical(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s is not an absolute path", printable(path))
	}
	existing := filepath.Clean(path)
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if !errors.Is(err, fs.ErrNotExist) || parent == existing {
			return "", err
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	rest, err := filepath.Rel(existing, filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, rest), nil
}

// CheckCreatable applies CheckDirectory to dir if it exists, or else to its
// nearest existing ancestor, which is where creating dir would start. A
// directory homeskillet is about to create and write into is only as safe as
// the directory it is created in.
func CheckCreatable(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%s is not an absolute path", printable(dir))
	}
	for {
		_, err := os.Lstat(dir)
		if err == nil {
			return CheckDirectory(dir)
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, fs.ErrNotExist) || parent == dir {
			return err
		}
		dir = parent
	}
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

// checkManifest holds the manifest to the rules for a linked source (see
// Resolve): every directory and symlink on the way to it must be private and
// inside the repository. A manifest symlinked through a directory that others
// can write would otherwise let them swap in their own entries and setup
// steps, even though the file it points at is private.
//
// The manifest is optional (see config.Load). Its absence cannot be forged:
// deleting or renaming it needs write access to the repository directory,
// which CheckRepository has already checked.
func checkManifest(repo string) error {
	manifest := filepath.Join(repo, config.FileName)
	if _, err := os.Lstat(manifest); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_, err := Resolve(repo, manifest)
	return err
}

// maxHops bounds the symlinks Resolve follows, as the kernel's own limit
// (40 on Linux, 32 on macOS) does, so a loop fails instead of spinning.
const maxHops = 40

// Resolve returns path, which must be inside root, with every symlink
// resolved. Along the way it requires each directory it passes through, and
// the file it reaches, to be private (owned by the current user and writable
// by nobody else), and every symlink it follows, not only the last, to lead
// inside root. root must be absolute with symlinks resolved and already
// checked, with its ancestors, by CheckDirectory.
//
// Checking only the path as written and its final destination misses the
// hops between. A link that bounces through /tmp/shared/hop back into the
// repository resolves inside it, yet whoever controls /tmp/shared can retarget
// the hop after the check; a hop through a shared directory inside the
// repository lies on neither path. Walking one component at a time, as the
// kernel does, sees every directory and link that decides where the path
// leads, and applies each ".." to the resolved path, never lexically. A
// symlink's own permission bits are never used: the directory holding it,
// checked here, is what protects it.
//
// A missing component is reported with an error that matches fs.ErrNotExist.
func Resolve(root, path string) (string, error) {
	if !filepath.IsAbs(root) || !Within(root, path) {
		return "", fmt.Errorf("%s is not inside the absolute path %s", printable(path), printable(root))
	}
	rest, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	separator := string(filepath.Separator)
	pending := strings.Split(rest, separator)
	resolved, hops := root, 0
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, component)
		if !Within(root, next) {
			// Outside root, a path may only pass through root's ancestors
			// on its way back down into it, as an absolute link naming a
			// file in the repository does. CheckDirectory checked those.
			if !Within(next, root) {
				return "", fmt.Errorf("%s leads outside the repository %s", printable(path), printable(root))
			}
			resolved = next
			continue
		}
		info, err := os.Lstat(next)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if hops++; hops > maxHops {
				return "", fmt.Errorf("%s: too many levels of symlinks", printable(path))
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				resolved = separator
			}
			pending = append(strings.Split(target, separator), pending...)
			continue
		}
		if err := checkPrivate(next, info); err != nil {
			return "", err
		}
		resolved = next
	}
	if !Within(root, resolved) {
		return "", fmt.Errorf("%s leads outside the repository %s", printable(path), printable(root))
	}
	return resolved, nil
}

// CheckPath requires each component of path below root to be private; path
// must be inside root. Symlinks are skipped because their own permission bits
// are never used. What protects a symlink is the directory that holds it, and
// that directory is checked. Callers check a symlink's resolved path
// separately.
func CheckPath(root, path string) error {
	if !filepath.IsAbs(root) || !Within(root, path) {
		return fmt.Errorf("%s is not inside the absolute path %s", printable(path), printable(root))
	}
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

// CheckExisting applies CheckPath to the part of path that exists: each
// existing component below root must be private, and the rest, which
// homeskillet is about to create private, is not checked. A link is only as
// safe as the directory holding it: anyone who can write to that directory can
// replace the link with their own file, which then runs as the user.
func CheckExisting(root, path string) error {
	if !filepath.IsAbs(root) || !Within(root, path) {
		return fmt.Errorf("%s is not inside the absolute path %s", printable(path), printable(root))
	}
	existing := path
	for existing != root {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		existing = filepath.Dir(existing)
	}
	return CheckPath(root, existing)
}

// CheckTree requires everything under root, root included, to be private, and
// every symlink to lead, at every hop, to something under root (see Resolve).
// A setup step can read, source, or execute any file in the repository, so
// for setup it is not enough to check only the manifest and the files it
// names.
//
// A symlink's own permission bits are never used (see CheckPath), but what it
// points at is what a step runs. One that leads outside root, even to a
// private file or only on its way back in, would run code through a path this
// check never saw, possibly in a directory someone else can write; a dangling
// one could be completed later by whoever can create its destination.
func CheckTree(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%s is not an absolute path", printable(root))
	}
	// Symlinks name resolved paths, so walk from a resolved root.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(resolvedRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if _, err := Resolve(resolvedRoot, path); err != nil {
				return fmt.Errorf("symlink %s cannot be checked inside the repository: %w", printable(path), err)
			}
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
	// Compared without regard to case, as CheckLocation does: macOS file
	// systems are usually case-insensitive, so a repository opened as
	// ~/dotfiles is the same directory as ~/Dotfiles there, and a target
	// spelled the other way must still count as inside it. On Linux this
	// refuses only paths that differ from the repository or data directory
	// by case alone.
	relative, err := filepath.Rel(strings.ToLower(root), strings.ToLower(path))
	return err == nil && filepath.IsLocal(relative)
}

// CheckOwner requires path, following symlinks, to be owned by the current
// user or root. It is the weaker rule for files homeskillet only looks at and
// never runs or links, such as a program a setup step's provides names:
// ownership rules out another account, such as a user named linuxbrew on a
// shared machine, while a group that may write, as Homebrew's admin group
// does, can already act as root through sudo.
func CheckOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
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

// printable quotes a path that contains control or formatting characters.
// CheckTree reports arbitrary file names from the repository, and printing
// one raw could let it rewrite the terminal line that explains the error.
func printable(path string) string {
	if config.Printable(path) {
		return path
	}
	return strconv.Quote(path)
}
