//go:build linux || darwin || windows

// Tests of the mirror: a repository sent and received whole, a copy replaced
// only once a stream arrives complete, streams that try to leave the copy,
// and comparing a stream with a copy.

package mirror

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// repository creates a canonical repository holding each file, with its
// contents, and a .git directory that a copy leaves out.
func repository(t *testing.T, files map[string]string) string {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	files[".git/HEAD"] = "ref: refs/heads/main\n"
	for name, data := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// stream returns the stream Send writes for repo.
func stream(t *testing.T, repo string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := Send(context.Background(), &b, repo); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// contents lists every file under dir as "path=data", sorted.
func contents(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // G122: the test's own copy, in its own temporary directory
		rel, _ := filepath.Rel(dir, path)
		found = append(found, filepath.ToSlash(rel)+"="+string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	return found
}

// parent returns a private directory to keep copies in.
func parent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUpdate(t *testing.T) {
	repo := repository(t, map[string]string{"bootstrap.json": "{}", "home/wsl/.bashrc": "one", "home/empty/.keep": ""})
	dir := parent(t)
	updated, err := Update(bytes.NewReader(stream(t, repo)), dir, "dotfiles")
	if err != nil {
		t.Fatal(err)
	}
	copied := updated.Path
	if !updated.Created {
		t.Error("the first copy is not reported as created")
	}
	want := []string{"bootstrap.json={}", "home/empty/.keep=", "home/wsl/.bashrc=one"}
	if got := contents(t, copied); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(copied, "home", "wsl", ".bashrc")); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file not private: %v, %v", info, err)
		}
	}

	// A second stream replaces the copy whole, and leaves nothing beside it.
	if err := os.Remove(filepath.Join(repo, "home", "empty", ".keep")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "home", "wsl", ".bashrc"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err = Update(bytes.NewReader(stream(t, repo)), dir, "dotfiles")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Created || updated.Differences != (Differences{Changed: 1, Removed: 1}) {
		t.Errorf("second copy: %+v", updated)
	}
	want = []string{"bootstrap.json={}", "home/wsl/.bashrc=two"}
	if got := contents(t, copied); !slices.Equal(got, want) {
		t.Fatalf("after an update: got %v, want %v", got, want)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("left beside the copy: %v, %v", entries, err)
	}
	// An update with nothing new says so.
	if updated, err = Update(bytes.NewReader(stream(t, repo)), dir, "dotfiles"); err != nil || !updated.None() {
		t.Fatalf("third copy: %+v, %v", updated, err)
	}
}

// TestUpdateKeepsCopyOnBrokenStream checks that a stream that stops partway,
// or holds a path leaving the copy, changes nothing.
func TestUpdateKeepsCopyOnBrokenStream(t *testing.T) {
	repo := repository(t, map[string]string{"home/.profile": "kept"})
	dir := parent(t)
	whole := stream(t, repo)
	updated, err := Update(bytes.NewReader(whole), dir, "dotfiles")
	if err != nil {
		t.Fatal(err)
	}
	copied := updated.Path
	outside := filepath.Join(filepath.Dir(dir), "escaped")
	for name, data := range map[string]string{
		"cut short":       string(whole[:len(whole)-10]),
		"no end":          strings.TrimSuffix(string(whole), "end\n"),
		"data after end":  string(whole) + "more",
		"other version":   strings.Replace(string(whole), header, "homeskillet mirror 2", 1),
		"escaping path":   header + "\nf - 1 ../escaped\nx\nend\n",
		"absolute path":   header + "\nf - 1 /tmp/escaped\nx\nend\n",
		"backslash":       header + "\nf - 1 a\\..\\..\\escaped\nx\nend\n",
		"unclean path":    header + "\nd a\nf - 1 a/../escaped\nx\nend\n",
		"control":         header + "\nf - 1 a\x1b[2K\nx\nend\n",
		"too large":       header + "\nf - 999999999999 big\nend\n",
		"file in no dir":  header + "\nf - 1 missing/file\nx\nend\n",
		"unknown line":    header + "\nl target link\nend\n",
		"short file data": header + "\nf - 10 a\nx",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Update(strings.NewReader(data), dir, "dotfiles"); err == nil {
				t.Fatal("accepted")
			}
			if got := contents(t, copied); !slices.Equal(got, []string{"home/.profile=kept"}) {
				t.Fatalf("copy changed: %v", got)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
				t.Fatalf("left beside the copy: %v, %v", entries, err)
			}
			if _, err := os.Lstat(outside); err == nil {
				t.Fatal("wrote outside the copy")
			}
		})
	}
	for _, name := range []string{"", ".hidden", "-flag", "Upper", "a/b", ".."} {
		if _, err := Update(bytes.NewReader(whole), dir, name); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}

func TestCompare(t *testing.T) {
	repo := repository(t, map[string]string{"a": "one", "dir/b": "two"})
	dir := parent(t)
	updated, err := Update(bytes.NewReader(stream(t, repo)), dir, "dotfiles")
	if err != nil {
		t.Fatal(err)
	}
	copied := updated.Path
	if diff, err := Compare(bytes.NewReader(stream(t, repo)), copied); err != nil || !diff.None() {
		t.Fatalf("unchanged: %+v, %v", diff, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "c"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, "dir")); err != nil {
		t.Fatal(err)
	}
	before := contents(t, copied)
	diff, err := Compare(bytes.NewReader(stream(t, repo)), copied)
	if err != nil || diff != (Differences{Added: 1, Changed: 1, Removed: 1}) {
		t.Fatalf("got %+v, %v", diff, err)
	}
	if after := contents(t, copied); !slices.Equal(before, after) {
		t.Fatal("compare changed the copy")
	}
}

func TestSendRefusesSymlinks(t *testing.T) {
	repo := repository(t, map[string]string{"real": "x"})
	if err := os.Symlink("real", filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Send(context.Background(), new(bytes.Buffer), repo); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("got %v", err)
	}
}
