//go:build linux || darwin

// Tests of steps with inputs: offered until they run and succeed, then again
// only when an input or a command changes, and the checks on inputs and on
// the record.

package setup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestInputs(t *testing.T) {
	step := touch("packages")
	step.Inputs = []string{"lists/packages.txt"}
	repo, home := fixture(t, step)
	list := filepath.Join(repo, "lists", "packages.txt")
	write := func(contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(list), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(list, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("jq\n")
	data := filepath.Join(home, ".local", "share", "homeskillet")
	plan := func(s config.Step) Plan {
		t.Helper()
		plans, err := Prepare([]config.Step{s}, repo, home, data)
		if err != nil {
			t.Fatal(err)
		}
		return plans[0]
	}
	run := func(opts Options) {
		t.Helper()
		opts.Repo, opts.Home, opts.Data, opts.Yes = repo, home, data, true
		if err := Run(context.Background(), []Plan{plan(step)}, opts); err != nil {
			t.Fatal(err)
		}
	}
	ran := filepath.Join(home, "packages")
	if p := plan(step); p.State != Pending {
		t.Fatalf("never ran, yet %s", p.State)
	}
	if _, err := os.Stat(data); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("preparing wrote the data directory: %v", err)
	}
	run(Options{})
	if p := plan(step); p.State != Done || !strings.HasPrefix(Detail(p), "inputs unchanged since it ran ") {
		t.Fatalf("after running: %s, %q", p.State, Detail(p))
	}
	// Done, it runs no more, even with what it made gone.
	if err := os.Remove(ran); err != nil {
		t.Fatal(err)
	}
	run(Options{})
	if exists(ran) {
		t.Fatal("a done step ran again")
	}
	// Contents decide: a changed list offers it again, and the same list
	// written anew does not.
	write("jq\nfzf\n")
	if plan(step).State != Pending {
		t.Fatal("a changed input left the step done")
	}
	write("jq\n")
	if plan(step).State != Done {
		t.Fatal("the same contents again count as a change")
	}
	changed := step
	changed.Run = config.Commands{{"sh", "-c", "true"}}
	if plan(changed).State != Pending {
		t.Fatal("a changed command left the step done")
	}
	// With creates as well, both must say done.
	withCreates := step
	withCreates.Creates = "packages"
	if plan(withCreates).State != Pending {
		t.Fatal("done by its inputs though what it creates is gone")
	}
	// A step named again runs, done or not.
	run(Options{Again: true})
	if !exists(ran) {
		t.Fatal("Again did not run a done step")
	}
	// Without a data directory there is nowhere to look.
	if _, err := Prepare([]config.Step{step}, repo, home, ""); err == nil {
		t.Fatal("prepared a step with inputs without a data directory")
	}
}

// TestInputsFailedStep checks that a step that fails is not recorded, so it
// is offered again.
func TestInputsFailedStep(t *testing.T) {
	step := config.Step{Name: "fails", Run: config.Commands{{"false"}}, Inputs: []string{"list"}}
	repo, home := fixture(t, step)
	if err := os.WriteFile(filepath.Join(repo, "list"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "data")
	plans, err := Prepare([]config.Step{step}, repo, home, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), plans, Options{Repo: repo, Home: home, Data: data, Yes: true}); err == nil {
		t.Fatal("false succeeded")
	}
	if plans, err = Prepare([]config.Step{step}, repo, home, data); err != nil || plans[0].State != Pending {
		t.Fatalf("after failing: %v, %v", plans, err)
	}
}

// TestInputChecks checks that an input must be a regular file reached
// inside the repository, and that a damaged record is refused.
func TestInputChecks(t *testing.T) {
	repo, home := fixture(t)
	if err := os.Mkdir(filepath.Join(repo, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for input, want := range map[string]string{"gone": "no such file", "dir": "not a regular file"} {
		if err := CheckInput(repo, input); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("input %s: %v, want %q", input, err, want)
		}
		step := config.Step{Name: "s", Run: config.Commands{{"true"}}, Inputs: []string{input}}
		if _, err := Prepare([]config.Step{step}, repo, home, filepath.Join(home, "data")); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("prepared a step with input %s: %v", input, err)
		}
	}
	if err := CheckInput(repo, "gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing input: %v", err)
	}
	// Prepare refuses the whole repository over such a link (see
	// trust.CheckTree); check refuses the input itself.
	outside := filepath.Join(filepath.Dir(repo), "outside")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(repo, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	if err := CheckInput(repo, "escape"); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("an input leading outside: %v", err)
	}
	if err := os.Remove(escape); err != nil {
		t.Fatal(err)
	}

	data := filepath.Join(home, "data")
	if err := os.WriteFile(filepath.Join(repo, "list"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	step := config.Step{Name: "s", Run: config.Commands{{"true"}}, Inputs: []string{"list"}}
	for _, damaged := range []string{
		`{"version": 2, "homes": {}}`,
		`{"version": 1, "homes": {"` + home + `": {"` + repo + `": {"s": {"fingerprint": "not hex", "time": "2026-10-27T12:00:00Z"}}}}}`,
		`{"version": 1, "homes": {"relative": {}}}`,
		`{"version": 1} {}`,
	} {
		if err := os.WriteFile(RecordPath(data), []byte(damaged), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Prepare([]config.Step{step}, repo, home, data); err == nil || !strings.Contains(err.Error(), "delete it") {
			t.Errorf("record %s: %v", damaged, err)
		}
	}
}
