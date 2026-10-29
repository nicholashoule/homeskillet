//go:build linux || darwin || windows

// Tests of the command line as a whole, through Run: parsing, linking and
// unlinking end to end, setup, the default repository, the data directory,
// empty values, and the view, each in temporary repositories and homes.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/setup"
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
	// t.TempDir leaves the mode to the umask; the ownership rules need the
	// ancestors of a repository or home not writable by others.
	if err := os.Chmod(root, 0o755); err != nil {
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
	requireSymlinks(t)
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
		{"invalid"}, {"--invalid"},
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

func TestRepositoryArgument(t *testing.T) {
	requireSymlinks(t)
	for _, test := range []struct {
		args    []string
		command string
		repo    string
		files   []string
		operand string
		dryRun  bool
	}{
		{[]string{"/r"}, "status", "/r", nil, "", false},
		{[]string{"."}, "status", ".", nil, "", false},
		{[]string{"../r", "--dry-run"}, "status", "../r", nil, "", true},
		{[]string{"link", "r"}, "link", "r", nil, "", false},
		{[]string{"link", "r", "--dry-run"}, "link", "r", nil, "", true},
		{[]string{"unlink", "--dry-run", "r"}, "unlink", "r", nil, "", true},
		{[]string{"setup", "--", "./-r"}, "setup", "./-r", nil, "", false},
		{[]string{"link", "--repo", "r"}, "link", "r", nil, "", false},
		{[]string{"link"}, "link", ".", nil, "", false},
		{[]string{"self", "use", "--dry-run", "latest"}, "self", ".", nil, "latest", true},
		{[]string{"self", "--dry-run", "list"}, "self", ".", nil, "", true},
		// Names after the repository select files; with --repo, every
		// operand is a file.
		{[]string{"link", "r", ".bashrc", "--dry-run", ".vimrc"}, "link", "r", []string{".bashrc", ".vimrc"}, "", true},
		{[]string{"link", ".bashrc", "--repo", "r"}, "link", "r", []string{".bashrc"}, "", false},
		{[]string{"/r", ".bashrc"}, "status", "/r", []string{".bashrc"}, "", false},
		// After the terminator, --dry-run is a file name, so selecting
		// fails before anything changes rather than linking for real.
		{[]string{"link", "--", "r", "--dry-run"}, "link", "r", []string{"--dry-run"}, "", false},
	} {
		inv, err := parse(test.args, new(bytes.Buffer))
		if err != nil {
			t.Fatalf("%v: %v", test.args, err)
		}
		if inv.command != test.command || inv.repo != test.repo || !slices.Equal(inv.files, test.files) ||
			inv.operand != test.operand || inv.dryRun != test.dryRun {
			t.Fatalf("%v: got %+v", test.args, inv)
		}
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		// A bare word is a command, never a directory that happens to
		// exist, so a typo cannot select a repository.
		{[]string{"dotfiles"}, "is not a command"},
		{[]string{"version", "/r"}, "unexpected"},
		{[]string{"self", "list", "/r"}, "unexpected"},
		{[]string{"self", "use", "v0.0.1", "v0.0.2"}, "unexpected"},
		{[]string{"self", "--dry-run", "upgrade"}, "unknown command self upgrade"},
		{[]string{"status", "r", "--force"}, "applies only to link"},
	} {
		if _, err := parse(test.args, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%v: got %v, want an error containing %q", test.args, err, test.want)
		}
	}

	repo, home := fixture(t, "config")
	run := func(args ...string) string {
		t.Helper()
		var output bytes.Buffer
		if err := Run(context.Background(), append(args, "--home", home), nil, &output, &output); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output.String())
		}
		return output.String()
	}
	link := filepath.Join(home, "config")
	run(repo)
	run("link", repo, "--dry-run")
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("status or a dry run changed home")
	}
	run("link", repo)
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(repo, "config") {
		t.Fatalf("link: %q, %v", target, err)
	}
	if output := run(repo); !strings.HasPrefix(output, "linked\t") {
		t.Fatalf("status: %s", output)
	}
	run("unlink", repo)
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unlink left %s: %v", link, err)
	}
}

func TestSetup(t *testing.T) {
	repo, home := fixture(t, "config")
	manifest := config.Manifest{
		Files: []config.Entry{{Source: "config", Target: "config"}},
		Setup: []config.Step{{Name: "marker", Run: config.Commands{touchCommand("marker")}, Creates: "marker"}},
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

// TestSetupInputs checks that setup records a step with inputs that ran, in
// the data directory --data-dir names, so it is done until an input changes,
// and that naming the step runs it again even so.
func TestSetupInputs(t *testing.T) {
	repo, home := fixture(t, "packages.txt")
	manifest := config.Manifest{Setup: []config.Step{{Name: "packages", Run: config.Commands{touchCommand("ran")}, Inputs: []string{"packages.txt"}}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, "data")
	ran := filepath.Join(home, "ran")
	run := func(args ...string) string {
		t.Helper()
		var output bytes.Buffer
		err := Run(context.Background(), append([]string{"setup", "--repo", repo, "--home", home, "--data-dir", dataDir, "--yes"}, args...), nil, &output, &output)
		if err != nil {
			t.Fatalf("setup %q: %s, %v", args, output.String(), err)
		}
		return output.String()
	}
	if output := run(); !strings.Contains(output, "ran\tpackages") {
		t.Fatalf("first run: %s", output)
	}
	if err := os.Remove(ran); err != nil {
		t.Fatal(err)
	}
	if output := run(); !strings.Contains(output, "done\tpackages\tinputs unchanged since it ran ") {
		t.Fatalf("second run: %s", output)
	}
	if output := run("packages"); !strings.Contains(output, "ran\tpackages") {
		t.Fatalf("named: %s", output)
	}
	if err := os.WriteFile(filepath.Join(repo, "packages.txt"), []byte("jq\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output := run("--dry-run"); !strings.Contains(output, "pending\tpackages") {
		t.Fatalf("after the list changed: %s", output)
	}
}

// TestSetupSteps checks that names after setup select steps, in the
// manifest's order, with the repository before them written as a path, and
// that a name matching no step for this platform fails before anything runs.
func TestSetupSteps(t *testing.T) {
	for _, test := range []struct {
		args  []string
		repo  string
		steps []string
	}{
		{[]string{"setup", "dotnet"}, ".", []string{"dotnet"}},
		{[]string{"setup", "./r", "dotnet", "fonts"}, "./r", []string{"dotnet", "fonts"}},
		{[]string{"setup", "/r"}, "/r", nil},
		{[]string{"setup", "dotnet", "--repo", "r"}, "r", []string{"dotnet"}},
	} {
		inv, err := parse(test.args, new(bytes.Buffer))
		if err != nil || inv.repo != test.repo || !slices.Equal(inv.steps, test.steps) {
			t.Fatalf("%v: got repo %q, steps %q, %v", test.args, inv.repo, inv.steps, err)
		}
	}

	repo, home := repository(t)
	touch := func(name string, platforms ...string) config.Step {
		return config.Step{Name: name, Run: config.Commands{touchCommand(name)}, Platforms: platforms}
	}
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	data, err := json.Marshal(config.Manifest{Setup: []config.Step{touch("first"), touch("second"), touch("third"), touch("elsewhere", other)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Named out of order, the steps still run in the manifest's order.
	output, err := invoke("setup", repo, home, "third", "first", "--yes")
	if err != nil || strings.Index(output, "ran\tfirst") > strings.Index(output, "ran\tthird") ||
		!exists(filepath.Join(home, "first")) || !exists(filepath.Join(home, "third")) || exists(filepath.Join(home, "second")) {
		t.Fatalf("named steps: %q, %v", output, err)
	}
	for name, want := range map[string]string{"typo": "has no setup step typo", "elsewhere": "is not for"} {
		if _, err := invoke("setup", repo, home, "second", name, "--yes"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if exists(filepath.Join(home, "second")) {
		t.Fatal("a step ran although another name was wrong")
	}
}

func TestWithoutManifest(t *testing.T) {
	requireSymlinks(t)
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
	makeShared(t, filepath.Join(repo, "home", ".config"))
	if _, err := invoke("status", repo, home); err == nil || !strings.Contains(err.Error(), "is writable by") {
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
		// Built for and run on the same platform, the two are one line.
		if !strings.HasPrefix(output.String(), "homeskillet ") || !strings.Contains(output.String(), "go:") ||
			!strings.Contains(output.String(), "platform:  "+runtime.GOOS+"/") || strings.Contains(output.String(), "host:") {
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

func TestRefusesSystemLocations(t *testing.T) {
	requireUnix(t)
	repo, home := fixture(t, "config")
	// The CLI checks home before any command runs, so even a command with
	// nothing to do, such as setup without steps, fails clearly.
	for _, command := range []string{"status", "setup"} {
		err := Run(context.Background(), []string{command, "--repo", repo, "--home", "/"}, nil, nil, nil)
		if err == nil || !strings.HasPrefix(err.Error(), "home: refusing to act on") {
			t.Errorf("%s with home /: %v", command, err)
		}
	}
	for _, args := range [][]string{
		{"status", "--repo", repo, "--home", "/"},
		{"link", "--repo", repo, "--home", "/etc"},
		{"status", "--repo", "/usr", "--home", home},
		{"self", "list", "--home", home, "--prefix", "/usr/local"},
		{"self", "install", "--home", home, "--prefix", "/"},
	} {
		err := Run(context.Background(), args, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "refusing to act on") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// TestEmptyValues checks that an empty option value or argument, as from an
// unset shell variable, is refused rather than read as the default: the real
// home for --home "$SCRATCH", every file for unlink repo "$FILE".
func TestEmptyValues(t *testing.T) {
	for _, args := range [][]string{
		{"link", "--home", ""}, {"link", "--home="}, {"status", "--repo", ""}, {"status", "--data-dir", ""},
		{"status", "--platform", ""}, {"self", "install", "--prefix", ""},
		{"link", ""}, {"unlink", ".", ""}, {"unlink", "--repo", ".", ""}, {"self", "use", ""}, {"install", ""}, {"update", ""},
	} {
		if _, err := parse(args, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf("%q: %v", args, err)
		}
	}
}

func TestNilStreams(t *testing.T) {
	requireSymlinks(t)
	repo, home := fixture(t, "config")
	//nolint:staticcheck // SA1012: a nil context is what this test checks
	if err := Run(nil, []string{"link", "--repo", repo, "--home", home}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"--help"}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"version"}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// TestMain clears the data directory setting, so tests never write records
// into the data directory of whoever runs them.
func TestMain(m *testing.M) {
	if err := os.Unsetenv(dataDirVariable); err != nil {
		panic(err)
	}
	// GitHub's Windows runners run tests elevated, the counterpart of root,
	// which every command refuses. The rules under test are the same either
	// way, and TestRefusesRoot covers the refusal itself.
	privileged = func() bool { return false }
	// The test binary stands in for homeskillet when a test runs a shell
	// script that calls it (see TestBashCompletion).
	if os.Getenv(asHomeskillet) == "1" {
		if err := Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "homeskillet:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOrphanedLinks(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/kept", "home/deleted")
	if _, err := invoke("link", repo, home); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(home, ".local", "share", "homeskillet", "links.json")
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("no record after link: %v", err)
	}
	// Deleting a file from home/ leaves its link behind; status finds it
	// without failing, and link removes it.
	if err := os.Remove(filepath.Join(repo, "home", "deleted")); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(home, "deleted")
	output, err := invoke("status", repo, home)
	if err != nil || !strings.Contains(output, "orphaned\t"+orphan) {
		t.Fatalf("status: %q, %v", output, err)
	}
	if output, err := invoke("link", repo, home, "--dry-run"); err != nil || !strings.Contains(output, "orphaned\t") {
		t.Fatalf("link --dry-run: %q, %v", output, err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("dry run removed the orphan: %v", err)
	}
	if output, err := invoke("link", repo, home); err != nil || !strings.Contains(output, "unlinked\t"+orphan) {
		t.Fatalf("link: %q, %v", output, err)
	}
	if _, err := os.Lstat(orphan); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan remains: %v", err)
	}
	if _, err := invoke("unlink", repo, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("record remains after unlinking everything: %v", err)
	}
}

func TestDataDir(t *testing.T) {
	requireUnix(t)
	_, home := repository(t)
	elsewhere := filepath.Join(filepath.Dir(home), "elsewhere")
	for name, test := range map[string]struct {
		inv  invocation
		env  string
		want string
	}{
		"default":          {invocation{command: "link"}, "", filepath.Join(home, ".local", "share", "homeskillet")},
		"tilde flag":       {invocation{command: "link", data: "~/.config/homeskillet"}, "", filepath.Join(home, ".config", "homeskillet")},
		"relative flag":    {invocation{command: "link", data: ".config/homeskillet"}, "", filepath.Join(home, ".config", "homeskillet")},
		"absolute flag":    {invocation{command: "link", data: elsewhere}, "", elsewhere},
		"tilde variable":   {invocation{command: "status"}, "~/.config/homeskillet", filepath.Join(home, ".config", "homeskillet")},
		"flag over env":    {invocation{command: "link", data: "~/flag"}, "~/env", filepath.Join(home, "flag")},
		"prefix":           {invocation{command: "self", prefix: elsewhere}, "~/env", filepath.Join(elsewhere, "share", "homeskillet")},
		"flag over prefix": {invocation{command: "self", prefix: elsewhere, data: "~/flag"}, "", filepath.Join(home, "flag")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(dataDirVariable, test.env)
			if got, err := dataDir(test.inv, home); err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
	for _, value := range []string{"/etc/homeskillet", "/usr/local/share/homeskillet", "/"} {
		if got, err := dataDir(invocation{command: "link", data: value}, home); err == nil {
			t.Errorf("%s accepted as %s", value, got)
		}
	}
}

func TestDataDirOption(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/file")
	if _, err := invoke("link", repo, home, "--data-dir", "~/.config/homeskillet"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "homeskillet", "links.json")); err != nil {
		t.Fatalf("record not in the chosen data directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "homeskillet")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("default data directory used anyway: %v", err)
	}
	// A system directory: /etc, or on Windows, where /etc is no absolute path,
	// the Windows directory.
	system := "/etc/homeskillet"
	if runtime.GOOS == "windows" {
		system = filepath.Join(os.Getenv("SystemRoot"), "homeskillet")
	}
	for _, args := range [][]string{
		{"link", "--repo", repo, "--home", home, "--data-dir", system},
		{"link", "--repo", repo, "--home", home, "--data-dir", filepath.Join(repo, "data")},
		// setup keeps the record of steps with inputs there, under the same rules.
		{"setup", "--repo", repo, "--home", home, "--data-dir", system},
	} {
		if err := Run(context.Background(), args, nil, nil, nil); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestPlatforms(t *testing.T) {
	requireSymlinks(t)
	other := map[string]string{"linux": "darwin", "darwin": "linux", "windows": "linux"}[runtime.GOOS]
	repo, home := repository(t, "home/.zshrc")
	manifest := `{"platforms": ["` + other + `"], "setup": [{"name": "marker", "run": ["sh", "-c", ": > \"$HOME/ran\""]}]}`
	if err := os.WriteFile(filepath.Join(repo, config.FileName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nothing applies to the detected platform, which is an error rather than
	// a silent success, except for unlink, which must always clean up.
	for _, command := range []string{"status", "link", "setup"} {
		if _, err := invoke(command, repo, home); err == nil || !strings.Contains(err.Error(), "applies to") {
			t.Errorf("%s on %s: %v", command, runtime.GOOS, err)
		}
	}
	if _, err := invoke("unlink", repo, home); err != nil {
		t.Errorf("unlink: %v", err)
	}
	// --platform acts for the other platform: links in a scratch home, and
	// lists, but never runs, its setup steps.
	output, err := invoke("link", repo, home, "--platform", other)
	if err != nil || !strings.Contains(output, "linked\t") {
		t.Fatalf("link --platform %s: %q, %v", other, output, err)
	}
	// Scripts read output and errors together and parse every line, so an
	// override adds no lines of its own.
	for line := range strings.Lines(output) {
		if !strings.HasPrefix(line, "linked\t") {
			t.Fatalf("unexpected line %q", line)
		}
	}
	if _, err := invoke("setup", repo, home, "--platform", other, "--yes"); err == nil || !strings.Contains(err.Error(), "--dry-run") {
		t.Fatalf("ran another OS's setup steps: %v", err)
	}
	if output, err := invoke("setup", repo, home, "--platform", other, "--dry-run"); err != nil || !strings.Contains(output, "pending\tmarker") {
		t.Fatalf("setup --dry-run --platform %s: %q, %v", other, output, err)
	}
	if _, err := os.Stat(filepath.Join(home, "ran")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a step for another OS ran")
	}
	for _, bad := range []string{"macos", "darwin/arm", "win"} {
		if _, err := invoke("status", repo, home, "--platform", bad); err == nil {
			t.Errorf("--platform %s accepted", bad)
		}
	}
	if _, err := invoke("self", "", home, "list", "--platform", other); err == nil {
		t.Error("--platform accepted by self")
	}
}

func TestSelectFiles(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc", "home/.vimrc", "home/.config/app/a", "home/.config/app/b")
	linked := func(target string) bool {
		_, err := os.Readlink(filepath.Join(home, target))
		return err == nil
	}
	// A typo selects nothing, so the command fails and changes nothing, even
	// alongside a name that matches.
	if _, err := invoke("link", repo, home, ".bashrc", ".bashrx"); err == nil || !strings.Contains(err.Error(), `".bashrx"`) {
		t.Fatalf("unmatched name: %v", err)
	}
	if linked(".bashrc") {
		t.Fatal("a failed selection linked a file")
	}
	// A target, a source, and a directory each select their files.
	for _, name := range []string{".bashrc", filepath.Join(home, ".vimrc"), "home/.config/app"} {
		if _, err := invoke("link", repo, home, name); err != nil {
			t.Fatalf("link %s: %v", name, err)
		}
	}
	for _, target := range []string{".bashrc", ".vimrc", ".config/app/a", ".config/app/b"} {
		if !linked(target) {
			t.Fatalf("%s not linked", target)
		}
	}
	// Unlinking one file keeps the others, and the record still knows them:
	// deleting one from the repository later makes it orphaned.
	if _, err := invoke("unlink", repo, home, ".bashrc"); err != nil {
		t.Fatal(err)
	}
	if linked(".bashrc") || !linked(".vimrc") {
		t.Fatal("unlink of one file touched another")
	}
	if err := os.Remove(filepath.Join(repo, "home", ".vimrc")); err != nil {
		t.Fatal(err)
	}
	if output, err := invoke("status", repo, home); err != nil || !strings.Contains(output, "orphaned\t"+filepath.Join(home, ".vimrc")) {
		t.Fatalf("record lost a link outside the selection: %q, %v", output, err)
	}
	// After the terminator, --dry-run is a name; it matches nothing, so the
	// command fails instead of linking for real.
	if err := Run(context.Background(), []string{"link", "--home", home, "--", repo, "--dry-run"}, nil, new(bytes.Buffer), new(bytes.Buffer)); err == nil || linked(".bashrc") {
		t.Fatalf("terminator: %v", err)
	}
}

func TestForce(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc", "home/.vimrc")
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Scripts get no view, so the error is their explanation and is printed.
	if _, err := invoke("link", repo, home); !errors.Is(err, link.ErrBlocked) || Reported(err) {
		t.Fatalf("conflict did not block link: %v", err)
	}
	if _, err := invoke("link", repo, home, "--force", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(bashrc); err != nil || string(data) != "mine\n" {
		t.Fatalf("dry run touched the conflict: %q, %v", data, err)
	}
	output, err := invoke("link", repo, home, ".bashrc", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(bashrc); err != nil || target != filepath.Join(repo, "home", ".bashrc") {
		t.Fatalf("not linked: %q, %v", target, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".vimrc")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a file outside the selection was linked")
	}
	// The replaced file is in the backup, named with the Unix time, intact.
	backups, err := filepath.Glob(filepath.Join(home, ".local", "share", "homeskillet", link.BackupDir, ".bashrc.*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups %v, %v", backups, err)
	}
	if stamp := strings.TrimPrefix(filepath.Base(backups[0]), ".bashrc."); strings.Trim(stamp, "0123456789") != "" {
		t.Fatalf("backup %s is not named with a Unix time", backups[0])
	}
	if data, err := os.ReadFile(backups[0]); err != nil || string(data) != "mine\n" {
		t.Fatalf("backup contents %q, %v", data, err)
	}
	if !strings.Contains(output, "moved\t"+bashrc+" -> "+backups[0]) {
		t.Fatalf("move not reported: %q", output)
	}
	// A second forced replacement in the same second keeps both backups.
	for range 2 {
		if err := os.Remove(bashrc); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bashrc, []byte("again\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := invoke("link", repo, home, ".bashrc", "--force"); err != nil {
			t.Fatal(err)
		}
	}
	if backups, err := filepath.Glob(filepath.Join(home, ".local", "share", "homeskillet", link.BackupDir, ".bashrc.*")); err != nil || len(backups) != 3 {
		t.Fatalf("backups overwrote each other: %v, %v", backups, err)
	}
}

func TestView(t *testing.T) {
	requireSymlinks(t)
	if newView(new(bytes.Buffer)) != nil {
		t.Fatal("a buffer is not a terminal")
	}
	// Nor is /dev/null, though it is a character device: output sent there
	// must keep its errors on standard error.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	nullView := newView(null)
	if err := null.Close(); err != nil {
		t.Fatal(err)
	}
	if nullView != nil {
		t.Fatal("/dev/null is not a terminal")
	}
	repo, home := repository(t, "home/.bashrc", "home/.vimrc")
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	v := &view{}
	render := func(args ...string) (string, error) {
		inv, err := parse(append(args, "--home", home), new(bytes.Buffer))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := config.Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err = runLinks(inv, manifest.Files, repo, home, filepath.Join(home, ".local", "share", "homeskillet"), &output, new(bytes.Buffer), v)
		return output.String(), err
	}
	output, err := render("link", repo)
	for _, want := range []string{"Conflicts (1): not homeskillet's links", filepath.Join(home, ".bashrc") + "  a file", "Would link (1)\n  " + filepath.Join(home, ".vimrc"), "Nothing changed.", "--force"} {
		if !strings.Contains(output, want) {
			t.Errorf("blocked link output lacks %q:\n%s", want, output)
		}
	}
	// The output explained the failure, so main prints no error line too.
	if !errors.Is(err, link.ErrBlocked) || !Reported(err) {
		t.Errorf("blocked link error: %v", err)
	}
	if strings.Contains(output, "\t") {
		t.Errorf("person-facing output has tab-separated lines:\n%s", output)
	}
	output, err = render("link", repo, "--force")
	for _, want := range []string{"Replaced (1): moved into the backup, then linked", "backup: " + filepath.Join(home, ".local", "share", "homeskillet", link.BackupDir, ".bashrc."), "Linked (1)\n  " + filepath.Join(home, ".vimrc"), "Done: 1 linked and 1 replaced (originals in the backup)."} {
		if err != nil || !strings.Contains(output, want) {
			t.Errorf("forced link output lacks %q (%v):\n%s", want, err, output)
		}
	}
	if output, err = render("status", repo); err != nil || !strings.Contains(output, "Linked (2)") || !strings.Contains(output, "Everything is linked: 2 files.") {
		t.Errorf("status output (%v):\n%s", err, output)
	}
	// With nothing to change, the files already linked are still listed,
	// and the closing line says why nothing changed.
	linked := "Already linked (2)\n  " + filepath.Join(home, ".bashrc") + "\n  " + filepath.Join(home, ".vimrc") + "\n\n"
	for args, want := range map[string]string{
		"link --dry-run":   linked + "Dry run: nothing would change; 2 files are already linked.",
		"link":             linked + "Nothing to change: 2 files are already linked.",
		"unlink --dry-run": "Dry run: nothing changed; unlink would remove 2.",
	} {
		fields := strings.Fields(args)
		if output, err = render(append([]string{fields[0], repo}, fields[1:]...)...); err != nil || !strings.HasSuffix(output, want+"\n") {
			t.Errorf("%s output (%v):\n%s", args, err, output)
		}
	}
}

// failAfter accepts n writes, then fails every one, like a closed pipe.
type failAfter struct{ n int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n == 0 {
		return 0, errors.New("broken pipe")
	}
	f.n--
	return len(p), nil
}

func TestForceStoppedPartway(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/a", "home/b")
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("mine "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Both moves are reported; reporting the first new link fails, after
	// that link was made and before b was linked.
	output := &failAfter{n: 2}
	err := Run(context.Background(), []string{"link", repo, "--home", home, "--force"}, nil, output, new(bytes.Buffer))
	if err == nil {
		t.Fatal("link succeeded although its output failed")
	}
	if target, err := os.Readlink(filepath.Join(home, "a")); err != nil || target != filepath.Join(repo, "home", "a") {
		t.Fatalf("a: %q, %v", target, err)
	}
	// b was moved aside but never linked, so it is back where it was.
	if data, err := os.ReadFile(filepath.Join(home, "b")); err != nil || string(data) != "mine b" {
		t.Fatalf("b not restored: %q, %v", data, err)
	}
	// The link made before the failure is recorded, and b, back to the
	// user's file, is not.
	contents, err := os.ReadFile(filepath.Join(home, ".local", "share", "homeskillet", "links.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"target": "a"`) || strings.Contains(string(contents), `"target": "b"`) {
		t.Fatalf("record after a partial run: %s", contents)
	}
}

func TestSelectionIgnoresOtherEntries(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc", "home/.config/app/conf")
	// A shared directory where another entry would go blocks that entry,
	// not the one named.
	shared := filepath.Join(home, ".config")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	makeShared(t, shared)
	if _, err := invoke("link", repo, home); err == nil {
		t.Fatal("linking into a shared directory succeeded")
	}
	if _, err := invoke("link", repo, home, ".bashrc"); err != nil {
		t.Fatalf("an unrelated entry blocked the named one: %v", err)
	}
	// A name given through a symlink to home still matches, as /tmp does on
	// macOS.
	alias := filepath.Join(filepath.Dir(home), "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	if output, err := invoke("status", repo, home, filepath.Join(alias, ".bashrc")); err != nil || !strings.HasPrefix(output, "linked\t") {
		t.Fatalf("name through a symlink: %q, %v", output, err)
	}
}

func TestDefaultRepository(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc")
	elsewhere := t.TempDir()
	// From a directory that is not a repository, naming none, with nothing
	// linked yet: the error says how to name one.
	t.Chdir(elsewhere)
	err := Run(context.Background(), []string{"link", "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer))
	if !errors.Is(err, config.ErrNothingToManage) || !strings.Contains(err.Error(), "name one: homeskillet link") {
		t.Fatalf("nothing recorded: %v", err)
	}
	err = Run(context.Background(), []string{"link", home, "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer))
	if !errors.Is(err, config.ErrNothingToManage) || !strings.Contains(err.Error(), "that is the home directory; name a repository instead") {
		t.Fatalf("home named as the repository, nothing recorded: %v", err)
	}
	// Once linked, the one repository linked into home is used.
	if _, err := invoke("link", repo, home); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"status", "--home", home}, nil, &output, new(bytes.Buffer)); err != nil ||
		!strings.Contains(output.String(), "linked\t"+filepath.Join(home, ".bashrc")) {
		t.Fatalf("remembered repository: %q, %v", output.String(), err)
	}
	// From a directory homeskillet refuses to act on, such as the root, or
	// for homeskillet.exe run from a WSL shell the distribution's files, the
	// remembered repository is used too. Named, the directory is refused.
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(os.Getenv("SystemRoot")) + `\`
	}
	t.Chdir(root)
	output.Reset()
	if err := Run(context.Background(), []string{"status", "--home", home}, nil, &output, new(bytes.Buffer)); err != nil ||
		!strings.Contains(output.String(), "linked\t"+filepath.Join(home, ".bashrc")) {
		t.Fatalf("remembered repository from %s: %q, %v", root, output.String(), err)
	}
	err = Run(context.Background(), []string{"status", ".", "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer))
	if err == nil || !strings.Contains(err.Error(), "refusing to act on") {
		t.Fatalf("%s named as the repository: %v", root, err)
	}
	t.Chdir(elsewhere)
	// Named, the current directory is used even when it is not a
	// repository, and the error has no hint.
	err = Run(context.Background(), []string{"link", ".", "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer))
	if !errors.Is(err, config.ErrNothingToManage) || strings.Contains(err.Error(), "name") {
		t.Fatalf("named repository: %v", err)
	}
	// Naming the home itself is still refused, but the error names the
	// repository linked into it.
	err = Run(context.Background(), []string{"unlink", home, "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer))
	if !errors.Is(err, config.ErrNothingToManage) || !strings.Contains(err.Error(), "that is the home directory") ||
		!strings.Contains(err.Error(), "homeskillet unlink "+repo) {
		t.Fatalf("home named as the repository: %v", err)
	}
	// With two repositories linked into home, neither is chosen.
	other := filepath.Join(filepath.Dir(repo), "other")
	if err := os.MkdirAll(filepath.Join(other, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "home", ".vimrc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("link", other, home); err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), []string{"link", "--home", home}, nil, new(bytes.Buffer), new(bytes.Buffer))
	if err == nil || !strings.Contains(err.Error(), "homeskillet link "+repo) || !strings.Contains(err.Error(), "homeskillet link "+other) {
		t.Fatalf("several recorded: %v", err)
	}
}

// TestUnlinkChecksDataDirectoryFirst checks that unlink refuses a data
// directory it could not save the record to before removing any link, so the
// failure leaves every link in place.
func TestUnlinkChecksDataDirectoryFirst(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc")
	if _, err := invoke("link", repo, home); err != nil {
		t.Fatal(err)
	}
	makeShared(t, filepath.Join(home, ".local", "share", "homeskillet"))
	if _, err := invoke("unlink", repo, home); err == nil || !strings.Contains(err.Error(), "is writable by") {
		t.Fatalf("unlink with a shared data directory: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(home, ".bashrc")); err != nil {
		t.Fatalf("unlink removed a link before failing: %v", err)
	}
}

func TestQuiet(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc")
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"link", repo, "--home", home, "--quiet"}, nil, &output, new(bytes.Buffer)); err != nil || output.Len() != 0 {
		t.Fatalf("quiet link: %q, %v", output.String(), err)
	}
	if _, err := os.Readlink(filepath.Join(home, ".bashrc")); err != nil {
		t.Fatalf("quiet link did not link: %v", err)
	}
	// A failure still fails, with its error, and still prints nothing.
	if err := os.WriteFile(filepath.Join(repo, "home", ".vimrc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".vimrc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), []string{"status", repo, "--home", home, "--quiet"}, nil, &output, new(bytes.Buffer))
	if !errors.Is(err, link.ErrBlocked) || Reported(err) || output.Len() != 0 {
		t.Fatalf("quiet status with a conflict: %q, %v", output.String(), err)
	}
	if _, err := parse([]string{"setup", repo, "--quiet"}, new(bytes.Buffer)); err == nil {
		t.Fatal("setup accepted --quiet, which would hide steps before approval")
	}
}

// TestSetupView checks the dry run's grouped listing for a terminal: done
// steps with what makes them done, pending ones with their commands, aligned,
// and a closing line that says what setup would do.
func TestSetupView(t *testing.T) {
	repo, home := repository(t)
	manifest := config.Manifest{Setup: []config.Step{
		{Name: "git", Run: config.Commands{{"true"}}, Creates: ".gitconfig"},
		{Name: "tools", Run: config.Commands{{"brew", "install", "jq"}, {"brew", "install", "rg"}}},
	}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := setup.Prepare(manifest.Setup, repo, home, "")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := (&view{}).steps(&output, plans, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Pending (1)", "  tools  brew install jq; brew install rg\n",
		"Done (1)", "  git  " + filepath.Join(home, ".gitconfig") + "\n",
		"Dry run: nothing changed; setup would offer 1 step.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("setup view lacks %q:\n%s", want, output.String())
		}
	}
	// A real run at a terminal lists the done steps first, then shows each
	// pending one before asking, without tab-separated lines, and adds no
	// line after a step that ran: its own output says what it did.
	var run bytes.Buffer
	manifest.Setup[1].Run = config.Commands{{"true"}}
	err = runSetup(context.Background(), manifest.Setup, repo, home, false, setup.Options{
		Repo: repo, Home: home, Input: strings.NewReader("y\n"), Output: &run, Errors: io.Discard,
	}, &view{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Done (1)\n  git  " + filepath.Join(home, ".gitconfig"), "pending  tools\n", "Run this step? [y/N] "} {
		if !strings.Contains(run.String(), want) {
			t.Errorf("terminal run lacks %q:\n%s", want, run.String())
		}
	}
	if strings.Contains(run.String(), "ran ") {
		t.Errorf("terminal run says a step ran:\n%s", run.String())
	}
	if strings.Contains(run.String(), "done\t") || strings.Contains(run.String(), "ran\t") {
		t.Errorf("terminal run has tab-separated lines:\n%s", run.String())
	}
	// Scripts get one line per step.
	var lines bytes.Buffer
	for i := range plans {
		if err := setup.List(&lines, plans[i]); err != nil {
			t.Fatal(err)
		}
	}
	if lines.String() != "done\tgit\t"+filepath.Join(home, ".gitconfig")+"\npending\ttools\tbrew install jq; brew install rg\n" {
		t.Errorf("script lines:\n%s", lines.String())
	}
}

func TestRefusesRoot(t *testing.T) {
	repo, home := repository(t, "home/config")
	defer func(previous func() bool) { privileged = previous }(privileged)
	privileged = func() bool { return true }
	for _, args := range [][]string{
		{"status", "--repo", repo, "--home", home},
		{"setup", "--repo", repo, "--home", home, "--dry-run"},
		{"self", "list", "--home", home},
		{"init", repo},
	} {
		if err := Run(context.Background(), args, nil, nil, nil); err == nil || err.Error() != rootRefusal {
			t.Errorf("%v as root: %v", args, err)
		}
	}
	// What only prints, and reads nothing, runs.
	for _, args := range [][]string{{"version"}, {"help"}, {"completion", "bash"}} {
		if err := Run(context.Background(), args, nil, nil, nil); err != nil {
			t.Errorf("%v as root: %v", args, err)
		}
	}
	// Completion reads nothing for root without --allow-root either.
	if _, ok := completionHome(invocation{home: home}); ok {
		t.Error("completion read the home as root")
	}
	if err := Run(context.Background(), []string{"status", "--repo", repo, "--home", home, "--allow-root"}, nil, nil, nil); err != nil {
		t.Errorf("status --allow-root: %v", err)
	}
}

// TestNextSetup checks that link and status at a terminal end with the setup
// steps still pending, and say nothing once every step is done.
func TestNextSetup(t *testing.T) {
	repo, home := repository(t)
	steps := []config.Step{
		{Name: "profile", Run: config.Commands{touchCommand("profile")}, Creates: "profile"},
		{Name: "done", Run: config.Commands{touchCommand("done")}, Creates: "done"},
	}
	if err := os.WriteFile(filepath.Join(home, "done"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config.Manifest{Setup: steps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	if next := nextSetup(steps, repo, home, "", false); next != "1 setup step is pending. Run homeskillet setup to review and approve it." {
		t.Fatalf("from the repository: %q", next)
	}
	t.Chdir(home)
	if next := nextSetup(steps, repo, home, "", false); !strings.Contains(next, "Run homeskillet setup "+setup.Quote([]string{repo})+" to review") {
		t.Fatalf("from elsewhere, the repository is not named: %q", next)
	}
	// setup falls back to the record as link did.
	if next := nextSetup(steps, repo, home, "", true); !strings.Contains(next, "Run homeskillet setup to review") {
		t.Fatalf("from the record, the repository is named: %q", next)
	}
	if err := os.WriteFile(filepath.Join(home, "profile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if next := nextSetup(steps, repo, home, "", false); next != "" {
		t.Fatalf("with every step done: %q", next)
	}
	// The view prints it after the closing line, and not when linking is
	// blocked, which has a next step of its own.
	v := &view{next: "Next: run setup."}
	var output bytes.Buffer
	if err := v.links(&output, "status", nil, false, false, nil); err != nil || !strings.HasSuffix(output.String(), "Next: run setup.\n") {
		t.Fatalf("status output lacks the next step: %q, %v", output.String(), err)
	}
	output.Reset()
	blocked := []link.Plan{{Source: filepath.Join(repo, "a"), Target: filepath.Join(home, "a"), State: link.Conflict}}
	if err := v.links(&output, "link", blocked, false, false, nil); err != nil || strings.Contains(output.String(), "Next: run setup.") {
		t.Fatalf("blocked link names the setup step: %q, %v", output.String(), err)
	}
}

func TestUnknownCommand(t *testing.T) {
	for word, want := range map[string]string{
		"lnk":    "'lnk' is not a command; did you mean 'link'?",
		"stauts": "'stauts' is not a command; did you mean 'status'?",
		"Unlnk":  "'Unlnk' is not a command; did you mean 'unlink'?",
		"udpate": "'udpate' is not a command; did you mean 'update'?",
		"lst":    "'lst' is not a command; did you mean 'list'?",
		"xyzzy":  "'xyzzy' is not a command; see 'homeskillet help'",
		"go":     "'go' is not a command; see 'homeskillet help'",
	} {
		if _, err := parse([]string{word}, new(bytes.Buffer)); err == nil || err.Error() != want {
			t.Errorf("%q: %v, want %q", word, err, want)
		}
	}
	// A directory by that name is here: the error says how to name it.
	t.Chdir(t.TempDir())
	if err := os.Mkdir("dotfiles", 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := parse([]string{"dotfiles"}, new(bytes.Buffer))
	if want := "'dotfiles' is not a command; see 'homeskillet help'\n  for the repository in dotfiles, write it as a path: ./dotfiles"; err == nil || err.Error() != want {
		t.Errorf("a directory named like the word: %v, want %q", err, want)
	}
	if _, err := parse([]string{"bad\x1b[2Jword"}, new(bytes.Buffer)); err == nil || strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("a word with control characters reached the message: %v", err)
	}
	for a, b := range map[string]string{"": "", "link": "link", "lnk": "link", "kitten": "sitting"} {
		want := map[string]int{"": 0, "link": 0, "lnk": 1, "kitten": 3}[a]
		if got := editDistance(a, b); got != want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", a, b, got, want)
		}
	}
}
