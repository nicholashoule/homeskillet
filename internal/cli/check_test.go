//go:build linux || darwin || windows

// Tests of check: a valid repository, every issue listed for scripts and in
// the view, and that it changes nothing.

package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// runCheckCommand runs check with args and returns what it printed.
func runCheckCommand(args ...string) (string, error) {
	var output bytes.Buffer
	err := Run(context.Background(), append([]string{"check"}, args...), nil, &output, &output)
	return output.String(), err
}

// writeManifests writes each manifest into repo, by name.
func writeManifests(t *testing.T, repo string, manifests map[string]string) {
	t.Helper()
	for name, data := range manifests {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// names lists what dir holds, for checking that nothing changed.
func names(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		found = append(found, path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestCheck(t *testing.T) {
	repo, _ := repository(t, "home/.bashrc", "home/.config/git/config")
	writeManifests(t, repo, map[string]string{
		config.FileName: `{"setup": [{"name": "tools", "run": ["true"]}]}`,
	})
	before := names(t, repo)
	output, err := runCheckCommand(repo)
	if err != nil || output != "valid\t"+repo+"\n" {
		t.Fatalf("valid repository: %q, %v", output, err)
	}
	if after := names(t, repo); !slices.Equal(before, after) {
		t.Fatalf("check changed the repository: %v, then %v", before, after)
	}

	// Every issue in both manifests is listed, one per line, and check fails.
	writeManifests(t, repo, map[string]string{
		config.FileName:         `{"setup": [{"name": "Bad Name", "run": ["true"]}, {"name": "tools", "run": ["true"]}]}`,
		config.DotfilesFileName: `{"files": [{"source": "../x", "target": "x"}], "setup": [{"name": "tools", "run": ["false"]}]}`,
	})
	output, err = runCheckCommand(repo)
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if err == nil || !strings.Contains(err.Error(), "is not valid: 3 issues") || Reported(err) || len(lines) != 3 {
		t.Fatalf("got %q, %v", output, err)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "issue\t") {
			t.Errorf("not an issue line: %q", line)
		}
	}

	// A listed file the repository lacks is an issue, though the manifests
	// are valid.
	if err := os.Remove(filepath.Join(repo, config.FileName)); err != nil {
		t.Fatal(err)
	}
	writeManifests(t, repo, map[string]string{
		config.DotfilesFileName: `{"files": [{"source": "home/.bashrc", "target": ".bashrc"}, {"source": "home/gone", "target": "gone"}]}`,
	})
	output, err = runCheckCommand(repo)
	if err == nil || output != "issue\thome/gone is listed, but the repository has no such file\n" {
		t.Fatalf("absent source: %q, %v", output, err)
	}
}

// TestCheckInputs checks that a setup step's input the repository lacks is
// an issue, as setup could not read it.
func TestCheckInputs(t *testing.T) {
	repo, _ := repository(t, "home/.bashrc", "lists/apt")
	writeManifests(t, repo, map[string]string{
		config.FileName: `{"setup": [{"name": "apt", "run": ["true"], "inputs": ["lists/apt", "lists/gone"]}]}`,
	})
	output, err := runCheckCommand(repo)
	if err == nil || output != "issue\tsetup step apt: input lists/gone is listed, but the repository has no such file\n" {
		t.Fatalf("absent input: %q, %v", output, err)
	}
}

// TestExamples checks each starter repository in examples/, so one that stops
// being valid fails here rather than for the person who copies it. Each is
// copied first, as a clone's permissions depend on how it was checked out.
func TestExamples(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			repo, _ := repository(t)
			copyTree(t, filepath.Join(root, entry.Name()), repo)
			output, err := runCheckCommand(repo)
			if err != nil || output != "valid\t"+repo+"\n" {
				t.Fatalf("%q, %v", output, err)
			}
		})
	}
}

// copyTree copies the files under from into to, which exists, with modes the
// ownership rules accept whatever the umask.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	root, err := os.OpenRoot(to)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	source := os.DirFS(from)
	err = fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == "." {
			return err
		}
		if entry.IsDir() {
			return root.Mkdir(filepath.FromSlash(path), 0o755)
		}
		data, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		file, err := root.OpenFile(filepath.FromSlash(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		return errors.Join(err, file.Close())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCheckSharedRepository checks that a repository others can change is
// one issue, not one for every file in it.
func TestCheckSharedRepository(t *testing.T) {
	repo, _ := repository(t, "home/.bashrc", "home/.profile")
	makeShared(t, repo)
	output, err := runCheckCommand(repo)
	if err == nil || strings.Count(output, "issue\t") != 1 || !strings.Contains(output, "writable by") {
		t.Fatalf("got %q, %v", output, err)
	}
}

func TestCheckView(t *testing.T) {
	repo, _ := repository(t, "home/.bashrc", "home/.profile")
	writeManifests(t, repo, map[string]string{config.FileName: `{"platforms": ["linux", "windows"], "setup": [{"name": "tools", "run": ["true"]}]}`})
	manifest, issues := checkRepository(repo)
	var output bytes.Buffer
	if err := (&view{}).check(&output, repo, manifest, issues); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repository: " + repo, "manifests:  " + config.FileName, "Valid: 2 files and 1 setup step, for linux, windows."} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("valid view lacks %q:\n%s", want, output.String())
		}
	}

	writeManifests(t, repo, map[string]string{config.DotfilesFileName: `{"setup": [{"name": "tools", "run": ["false"]}]}`})
	manifest, issues = checkRepository(repo)
	output.Reset()
	err := (&view{}).check(&output, repo, manifest, issues)
	for _, want := range []string{"manifests:  " + config.FileName + ", " + config.DotfilesFileName, "Issues (1)", "listed twice", "Not valid:"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("view of issues lacks %q:\n%s", want, output.String())
		}
	}
	if !Reported(err) {
		t.Errorf("issues in the view: %v, not reported", err)
	}
}
