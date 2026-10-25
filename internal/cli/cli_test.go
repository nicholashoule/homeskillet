//go:build linux || darwin

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// repository creates a repository holding each source, with no manifest, and
// an empty home directory. The temporary root is canonicalized because macOS
// exposes its temporary directory through a symlink.
func repository(t *testing.T, sources ...string) (repo, home string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, home = filepath.Join(root, "repo"), filepath.Join(root, "home")
	for _, directory := range []string{repo, home} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range sources {
		path := filepath.Join(repo, source)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo, home
}

// fixture creates a repository holding each source and a manifest linking it
// to the same relative path under an empty home directory.
func fixture(t *testing.T, sources ...string) (repo, home string) {
	t.Helper()
	repo, home = repository(t, sources...)
	var manifest config.Manifest
	for _, source := range sources {
		manifest.Files = append(manifest.Files, config.Entry{Source: source, Target: source})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, home
}

// invoke runs one command against repo and home. For self, repo is ignored
// and extra starts with the self command and its operand, which precede the
// options.
func invoke(command, repo, home string, extra ...string) (string, error) {
	var output bytes.Buffer
	args := append([]string{command, "--repo", repo, "--home", home}, extra...)
	if command == "self" {
		args = append(append([]string{command}, extra...), "--home", home)
	}
	err := Run(context.Background(), args, strings.NewReader(""), &output, &output)
	return output.String(), err
}

func TestLifecycle(t *testing.T) {
	file := ".config/prompt/theme.json"
	repo, home := fixture(t, file)
	source, target := filepath.Join(repo, file), filepath.Join(home, file)
	for _, command := range []string{"status", "link"} {
		output, err := invoke(command, repo, home, "--dry-run")
		if err != nil || !strings.Contains(output, "missing\t") {
			t.Fatalf("%s preview: %s, %v", command, output, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("preview created directories: %v", err)
		}
	}
	for range 2 {
		if _, err := invoke("link", repo, home); err != nil {
			t.Fatal(err)
		}
	}
	if destination, err := os.Readlink(target); err != nil || destination != source {
		t.Fatalf("unexpected link %q: %v", destination, err)
	}
	if output, err := invoke("status", repo, home); err != nil || !strings.Contains(output, "linked\t") {
		t.Fatalf("status: %s, %v", output, err)
	}
	if _, err := invoke("unlink", repo, home, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatal("unlink preview removed the link", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := invoke("unlink", repo, home); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("link still exists: %v", err)
	}
}

func TestProblemsReportedPerEntry(t *testing.T) {
	repo, home := fixture(t, "first", "second", "third")
	if err := os.WriteFile(filepath.Join(home, "second"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "third")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"status", "link"} {
		output, err := invoke(command, repo, home)
		for _, state := range []string{"missing\t", "conflict\t", "no-source\t"} {
			if err == nil || !strings.Contains(output, state) {
				t.Fatalf("%s did not report %q and fail: %s, %v", command, state, output, err)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "first")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial link occurred: %v", err)
	}
	if output, err := invoke("unlink", repo, home); err != nil || !strings.Contains(output, "skipped\t") {
		t.Fatalf("unlink was blocked: %s, %v", output, err)
	}
}

func TestCLIArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"link", "--help"}, {"link", "--allow-root", "--help"}, {"setup", "--yes", "--help"},
		{"self", "--help"}, {"self", "install", "--help"}, {"self", "use", "latest", "--help"},
	} {
		if err := Run(context.Background(), args, nil, new(bytes.Buffer), new(bytes.Buffer)); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"invalid"}, {"link", "extra"}, {"--invalid"},
		{"self"}, {"self", "upgrade"}, {"self", "use"}, {"self", "remove", "--dry-run"},
		{"self", "list", "extra"}, {"version", "extra"},
	} {
		if err := Run(context.Background(), args, nil, new(bytes.Buffer), new(bytes.Buffer)); err == nil {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
	// Each of these would otherwise succeed, so the error can only come from
	// the flag not applying to the command.
	repo, home := fixture(t, "config")
	for _, args := range [][]string{
		{"link", "--prefix", filepath.Join(home, ".local")},
		{"self", "list", "--repo", repo},
		{"self", "list", "--yes"},
		{"version", "--dry-run"},
	} {
		args = append(args, "--home", home)
		if args[0] == "link" {
			args = append(args, "--repo", repo)
		}
		err := Run(context.Background(), args, nil, new(bytes.Buffer), new(bytes.Buffer))
		if err == nil || !strings.Contains(err.Error(), "applies only to") && !strings.Contains(err.Error(), "takes no options") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if err := Run(context.Background(), []string{"--repo", repo, "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, "config")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("default command changed home")
	}
	if _, err := invoke("link", repo, home, "--yes"); err == nil {
		t.Fatal("--yes accepted outside setup")
	}
}

func TestSetup(t *testing.T) {
	repo, home := fixture(t, "config")
	manifest := config.Manifest{
		Files: []config.Entry{{Source: "config", Target: "config"}},
		Setup: []config.Step{{Name: "marker", Run: []string{"sh", "-c", `: > "$HOME/marker"`}, Creates: "marker"}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker")
	if output, err := invoke("setup", repo, home, "--dry-run"); err != nil || !strings.Contains(output, "pending\tmarker") {
		t.Fatalf("dry run: %s, %v", output, err)
	}
	if output, err := invoke("setup", repo, home); err != nil || !strings.Contains(output, "skipped\tmarker") {
		t.Fatalf("unanswered prompt did not decline: %s, %v", output, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("step ran without approval: %v", err)
	}
	var output bytes.Buffer
	err = Run(context.Background(), []string{"setup", "--repo", repo, "--home", home}, strings.NewReader("y\n"), &output, &output)
	if err != nil || !strings.Contains(output.String(), "ran\tmarker") {
		t.Fatalf("approved setup: %s, %v", output.String(), err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("approved step did not run", err)
	}
	if output, err := invoke("setup", repo, home, "--dry-run"); err != nil || !strings.Contains(output, "done\tmarker") {
		t.Fatalf("completed step not recognized: %s, %v", output, err)
	}
}

func TestWithoutManifest(t *testing.T) {
	files := []string{".bashrc", ".config/prompt/theme.json"}
	repo, home := repository(t, "home/.bashrc", "home/.config/prompt/theme.json")
	output, err := invoke("status", repo, home)
	if err != nil || strings.Count(output, "missing\t") != len(files) {
		t.Fatalf("status: %s, %v", output, err)
	}
	if _, err := invoke("link", repo, home); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		destination, err := os.Readlink(filepath.Join(home, file))
		if err != nil || destination != filepath.Join(repo, "home", file) {
			t.Fatalf("%s: unexpected link %q: %v", file, destination, err)
		}
	}
	// Setup has nothing to offer without a manifest, which is not an error.
	if output, err := invoke("setup", repo, home); err != nil || output != "" {
		t.Fatalf("setup: %q, %v", output, err)
	}
	if output, err := invoke("unlink", repo, home); err != nil || strings.Count(output, "unlinked\t") != len(files) {
		t.Fatalf("unlink: %s, %v", output, err)
	}
	// Discovered files get the same ownership checks as listed ones.
	if err := os.Chmod(filepath.Join(repo, "home", ".config"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("status", repo, home); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("shared directory under home accepted: %v", err)
	}
	// With no steps to run, setup has nothing to protect and must not fail
	// over files it never uses.
	if _, err := invoke("setup", repo, home); err != nil {
		t.Fatalf("setup without steps: %v", err)
	}
	// Neither a manifest nor a home directory most likely means the wrong
	// --repo, so it fails instead of reporting nothing to do.
	empty, _ := repository(t)
	if _, err := invoke("status", empty, home); err == nil {
		t.Fatal("repository with nothing to manage accepted")
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var output bytes.Buffer
		if err := Run(context.Background(), args, nil, &output, new(bytes.Buffer)); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(output.String(), "homeskillet ") || !strings.Contains(output.String(), "go:") {
			t.Fatalf("%v: %q", args, output.String())
		}
	}
}

func TestSelf(t *testing.T) {
	_, home := repository(t)
	prefix := filepath.Join(home, ".local")
	if output, err := invoke("self", "", home, "list"); err != nil || !strings.Contains(output, "no versions installed") {
		t.Fatalf("list: %q, %v", output, err)
	}
	if _, err := invoke("self", "", home, "use", "latest"); err == nil {
		t.Fatal("use succeeded with nothing installed")
	}
	// A test binary records no version, and an unversioned build names no
	// particular build, so it cannot be installed.
	if _, err := invoke("self", "", home, "install"); err == nil || !strings.Contains(err.Error(), "cannot install version") {
		t.Fatalf("installed an unversioned build: %v", err)
	}
	if _, err := os.Lstat(prefix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed install wrote %s: %v", prefix, err)
	}
}
