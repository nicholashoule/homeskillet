//go:build linux || darwin || windows

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
// The rules are the same everywhere; what "owned by" and "writable by" mean is
// not. On Linux and macOS (trust_unix.go), ownership is compared with the
// effective user ID, the identity the kernel uses for permission checks, and
// permissions are mode bits. Under sudo that is root, so a repository owned
// by the invoking user is rejected. On Windows (trust_windows.go), they come
// from each file's security descriptor: its owner, and the access control
// list that says who may change it.
package trust

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// CheckRepository checks the repository directory and every directory above
// it (see CheckDirectory), and its manifests if it has any. repo must be
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

// CheckProgram requires a program that homeskillet is about to copy, already
// open as file, to be a regular file owned by the current user or root and
// writable by nobody else. The check reads the open file, not its path, so it
// applies to exactly the bytes that are then read. A program owned by root is
// as trustworthy as the system it came with; one owned by another user is
// not, and under sudo that includes the invoking user.
func CheckProgram(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", printable(file.Name()))
	}
	return checkOpenProgram(file, info)
}

// checkManifest holds each manifest the repository has (see
// config.FileNames) to the rules for a linked source (see Resolve): every
// directory and symlink on the way to it must be private and inside the
// repository. A manifest symlinked through a directory that others can write
// would otherwise let them swap in their own entries and setup steps, even
// though the file it points at is private.
//
// Manifests are optional (see config.Load). Their absence cannot be forged:
// deleting or renaming one needs write access to the repository directory,
// which CheckRepository has already checked.
func checkManifest(repo string) error {
	for _, name := range config.FileNames() {
		manifest := filepath.Join(repo, name)
		if _, err := os.Lstat(manifest); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := Resolve(repo, manifest); err != nil {
			return err
		}
	}
	return nil
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
	pending := components(rest)
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
			// An absolute target starts again from the root of its volume.
			// So does one that starts with a separator but names no volume,
			// which Windows reads as relative to the root of the link's own
			// volume; on Linux and macOS, every such target is absolute and
			// volumes are always empty.
			if volume := filepath.VolumeName(target); volume != "" || strings.HasPrefix(target, separator) {
				// C:file means file in drive C's current directory, which
				// depends on the process, not on the link.
				if volume != "" && !filepath.IsAbs(target) {
					return "", fmt.Errorf("%s: %s names a drive but no directory", printable(path), printable(next))
				}
				if volume == "" {
					volume = filepath.VolumeName(root)
				}
				resolved = volume + separator
				target = target[len(filepath.VolumeName(target)):]
			}
			pending = append(components(target), pending...)
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
	for _, component := range components(relative) {
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
// resolved wherever the answer guards a filesystem change. On Windows the
// comparison ignores case, as Windows does.
func Within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(relative)
}

// Overlaps reports whether one of a and b is the other or lies below it,
// compared without regard to case. It is for refusals only, such as a link
// target that may not lie inside the repository: on macOS, whose file systems
// are usually case-insensitive, ~/Dotfiles is the repository opened as
// ~/dotfiles, so a target spelled either way must be refused. It never
// decides that a path is safely inside a root; Within does that, exactly,
// because on a case-sensitive volume ~/Repo is a different directory than
// ~/repo, and treating it as inside would let a symlink reach it unchecked.
func Overlaps(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return Within(a, b) || Within(b, a)
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
	return checkOwner(path, info)
}

// SamePath reports whether a and b name the same path: exactly on Linux and
// macOS, and without regard to case on Windows, whose file systems ignore
// it. Both must be clean and absolute, as for Within, which filepath.Rel
// already makes case-insensitive on Windows.
func SamePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// PathKey returns path in the form to use as a map key wherever SamePath
// decides which paths are the same: lowercase on Windows, unchanged elsewhere.
func PathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// components splits path at every separator, dropping empty names. Windows
// accepts both / and \, so a symlink written with either is read the same.
func components(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == filepath.Separator })
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
