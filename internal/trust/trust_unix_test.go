//go:build linux || darwin

// Tests of the ownership and permission rules for repositories, trees, and
// symlinks, and of the errors they print.

package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestCheckTree(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Not left to the umask: the rules under test need the ancestors not
	// writable by others.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	nested, file := filepath.Join(root, "a", "b"), filepath.Join(root, "a", "b", "file")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Symlink permission bits are meaningless, so a link (0777 on Linux) to a
	// private file in the tree must be accepted.
	if err := os.Symlink(filepath.Join("a", "b", "file"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := CheckTree(root); err != nil {
		t.Fatalf("private tree rejected: %v", err)
	}
	for _, test := range []struct {
		path        string
		shared, own os.FileMode
	}{
		{nested, 0o775, 0o755},
		{file, 0o606, 0o600},
	} {
		if err := os.Chmod(test.path, test.shared); err != nil {
			t.Fatal(err)
		}
		if err := CheckTree(root); err == nil || !strings.Contains(err.Error(), test.path) {
			t.Fatalf("shared %s accepted: %v", test.path, err)
		}
		if err := os.Chmod(test.path, test.own); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckTreeSymlinks(t *testing.T) {
	for name, test := range map[string]struct {
		destination string
		accept      bool
	}{
		"file in the tree":      {"file", true},
		"back into the tree":    {filepath.Join("..", "tree", "file"), true},
		"directory in the tree": {"dir", true},
		"file outside":          {filepath.Join("..", "outside", "file"), false},
		"directory outside":     {filepath.Join("..", "outside"), false},
		"absolute path outside": {"/etc/passwd", false},
		"dangling outside":      {"/nonexistent", false},
		"dangling in the tree":  {"missing", false},
		"loop":                  {"loop", false},
		// The kernel passes through outside on the way back in, so
		// whoever controls outside controls where the link leads.
		// A literal path: filepath.Join would clean the hop away.
		"through a directory outside": {"../outside/../tree/file", false},
		"absolute path into the tree": {"", true},
	} {
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			// Not left to the umask: the rules under test need the ancestors not
			// writable by others.
			if err := os.Chmod(root, 0o755); err != nil {
				t.Fatal(err)
			}
			tree, outside := filepath.Join(root, "tree"), filepath.Join(root, "outside")
			for _, dir := range []string{tree, filepath.Join(tree, "dir"), outside} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []string{filepath.Join(tree, "file"), filepath.Join(outside, "file")} {
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Even a private destination outside the tree is refused: the
			// check vouches only for what is inside it.
			destination := test.destination
			if destination == "" {
				destination = filepath.Join(tree, "file")
			}
			if err := os.Symlink(destination, filepath.Join(tree, "loop")); err != nil {
				t.Fatal(err)
			}
			err = CheckTree(tree)
			if test.accept && err != nil {
				t.Fatalf("symlink to %s rejected: %v", destination, err)
			}
			if !test.accept && err == nil {
				t.Fatalf("symlink to %s accepted", destination)
			}
		})
	}
}

// withMode reports a different mode for a real file, keeping its ownership,
// for modes a test cannot set, such as the sticky bit on a regular file, which
// macOS refuses to set for an unprivileged user.
type withMode struct {
	os.FileInfo
	mode os.FileMode
}

func (w withMode) Mode() os.FileMode { return w.mode }

func TestStickyExcusesOnlyDirectories(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Not left to the umask: the rules under test need the ancestors not
	// writable by others.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "program")
	if err := os.WriteFile(program, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(program)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := CheckProgram(file); err != nil {
		t.Fatalf("private program rejected: %v", err)
	}
	// The sticky bit restricts renaming a directory's entries; it does
	// nothing to stop others writing to a regular file.
	if err := checkShared(program, withMode{info, os.ModeSticky | 0o777}); err == nil {
		t.Fatal("world-writable program accepted because of the sticky bit")
	}
	dir, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkShared(root, withMode{dir, os.ModeDir | os.ModeSticky | 0o777}); err != nil {
		t.Fatalf("sticky directory rejected: %v", err)
	}
}

func TestCheckRepositoryManifest(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Not left to the umask: the rules under test need the ancestors not
	// writable by others.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, outside := filepath.Join(root, "repo"), filepath.Join(root, "outside")
	shared, private := filepath.Join(repo, "shared"), filepath.Join(repo, "private")
	for _, dir := range []string{repo, outside, shared, private} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// os.Mkdir applies the umask, so set the shared mode explicitly.
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{outside, shared, private} {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The manifest is optional, so a repository without one passes.
	if err := CheckRepository(repo); err != nil {
		t.Fatalf("repository without a manifest rejected: %v", err)
	}
	// Each name is checked, so neither can be the way around the rules.
	for _, name := range config.FileNames() {
		manifest := filepath.Join(repo, name)
		for destination, accept := range map[string]bool{
			filepath.Join("private", "manifest.json"): true,
			// Others can replace the private file through the shared directory.
			filepath.Join("shared", "manifest.json"): false,
			filepath.Join(outside, "manifest.json"):  false,
			"missing.json":                           false,
		} {
			if err := os.Symlink(destination, manifest); err != nil {
				t.Fatal(err)
			}
			if err := CheckRepository(repo); (err == nil) != accept {
				t.Errorf("%s -> %s: got %v, want accepted %v", name, destination, err, accept)
			}
			if err := os.Remove(manifest); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestErrorsQuoteUnprintableNames(t *testing.T) {
	err := writableError("/repo/evil\x1b[2Kname")
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Fatalf("raw escape character in error: %q", err)
	}
}

func TestWritableErrorWSLHint(t *testing.T) {
	const hint = "Linux filesystem"
	t.Setenv("WSL_DISTRO_NAME", "")
	if strings.Contains(writableError("/mnt/c/dotfiles").Error(), hint) {
		t.Fatal("WSL hint shown outside WSL")
	}
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	for path, want := range map[string]bool{
		"/mnt/c":           true,
		"/mnt/c/dotfiles":  true,
		"/mnt/D/dotfiles":  true,
		"/mnt/wsl/shared":  false,
		"/mnt/cdrom":       false,
		"/home/u/dotfiles": false,
	} {
		if got := strings.Contains(writableError(path).Error(), hint); got != want {
			t.Errorf("%s: hint %v, want %v", path, got, want)
		}
	}
}

// TestWithinIsExact checks that containment compares paths exactly: on a
// case-sensitive volume, ~/Repo is not inside ~/repo, and a symlink that
// resolves there must not count as staying in the repository.
func TestWithinIsExact(t *testing.T) {
	if Within("/home/u/dotfiles", "/home/u/Dotfiles/x") || !Within("/home/u/dotfiles", "/home/u/dotfiles/x") {
		t.Fatal("Within must compare exactly")
	}
	for _, test := range []struct {
		a, b string
		want bool
	}{
		{"/home/u/dotfiles", "/home/u/Dotfiles/.git/hooks", true},
		{"/home/u/Dotfiles/.git", "/home/u/dotfiles", true},
		{"/home/u/dotfiles", "/home/u/dotfiles-other", false},
	} {
		if got := Overlaps(test.a, test.b); got != test.want {
			t.Errorf("Overlaps(%q, %q) = %v, want %v", test.a, test.b, got, test.want)
		}
	}
}

// TestResolveRefusesCaseDistinctDirectory checks that a symlink in the
// repository to a sibling directory whose name differs only by case leads
// outside the repository on a case-sensitive volume: the sibling is another
// directory, which CheckTree never walks, so a script there would otherwise
// run unchecked.
func TestResolveRefusesCaseDistinctDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, sibling := filepath.Join(root, "repo"), filepath.Join(root, "Repo")
	for _, dir := range []string{repo, sibling} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	repoInfo, _ := os.Stat(repo)
	if siblingInfo, err := os.Stat(sibling); err == nil && os.SameFile(repoInfo, siblingInfo) {
		t.Skip("case-insensitive file system: Repo is the repository")
	}
	if err := os.WriteFile(filepath.Join(sibling, "install.sh"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../Repo", filepath.Join(repo, "scripts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(repo, filepath.Join(repo, "scripts", "install.sh")); err == nil {
		t.Fatal("a symlink to a case-distinct sibling directory resolved inside the repository")
	}
	if err := CheckTree(repo); err == nil {
		t.Fatal("CheckTree accepted a tree with a symlink leaving it")
	}
}
