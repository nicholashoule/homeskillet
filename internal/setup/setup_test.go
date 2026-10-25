//go:build linux || darwin

package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// fixture creates a private repository whose manifest lists steps, plus an
// empty home directory, under a canonical temporary root.
func fixture(t *testing.T, steps ...config.Step) (repo, home string) {
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
	data, err := json.Marshal(config.Manifest{Setup: steps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, home
}

func prepare(t *testing.T, steps []config.Step, repo, home string) []Plan {
	t.Helper()
	plans, err := Prepare(steps, repo, home)
	if err != nil {
		t.Fatal(err)
	}
	return plans
}

// touch returns a step that creates $HOME/name, so tests can see whether it ran.
func touch(name string) config.Step {
	return config.Step{Name: name, Run: []string{"sh", "-c", `: > "$HOME/$1"`, "sh", name}}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPrepareStates(t *testing.T) {
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "fake-tool"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	steps := []config.Step{
		{Name: "created", Run: []string{"true"}, Creates: ".local/bin/tool"},
		{Name: "provided", Run: []string{"true"}, Provides: "fake-tool"},
		{Name: "missing", Run: []string{"true"}, Creates: ".local/bin/absent", Provides: "absent-tool"},
		{Name: "unchecked", Run: []string{"true"}},
	}
	repo, home := fixture(t, steps...)
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "tool"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	var states []State
	for _, plan := range prepare(t, steps, repo, home) {
		states = append(states, plan.State)
	}
	if want := []State{Done, Done, Pending, Pending}; !slices.Equal(states, want) {
		t.Fatalf("got %v, want %v", states, want)
	}
}

func TestPrepareRejectsSharedRepositoryFiles(t *testing.T) {
	steps := []config.Step{{Name: "script", Run: []string{"sh", "setup/run.sh"}}}
	repo, home := fixture(t, steps...)
	script := filepath.Join(repo, "setup", "run.sh")
	if err := os.Mkdir(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	prepare(t, steps, repo, home)
	if err := os.Chmod(script, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(steps, repo, home); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("shared script accepted: %v", err)
	}
}

func TestRunAsksBeforeEachStep(t *testing.T) {
	for answer, approve := range map[string]bool{
		"y\n": true, "yes\n": true, "Y\n": true, "y": true,
		"n\n": false, "\n": false, "": false, "sure\n": false,
	} {
		t.Run(fmt.Sprintf("%q", answer), func(t *testing.T) {
			steps := []config.Step{touch("marker")}
			repo, home := fixture(t, steps...)
			var output bytes.Buffer
			err := Run(context.Background(), prepare(t, steps, repo, home), Options{
				Repo: repo, Home: home, Input: strings.NewReader(answer), Output: &output, Errors: io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			// The prompt must show exactly what will run before asking.
			if want := `run: sh -c ': > "$HOME/$1"' sh marker`; !strings.Contains(output.String(), want) {
				t.Fatalf("prompt did not show the command:\n%s", output.String())
			}
			if ran := exists(filepath.Join(home, "marker")); ran != approve {
				t.Fatalf("ran = %v, want %v", ran, approve)
			}
			if !approve && !strings.Contains(output.String(), "skipped\tmarker") {
				t.Fatalf("declined step not reported:\n%s", output.String())
			}
		})
	}
}

func TestRunYesSkipsPromptsAndDoneSteps(t *testing.T) {
	done := touch("done")
	done.Creates = "done"
	steps := []config.Step{touch("first"), done, touch("second")}
	repo, home := fixture(t, steps...)
	if err := os.WriteFile(filepath.Join(home, "done"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Output: &output, Errors: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Run this step?") || strings.Contains(output.String(), "\tdone") {
		t.Fatalf("prompted or offered a done step:\n%s", output.String())
	}
	if !exists(filepath.Join(home, "first")) || !exists(filepath.Join(home, "second")) {
		t.Fatal("approved steps did not run")
	}
}

func TestRunEnvironment(t *testing.T) {
	steps := []config.Step{{Name: "env", Run: []string{"sh", "-c",
		`printf '%s\n' "$(pwd -P)" "$HOME" "$HOMESKILLET_HOME" "$HOMESKILLET_REPO" > "$HOME/env"`}}}
	repo, home := fixture(t, steps...)
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "env"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(data)), []string{repo, home, home, repo}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v (working directory, HOME, HOMESKILLET_HOME, HOMESKILLET_REPO)", got, want)
	}
}

func TestPrepareRejectsSymlinksLeavingRepository(t *testing.T) {
	steps := []config.Step{{Name: "script", Run: []string{"sh", "setup/run.sh"}}}
	repo, home := fixture(t, steps...)
	// The script the step runs is a symlink into a directory anyone can
	// write, so the code that runs is not the code the repository vouches for.
	shared := filepath.Join(filepath.Dir(repo), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "run.sh"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "setup"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "setup", "run.sh")
	if err := os.Symlink(filepath.Join("..", "..", "shared", "run.sh"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(steps, repo, home); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("symlink to a shared script accepted: %v", err)
	}
	// The same link pointing at a private file in the repository is fine.
	if err := os.WriteFile(filepath.Join(repo, "setup", "real.sh"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.sh", link); err != nil {
		t.Fatal(err)
	}
	prepare(t, steps, repo, home)
}

func TestRunRepositoryScript(t *testing.T) {
	steps := []config.Step{{Name: "script", Run: []string{"./setup/hello.sh"}}}
	repo, home := fixture(t, steps...)
	script := filepath.Join(repo, "setup", "hello.sh")
	if err := os.Mkdir(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > \"$HOME/hello\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard,
	})
	if err != nil || !exists(filepath.Join(home, "hello")) {
		t.Fatalf("relative script did not run from the repository: %v", err)
	}
}

func TestRunStopsAtFailure(t *testing.T) {
	steps := []config.Step{{Name: "fail", Run: []string{"sh", "-c", "exit 3"}}, touch("after")}
	repo, home := fixture(t, steps...)
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "setup step fail") {
		t.Fatalf("failure not reported: %v", err)
	}
	if exists(filepath.Join(home, "after")) {
		t.Fatal("ran a step after a failure")
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	steps := []config.Step{{Name: "slow", Run: []string{"sleep", "30"}}}
	repo, home := fixture(t, steps...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard,
	})
	if err == nil {
		t.Fatal("canceled context did not stop the step")
	}
}

func TestQuote(t *testing.T) {
	for want, argv := range map[string][]string{
		"brew install oh-my-posh": {"brew", "install", "oh-my-posh"},
		"echo 'a b'":              {"echo", "a b"},
		`echo 'it'\''s'`:          {"echo", "it's"},
		"echo ''":                 {"echo", ""},
		"sh -c 'a | b; c'":        {"sh", "-c", "a | b; c"},
		"sh -c '$HOME'":           {"sh", "-c", "$HOME"},
	} {
		if got := Quote(argv); got != want {
			t.Errorf("Quote(%q) = %s, want %s", argv, got, want)
		}
	}
}

func TestRunRequiresDirectories(t *testing.T) {
	steps := []config.Step{touch("ran")}
	repo, home := fixture(t, steps...)
	plans := prepare(t, steps, repo, home)
	// An unset Repo would run in the current directory and an unset Home
	// would empty HOME, so each must be refused rather than defaulted.
	for name, opts := range map[string]Options{
		"zero":        {Yes: true},
		"no repo":     {Home: home, Yes: true},
		"no home":     {Repo: repo, Yes: true},
		"system home": {Repo: repo, Home: "/etc", Yes: true},
	} {
		if err := Run(context.Background(), plans, opts); err == nil {
			t.Errorf("%s: ran", name)
		}
	}
	if exists(filepath.Join(home, "ran")) {
		t.Fatal("a refused run executed a step")
	}
	for _, dir := range []string{"/", "/usr"} {
		if _, err := Prepare(steps, repo, dir); err == nil {
			t.Errorf("Prepare accepted home %s", dir)
		}
		if _, err := Prepare(steps, dir, home); err == nil {
			t.Errorf("Prepare accepted repository %s", dir)
		}
	}
}

func TestRunNilStreams(t *testing.T) {
	steps := []config.Step{touch("ran")}
	repo, home := fixture(t, steps...)
	plans := prepare(t, steps, repo, home)
	// Without --yes, nil input declines and nil output discards the prompt.
	//nolint:staticcheck // SA1012: a nil context is what this test checks
	if err := Run(nil, plans, Options{Repo: repo, Home: home}); err != nil || exists(filepath.Join(home, "ran")) {
		t.Fatalf("declined run: %v", err)
	}
	if err := Run(context.Background(), plans, Options{Repo: repo, Home: home, Yes: true}); err != nil || !exists(filepath.Join(home, "ran")) {
		t.Fatalf("approved run: %v", err)
	}
	if err := Describe(nil, plans[0], repo); err != nil {
		t.Fatal(err)
	}
}

func TestRunChecksEachStepAgain(t *testing.T) {
	// The first step does the second's work, so the second is not run.
	first := touch("a")
	second := touch("b")
	second.Creates = "a"
	steps := []config.Step{first, second}
	repo, home := fixture(t, steps...)
	var output bytes.Buffer
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true, Output: &output})
	if err != nil || exists(filepath.Join(home, "b")) || !strings.Contains(output.String(), "done\tb") {
		t.Fatalf("second step ran after the first did its work: %v\n%s", err, output.String())
	}
	// A step that loosens the repository stops the steps after it.
	loosen := config.Step{Name: "loosen", Run: []string{"sh", "-c", `chmod 666 "$HOMESKILLET_REPO/dotfiles.json"`}}
	steps = []config.Step{loosen, touch("after")}
	repo, home = fixture(t, steps...)
	err = Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true, Output: io.Discard})
	if err == nil || exists(filepath.Join(home, "after")) {
		t.Fatalf("a step ran after the repository became writable by others: %v", err)
	}
}

func TestRunYesGivesStepsNoInput(t *testing.T) {
	steps := []config.Step{{Name: "reads", Run: []string{"sh", "-c", `read -r line; printf '%s' "$line" > "$HOME/read"`}}}
	repo, home := fixture(t, steps...)
	err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Input: strings.NewReader("secret\n"), Output: io.Discard, Errors: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "read")); err != nil || len(data) != 0 {
		t.Fatalf("an unattended step read input: %q, %v", data, err)
	}
}

func TestConfirmBoundsTheAnswer(t *testing.T) {
	// A megabyte of y without a newline is not an approval, and is not
	// kept in memory.
	approved, err := confirm(strings.NewReader(strings.Repeat("y", 1<<20)), io.Discard)
	if err != nil || approved {
		t.Fatalf("approved %v, %v", approved, err)
	}
	if approved, err := confirm(strings.NewReader("  yes  \n"), io.Discard); err != nil || !approved {
		t.Fatalf("yes declined: %v", err)
	}
}
