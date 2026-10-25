//go:build linux || darwin

package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// installer returns steps like a dotfiles repository's oh-my-posh and font
// steps: the first installs a program into $HOME/.local/bin, and the second
// runs it by name.
func installer() []config.Step {
	return []config.Step{
		{
			Name: "tool",
			Run: []string{"sh", "-c", `mkdir -p "$HOME/.local/bin" && chmod 700 "$HOME/.local" "$HOME/.local/bin" &&
printf '#!/bin/sh\n: > "$HOME/used"\n' > "$HOME/.local/bin/fake-tool" && chmod 700 "$HOME/.local/bin/fake-tool"`},
			Provides: "fake-tool",
		},
		{Name: "use-tool", Run: []string{"fake-tool"}, Creates: "used"},
		// A step's own shell finds the program too, through the exported PATH.
		{Name: "shell-tool", Run: []string{"sh", "-c", `command -v fake-tool > "$HOME/found"`}, Creates: "found"},
	}
}

func TestStepsFindProgramsEarlierStepsInstalled(t *testing.T) {
	// A PATH without the home's .local/bin, as on a fresh machine.
	t.Setenv("PATH", "/usr/bin:/bin")
	steps := installer()
	repo, home := fixture(t, steps...)
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(home, "used")) {
		t.Fatal("the second step did not run the installed program")
	}
	if data, err := os.ReadFile(filepath.Join(home, "found")); err != nil || !strings.HasSuffix(strings.TrimSpace(string(data)), "/.local/bin/fake-tool") {
		t.Fatalf("a step's shell did not find the installed program: %q, %v", data, err)
	}
	// Provides now sees the installed program, so nothing is offered again.
	for _, plan := range prepare(t, steps, repo, home) {
		if plan.State != Done {
			t.Errorf("%s is %s after setup", plan.Step.Name, plan.State)
		}
	}
}

func TestSharedUserBinNotSearched(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	steps := installer()
	repo, home := fixture(t, steps...)
	if err := Run(context.Background(), prepare(t, steps, repo, home)[:1], Options{Repo: repo, Home: home, Yes: true}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.Chmod(bin, 0o777); err != nil {
		t.Fatal(err)
	}
	// Others could have planted fake-tool, so it must be neither found nor
	// run.
	if got := searchPath(home); strings.Contains(got, bin) {
		t.Fatalf("shared %s joined PATH: %s", bin, got)
	}
	plans := prepare(t, steps, repo, home)
	if plans[0].State != Pending {
		t.Fatal("Provides found a program in a shared directory")
	}
	if err := Run(context.Background(), plans[1:2], Options{Repo: repo, Home: home, Yes: true}); err == nil || exists(filepath.Join(home, "used")) {
		t.Fatalf("ran a program from a shared directory: %v", err)
	}
}

func TestSearchPathAppends(t *testing.T) {
	_, home := fixture(t)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for inherited, want := range map[string]string{
		"/usr/bin:/bin":           "/usr/bin:/bin:" + bin,
		"":                        bin,
		bin + ":/usr/bin":         bin + ":/usr/bin",
		"/usr/bin:" + bin + ":/x": "/usr/bin:" + bin + ":/x",
	} {
		t.Setenv("PATH", inherited)
		if got := searchPath(home); got != want {
			t.Errorf("PATH %q: got %q, want %q", inherited, got, want)
		}
	}
}

func TestLookPath(t *testing.T) {
	dir := t.TempDir()
	for name, mode := range map[string]os.FileMode{"tool": 0o700, "data": 0o600} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPath("tool", "/nonexistent:"+dir); err != nil || got != filepath.Join(dir, "tool") {
		t.Fatalf("tool: %q, %v", got, err)
	}
	for _, name := range []string{"data", "subdir", "missing"} {
		if got, err := lookPath(name, dir); err == nil {
			t.Errorf("%s resolved to %s", name, got)
		}
	}
	// Relative entries, including the empty one, are never searched.
	t.Chdir(dir)
	for _, path := range []string{"", ".", ":", "subdir/..", "/nonexistent:"} {
		if got, err := lookPath("tool", path); err == nil {
			t.Errorf("PATH %q resolved tool in the current directory: %s", path, got)
		}
	}
	if got, err := lookPath("./setup/x.sh", ""); err != nil || got != "./setup/x.sh" {
		t.Errorf("relative path changed: %q, %v", got, err)
	}
}

func TestStepSeesReviewedName(t *testing.T) {
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no /proc to read argv from")
	}
	// argv[0] is what the user reviewed ("sh"), not the resolved path.
	steps := []config.Step{{Name: "argv", Run: []string{"sh", "-c", `tr '\0' '\n' < /proc/$$/cmdline | head -n 1 > "$HOME/argv0"`}}}
	repo, home := fixture(t, steps...)
	if err := Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "argv0")); err != nil || strings.TrimSpace(string(data)) != "sh" {
		t.Fatalf("argv[0] = %q, %v", data, err)
	}
}

func TestProvidesFindsHomebrewPrograms(t *testing.T) {
	// A PATH without Homebrew, as in the shell that just installed it.
	t.Setenv("PATH", "/usr/bin:/bin")
	brewBin := t.TempDir()
	defer func(previous []string) { homebrewBins = previous }(homebrewBins)
	homebrewBins = []string{filepath.Join(brewBin, "missing"), brewBin}
	steps := []config.Step{{Name: "homebrew", Run: []string{"false"}, Provides: "brew"}}
	repo, home := fixture(t, steps...)
	if plans := prepare(t, steps, repo, home); plans[0].State != Pending {
		t.Fatal("done before Homebrew is installed")
	}
	if err := os.WriteFile(filepath.Join(brewBin, "brew"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if plans := prepare(t, steps, repo, home); plans[0].State != Done {
		t.Fatal("brew in a Homebrew bin directory not recognized")
	}
	// Homebrew's directories are for detection only; steps cannot run from them.
	if got := searchPath(home); strings.Contains(got, brewBin) {
		t.Fatalf("Homebrew directory joined the step PATH: %s", got)
	}
}
