//go:build linux || darwin

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
	nested, file := filepath.Join(root, "a", "b"), filepath.Join(root, "a", "b", "file")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Symlink permission bits are meaningless, so a link (0777 on Linux) and
	// a dangling one must both be accepted.
	if err := os.Symlink("/nonexistent", filepath.Join(root, "dangling")); err != nil {
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

func TestCheckRepositoryManifest(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
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
	manifest := filepath.Join(repo, config.FileName)
	// The manifest is optional, so a repository without one passes.
	if err := CheckRepository(repo); err != nil {
		t.Fatalf("repository without a manifest rejected: %v", err)
	}
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
			t.Errorf("manifest -> %s: got %v, want accepted %v", destination, err, accept)
		}
		if err := os.Remove(manifest); err != nil {
			t.Fatal(err)
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
