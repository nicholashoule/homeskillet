//go:build linux || darwin

// Tests of setup steps: their states, review and approval, --yes with its
// detached session, grouped commands, cancellation and interrupts, and the
// repository checks before each step.

package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
	return config.Step{Name: name, Run: config.Commands{{"sh", "-c", `: > "$HOME/$1"`, "sh", name}}}
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
		{Name: "created", Run: config.Commands{{"true"}}, Creates: ".local/bin/tool"},
		{Name: "provided", Run: config.Commands{{"true"}}, Provides: "fake-tool"},
		{Name: "missing", Run: config.Commands{{"true"}}, Creates: ".local/bin/absent", Provides: "absent-tool"},
		{Name: "unchecked", Run: config.Commands{{"true"}}},
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
	steps := []config.Step{{Name: "script", Run: config.Commands{{"sh", "setup/run.sh"}}}}
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
	// The done step is reported with what shows it is done, not offered.
	if strings.Contains(output.String(), "Run this step?") || strings.Contains(output.String(), "pending\tdone") ||
		strings.Contains(output.String(), "ran\tdone") ||
		!strings.Contains(output.String(), "done\tdone\t"+filepath.Join(home, "done")+"\n") {
		t.Fatalf("prompted, offered, or left out a done step:\n%s", output.String())
	}
	if !exists(filepath.Join(home, "first")) || !exists(filepath.Join(home, "second")) {
		t.Fatal("approved steps did not run")
	}
}

func TestRunEnvironment(t *testing.T) {
	steps := []config.Step{{Name: "env", Run: config.Commands{{"sh", "-c",
		`printf '%s\n' "$(pwd -P)" "$HOME" "$HOMESKILLET_HOME" "$HOMESKILLET_REPO" > "$HOME/env"`}}}}
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
	steps := []config.Step{{Name: "script", Run: config.Commands{{"sh", "setup/run.sh"}}}}
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
	steps := []config.Step{{Name: "script", Run: config.Commands{{"./setup/hello.sh"}}}}
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
	steps := []config.Step{{Name: "fail", Run: config.Commands{{"sh", "-c", "exit 3"}}}, touch("after")}
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
	steps := []config.Step{{Name: "slow", Run: config.Commands{{"sleep", "30"}}}}
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
	loosen := config.Step{Name: "loosen", Run: config.Commands{{"sh", "-c", `chmod 666 "$HOMESKILLET_REPO/dotfiles.json"`}}}
	steps = []config.Step{loosen, touch("after")}
	repo, home = fixture(t, steps...)
	err = Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true, Output: io.Discard})
	if err == nil || exists(filepath.Join(home, "after")) {
		t.Fatalf("a step ran after the repository became writable by others: %v", err)
	}
}

func TestRunYesGivesStepsNoInput(t *testing.T) {
	steps := []config.Step{{Name: "reads", Run: config.Commands{{"sh", "-c", `read -r line; printf '%s' "$line" > "$HOME/read"`}}}}
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

// TestRunGroupedCommands checks that a step's commands are shown together,
// approved once, and run in order, stopping at the first that fails.
func TestRunGroupedCommands(t *testing.T) {
	record := func(word string) []string {
		return []string{"sh", "-c", `printf '%s\n' "$1" >> "$HOME/order"`, "sh", word}
	}
	grouped := config.Step{Name: "grouped", Run: config.Commands{record("first"), record("second")}}
	broken := config.Step{Name: "broken", Run: config.Commands{record("third"), {"false"}, record("never")}}
	repo, home := fixture(t, grouped, broken)
	// Each step gets its own run: a step's stdin is the rest of Input, so a
	// step run earlier would read the answer meant for the next prompt.
	run := func(step config.Step) (string, error) {
		var output bytes.Buffer
		err := Run(context.Background(), prepare(t, []config.Step{step}, repo, home), Options{
			Repo: repo, Home: home, Input: strings.NewReader("y\n"), Output: &output, Errors: io.Discard,
		})
		return output.String(), err
	}
	output, err := run(grouped)
	if err != nil || strings.Count(output, "Run this step?") != 1 ||
		!strings.Contains(output, "  run: "+Quote(record("first"))+"\n  run: "+Quote(record("second"))+"\n  in:") {
		t.Fatalf("grouped commands not shown together and approved once: %v\n%s", err, output)
	}
	if _, err := run(broken); err == nil || !strings.Contains(err.Error(), "command 2 of 3, false") {
		t.Fatalf("a failing command did not stop its step: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "order")); err != nil || string(data) != "first\nsecond\nthird\n" {
		t.Fatalf("commands ran as %q, %v", data, err)
	}
}

// detached returns a step that creates $HOME/name-leader when it leads its
// own process group, as a step in a session of its own does, and
// $HOME/name-tty when it can open the controlling terminal.
func detached(name string) config.Step {
	return config.Step{Name: name, Run: config.Commands{{"sh", "-c", `
		[ "$(ps -o pgid= -p $$ | tr -d ' ')" = "$$" ] && : > "$HOME/$1-leader"
		(: < /dev/tty) 2> /dev/null && : > "$HOME/$1-tty"
		:`, "sh", name}}}
}

// TestRunYesDetachesSteps checks that a step approved by --yes runs in a
// session of its own, without the terminal, so sudo in it can neither prompt
// nor use a password cached for the terminal, while a step approved on its
// own keeps the terminal and the caller's process group.
func TestRunYesDetachesSteps(t *testing.T) {
	steps := []config.Step{detached("unattended")}
	repo, home := fixture(t, steps...)
	if err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(home, "unattended-leader")) || exists(filepath.Join(home, "unattended-tty")) {
		t.Fatal("an unattended step shares the caller's session or terminal")
	}
	steps = []config.Step{detached("approved")}
	if err := Run(context.Background(), prepare(t, steps, repo, home), Options{
		Repo: repo, Home: home, Input: strings.NewReader("y\n"), Output: io.Discard, Errors: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(home, "approved-leader")) {
		t.Fatal("a step approved on its own left the caller's process group")
	}
}

// interrupt runs steps under --yes, sends this process SIGINT, as Ctrl-C
// would, once the first step has created $HOME/started, and returns Run's
// error. It fails the test if Run has not returned ten seconds later.
func interrupt(t *testing.T, steps ...config.Step) (home string, err error) {
	t.Helper()
	// Kill what ignores the interrupt quickly, to keep the tests fast.
	grace := interruptGrace
	interruptGrace = 200 * time.Millisecond
	t.Cleanup(func() { interruptGrace = grace })
	repo, home := fixture(t, steps...)
	plans := prepare(t, steps, repo, home)
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), plans, Options{Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !exists(filepath.Join(home, "started")) {
		if time.Now().After(deadline) {
			t.Fatal("the step did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		return home, err
	case <-time.After(10 * time.Second):
		t.Fatal("setup did not stop after the interrupt")
		return home, nil
	}
}

// TestRunYesPassesInterrupts checks that Ctrl-C, which the terminal sends
// only to homeskillet's process group, still stops a step run under --yes.
func TestRunYesPassesInterrupts(t *testing.T) {
	if _, err := interrupt(t, config.Step{Name: "slow", Run: config.Commands{{"sh", "-c", `: > "$HOME/started"; sleep 30`}}}); err == nil {
		t.Fatal("the interrupted step succeeded")
	}
}

// TestRunYesKeepsIgnoredSignals checks that a signal the process started out
// ignoring, as under nohup, stays ignored: the step is not stopped by it and
// setup finishes.
func TestRunYesKeepsIgnoredSignals(t *testing.T) {
	signal.Ignore(syscall.SIGHUP)
	t.Cleanup(func() { signal.Reset(syscall.SIGHUP) })
	steps := []config.Step{{Name: "slow", Run: config.Commands{{"sh", "-c", `: > "$HOME/started"; sleep 0.5; : > "$HOME/finished"`}}}}
	repo, home := fixture(t, steps...)
	plans := prepare(t, steps, repo, home)
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), plans, Options{Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !exists(filepath.Join(home, "started")) {
		if time.Now().After(deadline) {
			t.Fatal("the step did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil || !exists(filepath.Join(home, "finished")) {
			t.Fatalf("an ignored hangup stopped the step: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("setup did not finish")
	}
}

// TestPrepareRejectsSharedHome checks that setup, like link, refuses a home
// directory others can write to, where a planted file could make a step look
// done.
func TestPrepareRejectsSharedHome(t *testing.T) {
	steps := []config.Step{touch("x")}
	repo, home := fixture(t, steps...)
	if err := os.Chmod(home, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(steps, repo, home); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("shared home accepted: %v", err)
	}
}

// TestRunYesStopsWhatIgnoresInterrupts checks that Ctrl-C stops a step whose
// background job ignores SIGINT, as a non-interactive shell's background jobs
// do, rather than waiting for the job while it holds the step's output.
func TestRunYesStopsWhatIgnoresInterrupts(t *testing.T) {
	if _, err := interrupt(t, config.Step{Name: "background", Run: config.Commands{{"sh", "-c", `: > "$HOME/started"; sleep 30 & wait`}}}); err == nil {
		t.Fatal("the interrupted step succeeded")
	}
}

// TestRunYesInterruptStopsSetup checks that Ctrl-C stops setup even when the
// step catches the signal and exits successfully: no later step runs.
func TestRunYesInterruptStopsSetup(t *testing.T) {
	home, err := interrupt(t,
		config.Step{Name: "catches", Run: config.Commands{{"sh", "-c", `trap 'exit 0' INT; : > "$HOME/started"; while :; do sleep 1; done`}}},
		touch("after"),
	)
	if err == nil || exists(filepath.Join(home, "after")) {
		t.Fatalf("setup went on after the interrupt: %v", err)
	}
}

// TestRunYesCancellationStopsChildren checks that canceling stops everything
// a step run under --yes started, not only the step, which would otherwise
// leave a background child running and holding its output open.
func TestRunYesCancellationStopsChildren(t *testing.T) {
	steps := []config.Step{{Name: "slow", Run: config.Commands{{"sh", "-c", "sleep 30 & wait"}}}}
	repo, home := fixture(t, steps...)
	plans := prepare(t, steps, repo, home)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Run(ctx, plans, Options{Repo: repo, Home: home, Yes: true, Output: io.Discard, Errors: io.Discard})
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("canceled step: %v after %v", err, time.Since(start))
	}
}

// TestRunYesInterruptKillsOrphans checks that an interrupt also stops a
// step's background children when the step's output goes to files: the shell
// then exits at once, before the grace period, and Wait returns while the
// children, which ignore the interrupt, would otherwise go on.
func TestRunYesInterruptKillsOrphans(t *testing.T) {
	grace := interruptGrace
	interruptGrace = 2 * time.Second
	t.Cleanup(func() { interruptGrace = grace })
	steps := []config.Step{{Name: "background", Run: config.Commands{{"sh", "-c", `sleep 30 & echo $! > "$HOME/child"; : > "$HOME/started"; wait`}}}}
	repo, home := fixture(t, steps...)
	plans := prepare(t, steps, repo, home)
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), plans, Options{Repo: repo, Home: home, Yes: true, Output: output, Errors: output})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !exists(filepath.Join(home, "started")) {
		if time.Now().After(deadline) {
			t.Fatal("the step did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("the interrupted step succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("setup did not stop")
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "child"))
	if err != nil {
		t.Fatal(err)
	}
	var child int
	if _, err := fmt.Sscan(string(data), &child); err != nil {
		t.Fatal(err)
	}
	// Right after Run returns, the child must be gone, not merely doomed by
	// a grace period that was canceled.
	deadline = time.Now().Add(time.Second)
	for running(t, child) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatal("a background child outlived the interrupted step")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// running reports whether process pid is alive. A killed child that nobody
// has reaped yet, as in a container whose init does not reap orphans, still
// answers kill(pid, 0) but is a zombie, which ps shows as state Z.
func running(t *testing.T, pid int) bool {
	t.Helper()
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	// Linux has /proc; the state follows the command name in parentheses.
	if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		_, after, found := strings.Cut(string(stat), ") ")
		return !found || !strings.HasPrefix(after, "Z")
	} else if runtime.GOOS == "linux" {
		return !errors.Is(err, fs.ErrNotExist)
	}
	out, err := exec.CommandContext(t.Context(), "ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // G204: ps with fixed options and a process ID this test started
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return false // ps found no such process
	case err != nil:
		t.Fatalf("ps: %v", err) // not an answer about the process
	}
	return !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
