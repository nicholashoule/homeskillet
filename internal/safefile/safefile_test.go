// Tests of bounded reads and atomic writes.

package safefile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := Read(path, 5); err != nil || string(data) != "12345" {
		t.Fatalf("at the limit: %q, %v", data, err)
	}
	if _, err := Read(path, 4); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := Read(filepath.Join(t.TempDir(), "missing"), 5); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	path := filepath.Join(dir, "file")
	failure := errors.New("disk full")
	if err := Write(path, 0o600, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("write failure not returned: %v", err)
	}
	// Neither the file nor a temporary leftover may remain.
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("failed write left %v, %v", entries, err)
	}
	for _, contents := range []string{"first", "second"} {
		if err := Write(path, 0o640, func(w io.Writer) error {
			_, err := io.WriteString(w, contents)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != contents {
			t.Fatalf("read %q, %v; want %q", data, err, contents)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("successful writes left %v, %v", entries, err)
	}
	// Windows has no Unix permission bits to check.
	if runtime.GOOS == "windows" {
		return
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode: %v, %v", info, err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("new directory must be private: %v, %v", info, err)
	}
}

// TestCreate checks that Create writes a new file whole, and that it never
// replaces anything already at the path, a file, a directory, or a symlink,
// even a dangling one, leaving no temporary file either way.
func TestCreate(t *testing.T) {
	dir := t.TempDir()
	contents := func(text string) func(io.Writer) error {
		return func(w io.Writer) error {
			_, err := io.WriteString(w, text)
			return err
		}
	}
	path := filepath.Join(dir, "file")
	if err := Create(path, 0o640, contents("first")); err != nil {
		t.Fatal(err)
	}
	if err := Create(path, 0o640, contents("second")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("existing file: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "first" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Create(filepath.Join(dir, "directory"), 0o640, contents("x")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("existing directory: %v", err)
	}
	// Windows needs a privilege to make symlinks; elsewhere, a dangling one
	// must count as present too.
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink("missing", dangling); err == nil {
		if err := Create(dangling, 0o640, contents("x")); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("dangling symlink: %v", err)
		}
		if target, err := os.Readlink(dangling); err != nil || target != "missing" {
			t.Fatalf("dangling symlink replaced: %q, %v", target, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), TempPrefix) {
			t.Errorf("temporary file left: %s", entry.Name())
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode: %v, %v", info, err)
	}
}
