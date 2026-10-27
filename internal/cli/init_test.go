//go:build linux || darwin || windows

// Tests of init: the starter manifest it writes, that it never replaces one,
// its dry run, and its refusal of a repository others could write to.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
)

// emptyRepository returns a new, private, empty directory for init.
func emptyRepository(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Not left to the umask: the ownership rules need the ancestors of a
	// repository not writable by others.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "dotfiles")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

// statusOf runs status for repo in home.
func statusOf(repo, home string) (string, error) {
	var output bytes.Buffer
	err := Run(context.Background(), []string{"status", repo, "--home", home}, nil, &output, &output)
	return output.String(), err
}

func initialize(args ...string) (string, error) {
	var output bytes.Buffer
	err := Run(context.Background(), append([]string{"init"}, args...), nil, &output, &output)
	return output.String(), err
}

// TestInit checks that init writes a starter manifest that Load accepts, for
// this machine's operating system or --platform, with an empty home/ and no
// files listed, so files under home/ are still found.
func TestInit(t *testing.T) {
	detected, _ := host.Detect()
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, detected.OS},
		{[]string{"--platform", "darwin/arm64"}, "darwin/arm64"},
	} {
		repo := emptyRepository(t)
		output, err := initialize(append([]string{repo}, test.args...)...)
		if err != nil || !strings.Contains(output, "created\t"+filepath.Join(repo, config.FileName)) {
			t.Fatalf("%v: %q, %v", test.args, output, err)
		}
		manifest, err := config.Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(manifest.Platforms, []string{test.want}) || len(manifest.Files) != 0 || len(manifest.Setup) != 0 {
			t.Errorf("%v: manifest %+v", test.args, manifest)
		}
		// The repository works at once, empty as it is, on its platform.
		if test.want == detected.OS {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if output, err := statusOf(repo, home); err != nil {
				t.Errorf("status of the new repository: %q, %v", output, err)
			}
		}
		// A file added under home/ afterward is found without a list.
		if err := os.WriteFile(filepath.Join(repo, config.HomeDir, ".bashrc"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if manifest, err := config.Load(repo); err != nil || len(manifest.Files) != 1 {
			t.Errorf("%v: file under home/ not found: %+v, %v", test.args, manifest.Files, err)
		}
	}
}

// TestInitNeverReplaces checks that an existing manifest, even a symlink, is
// left alone, and that an existing home/ is kept as it is.
func TestInitNeverReplaces(t *testing.T) {
	requireSymlinks(t)
	repo := emptyRepository(t)
	path := filepath.Join(repo, config.FileName)
	if err := os.WriteFile(path, []byte(`{"setup": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize(repo); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing manifest: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != `{"setup": []}` {
		t.Fatalf("manifest changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(repo, config.HomeDir)); err == nil {
		t.Fatal("a refused init created home/")
	}

	repo = emptyRepository(t)
	if err := os.Symlink("elsewhere.json", filepath.Join(repo, config.FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize(repo); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("dangling manifest symlink: %v", err)
	}

	// A manifest under the other name is not added to either.
	repo = emptyRepository(t)
	if err := os.WriteFile(filepath.Join(repo, config.DotfilesFileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize(repo); err == nil || !strings.Contains(err.Error(), config.DotfilesFileName+" already exists") ||
		exists(filepath.Join(repo, config.FileName)) {
		t.Fatalf("%s beside %s: %v", config.FileName, config.DotfilesFileName, err)
	}

	repo = emptyRepository(t)
	kept := filepath.Join(repo, config.HomeDir, ".vimrc")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := initialize(repo)
	if err != nil || strings.Contains(output, "created\t"+filepath.Dir(kept)) || !exists(kept) {
		t.Fatalf("existing home/: %q, %v", output, err)
	}
}

// TestInitDryRun checks that a dry run prints the manifest and changes
// nothing.
func TestInitDryRun(t *testing.T) {
	repo := emptyRepository(t)
	output, err := initialize(repo, "--dry-run")
	if err != nil || !strings.Contains(output, `"platforms": [`) || !strings.Contains(output, "dry run: nothing changed") {
		t.Fatalf("%q, %v", output, err)
	}
	if entries, err := os.ReadDir(repo); err != nil || len(entries) != 0 {
		t.Fatalf("dry run changed the repository: %v, %v", entries, err)
	}
}

// TestInitRefusesSharedRepository checks that init writes nothing into a
// directory another user could write to, since the manifest decides what runs.
func TestInitRefusesSharedRepository(t *testing.T) {
	requireUnix(t)
	repo := emptyRepository(t)
	if err := os.Chmod(repo, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := initialize(repo); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("shared repository: %v", err)
	}
	if entries, err := os.ReadDir(repo); err != nil || len(entries) != 0 {
		t.Fatalf("refused init changed the repository: %v, %v", entries, err)
	}
}

// TestInitRefusesUnusableHome checks that init writes nothing when home/
// exists but Load would refuse it, and accepts a symlink to a directory
// inside the repository, as Load does.
func TestInitRefusesUnusableHome(t *testing.T) {
	requireSymlinks(t)
	outside := t.TempDir()
	for name, test := range map[string]struct {
		make func(t *testing.T, repo string)
		ok   bool
	}{
		"a file": {func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, config.HomeDir), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		"a dangling symlink": {func(t *testing.T, repo string) {
			if err := os.Symlink("missing", filepath.Join(repo, config.HomeDir)); err != nil {
				t.Fatal(err)
			}
		}, false},
		"a symlink out of the repository": {func(t *testing.T, repo string) {
			if err := os.Symlink(outside, filepath.Join(repo, config.HomeDir)); err != nil {
				t.Fatal(err)
			}
		}, false},
		"a symlink to a directory inside": {func(t *testing.T, repo string) {
			if err := os.Mkdir(filepath.Join(repo, "laptop"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("laptop", filepath.Join(repo, config.HomeDir)); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			repo := emptyRepository(t)
			test.make(t, repo)
			_, err := initialize(repo)
			if written := exists(filepath.Join(repo, config.FileName)); (err == nil) != test.ok || written != test.ok {
				t.Fatalf("error %v, manifest written %v", err, written)
			}
		})
	}
}

func TestInitArguments(t *testing.T) {
	repo := emptyRepository(t)
	for _, args := range [][]string{
		{repo, "--home", repo}, {repo, "--force"}, {repo, "--yes"}, {repo, "--data-dir", repo}, {repo, "extra"},
	} {
		if _, err := parse(append([]string{"init"}, args...), new(bytes.Buffer)); err == nil {
			t.Errorf("init accepted %q", args)
		}
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
