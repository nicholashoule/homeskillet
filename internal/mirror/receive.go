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

// Updated is what Update did: where the copy is, whether it is the first,
// and otherwise how it differs from the one it replaced.
type Updated struct {
	Path    string
	Created bool
	Differences
}

// Update reads a stream from r into a new directory and puts it in place of
// parent/name. parent must exist and be private. The new copy is built
// beside the old and swapped in only once the whole stream has arrived, so a
// broken transfer leaves the old copy as it was, and links into the copy
// never lead into a half-written one. Leftovers of an earlier run that was
// stopped partway are removed first.
func Update(r io.Reader, parent, name string) (Updated, error) {
	if !ValidName(name) {
		return Updated{}, fmt.Errorf("mirror: %q is not a name for a copy", name)
	}
	if err := removeLeftovers(parent, name); err != nil {
		return Updated{}, err
	}
	target := filepath.Join(parent, name)
	var old *os.Root
	if info, err := os.Lstat(target); err == nil && info.IsDir() {
		if old, err = os.OpenRoot(target); err != nil {
			return Updated{}, err
		}
	}
	staging, err := os.MkdirTemp(parent, "."+name+".new-")
	if err != nil {
		return Updated{}, errors.Join(err, closeRoot(old))
	}
	diff, err := receive(r, staging, old)
	// Closed before the swap: Windows will not rename a directory while it
	// is open.
	if err = errors.Join(err, closeRoot(old)); err != nil {
		return Updated{}, errors.Join(err, os.RemoveAll(staging))
	}
	if err := swap(staging, target, parent, name); err != nil {
		return Updated{}, errors.Join(err, os.RemoveAll(staging))
	}
	return Updated{Path: target, Created: old == nil, Differences: diff}, nil
}

// closeRoot closes root, which may be nil.
func closeRoot(root *os.Root) error {
	if root == nil {
		return nil
	}
	return root.Close()
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

// receive writes the stream from r into dir, which must be new and empty,
// and counts how it differs from old, the copy it replaces, or nil when there
// is none. Every write goes through an os.Root and creates its file or
// directory anew, so nothing in the stream can reach outside dir or through a
// link.
func receive(r io.Reader, dir string, old *os.Root) (Differences, error) {
	rd, err := newReader(r)
	if err != nil {
		return Differences{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Differences{}, err
	}
	defer root.Close() //nolint:errcheck // every file written through it is closed and checked below
	t := tally{old: old, sent: map[string]bool{}}
	for {
		e, ok, err := rd.next()
		if err != nil {
			return Differences{}, err
		}
		if !ok {
			return t.finish()
		}
		name := filepath.FromSlash(e.path)
		var data []byte
		if e.dir {
			err = root.Mkdir(name, e.mode())
		} else if data, err = rd.file(e); err == nil {
			err = write(root, name, e.mode(), data)
		}
		if err == nil {
			err = t.note(e, data)
		}
		if err != nil {
			return Differences{}, err
		}
	}
}

// file reads the bytes of e, a file, from the stream. A stream that ends
// early is an error, so a broken transfer never counts as a whole copy.
func (rd *reader) file(e entry) ([]byte, error) {
	data := make([]byte, e.size)
	if _, err := io.ReadFull(rd.in, data); err != nil {
		return nil, errors.New("mirror: the stream ended inside a file; nothing was changed")
	}
	return data, nil
}

// write creates the file name in root, which must not exist, with data.
func write(root *os.Root, name string, mode os.FileMode, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	return errors.Join(err, file.Sync(), file.Close())
}

// tally counts how a stream differs from a copy, old, as its entries arrive.
// With no copy, every entry is added.
type tally struct {
	old  *os.Root
	sent map[string]bool
	diff Differences
}

// note counts e, with data its bytes when it is a file.
func (t *tally) note(e entry, data []byte) error {
	t.sent[e.path] = true
	if t.old == nil {
		t.diff.Added++
		return nil
	}
	same, held, err := holds(t.old, e, data)
	switch {
	case err != nil:
		return err
	case same:
	case held:
		t.diff.Changed++
	default:
		t.diff.Added++
	}
	return nil
}

// finish counts what the copy has and the stream did not, a directory once
// with all it holds, and returns the differences.
func (t *tally) finish() (Differences, error) {
	if t.old == nil {
		return t.diff, nil
	}
	err := fs.WalkDir(t.old.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		if !t.sent[name] {
			t.diff.Removed++
			if d.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	return t.diff, err
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
	t := tally{old: root, sent: map[string]bool{}}
	for {
		e, ok, err := rd.next()
		if err != nil {
			return Differences{}, err
		}
		if !ok {
			return t.finish()
		}
		var data []byte
		if !e.dir {
			if data, err = rd.file(e); err != nil {
				return Differences{}, err
			}
		}
		if err := t.note(e, data); err != nil {
			return Differences{}, err
		}
	}
}

// holds reports whether the copy in root has e as the stream has it, with
// data its bytes when it is a file, and whether it has anything at e's path.
func holds(root *os.Root, e entry, data []byte) (same, held bool, err error) {
	info, err := root.Lstat(filepath.FromSlash(e.path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if e.dir {
		return info.IsDir(), true, nil
	}
	// Windows keeps no executable bit, so there only the bytes count.
	if !info.Mode().IsRegular() || info.Size() != e.size ||
		runtime.GOOS != "windows" && (info.Mode().Perm()&0o100 != 0) != e.executable {
		return false, true, nil
	}
	file, err := root.Open(filepath.FromSlash(e.path))
	if err != nil {
		return false, true, err
	}
	copied, err := io.ReadAll(io.LimitReader(file, e.size+1))
	if err := errors.Join(err, file.Close()); err != nil {
		return false, true, err
	}
	return bytes.Equal(copied, data), true, nil
}
