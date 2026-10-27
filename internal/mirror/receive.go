//go:build linux || darwin || windows

// Receiving a stream: building a new copy and putting it in place of the old
// one whole, or comparing a stream with the copy there is.

package mirror

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Update reads a stream from r into a new directory and puts it in place of
// parent/name, returning that path. parent must exist and be private. The
// new copy is built beside the old and swapped in only once the whole stream
// has arrived, so a broken transfer leaves the old copy as it was, and links
// into the copy never lead into a half-written one. Leftovers of an earlier
// run that was stopped partway are removed first.
func Update(r io.Reader, parent, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("mirror: %q is not a name for a copy", name)
	}
	if err := removeLeftovers(parent, name); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, "."+name+".new-")
	if err != nil {
		return "", err
	}
	if err := receive(r, staging); err != nil {
		return "", errors.Join(err, os.RemoveAll(staging))
	}
	target := filepath.Join(parent, name)
	if err := swap(staging, target, parent, name); err != nil {
		return "", errors.Join(err, os.RemoveAll(staging))
	}
	return target, nil
}

// ValidName reports whether name can name a copy, a plain directory name:
// lowercase letters, digits, '.', '_', and '-', starting with a letter or
// digit.
func ValidName(name string) bool {
	if name == "" || name[0] == '.' || name[0] == '-' {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-'
	})
}

// removeLeftovers removes what an earlier Update of name left behind when it
// was stopped: a copy being built, or an old copy being replaced.
func removeLeftovers(parent, name string) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+name+".new-") || strings.HasPrefix(e.Name(), "."+name+".old-") {
			if err := os.RemoveAll(filepath.Join(parent, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// receive writes the stream from r into dir, which must be new and empty.
// Every write goes through an os.Root and creates its file or directory
// anew, so nothing in the stream can reach outside dir or through a link.
func receive(r io.Reader, dir string) error {
	rd, err := newReader(r)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck // every file written through it is closed and checked below
	for {
		e, ok, err := rd.next()
		if err != nil || !ok {
			return err
		}
		name := filepath.FromSlash(e.path)
		if e.dir {
			if err := root.Mkdir(name, e.mode()); err != nil {
				return err
			}
			continue
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, e.mode())
		if err != nil {
			return err
		}
		_, err = io.CopyN(file, rd.in, e.size)
		if errors.Is(err, io.EOF) {
			err = errors.New("mirror: the stream ended inside a file; nothing was changed")
		}
		if err := errors.Join(err, file.Sync(), file.Close()); err != nil {
			return err
		}
	}
}

// swap puts staging in place of target: the old copy, if any, moves into a
// directory of its own beside it, staging takes its name, and then the old
// copy is removed. Should the second rename fail, the old copy goes back.
func swap(staging, target, parent, name string) error {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return os.Rename(staging, target)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory homeskillet made; move it aside", target)
	}
	old, err := os.MkdirTemp(parent, "."+name+".old-")
	if err != nil {
		return err
	}
	moved := filepath.Join(old, name)
	if err := os.Rename(target, moved); err != nil {
		return errors.Join(err, os.Remove(old))
	}
	if err := os.Rename(staging, target); err != nil {
		return errors.Join(err, os.Rename(moved, target))
	}
	return os.RemoveAll(old)
}

// Differences counts how a stream differs from a copy.
type Differences struct {
	Added, Changed, Removed int
}

// None reports whether the stream matches the copy.
func (d Differences) None() bool {
	return d.Added+d.Changed+d.Removed == 0
}

// Compare reads a stream from r and reports how it differs from the copy at
// dir, changing nothing. A file whose contents or executable mark differ is
// changed; a directory or file the copy has and the stream does not is
// removed.
func Compare(r io.Reader, dir string) (Differences, error) {
	rd, err := newReader(r)
	if err != nil {
		return Differences{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Differences{}, err
	}
	defer root.Close() //nolint:errcheck // opened only to read
	var diff Differences
	sent := make(map[string]bool)
	for {
		e, ok, err := rd.next()
		if err != nil {
			return Differences{}, err
		}
		if !ok {
			break
		}
		sent[e.path] = true
		same, err := matches(root, e, rd)
		if err != nil {
			return Differences{}, err
		}
		switch {
		case same:
		case errors.Is(statError(root, e.path), fs.ErrNotExist):
			diff.Added++
		default:
			diff.Changed++
		}
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		if !sent[name] {
			diff.Removed++
			if d.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	return diff, err
}

// matches reports whether the copy holds e as the stream has it, reading the
// file's bytes from the stream in either case.
func matches(root *os.Root, e entry, rd *reader) (bool, error) {
	info, err := root.Lstat(filepath.FromSlash(e.path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	held := err == nil
	if e.dir {
		return held && info.IsDir(), nil
	}
	sent, err := io.ReadAll(io.LimitReader(rd.in, e.size))
	if err == nil && int64(len(sent)) != e.size {
		err = errors.New("mirror: the stream ended inside a file")
	}
	if err != nil {
		return false, err
	}
	// Windows keeps no executable bit, so there only the bytes count.
	if !held || !info.Mode().IsRegular() || info.Size() != e.size ||
		runtime.GOOS != "windows" && (info.Mode().Perm()&0o100 != 0) != e.executable {
		return false, nil
	}
	file, err := root.Open(filepath.FromSlash(e.path))
	if err != nil {
		return false, err
	}
	copied, err := io.ReadAll(io.LimitReader(file, e.size+1))
	if err := errors.Join(err, file.Close()); err != nil {
		return false, err
	}
	return bytes.Equal(copied, sent), nil
}

// statError returns the error of an Lstat of p in root, or nil.
func statError(root *os.Root, p string) error {
	_, err := root.Lstat(filepath.FromSlash(p))
	return err
}
