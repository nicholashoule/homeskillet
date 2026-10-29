//go:build linux || darwin

// Tests of built-in steps, which homeskillet itself defines, as for
// homeskillet prepare: the same review and approval as a repository's steps,
// run in the home with no repository, and the checks they still get.

package setup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestRunBuiltinAsks(t *testing.T) {
	_, home := fixture(t)
	steps := []config.Step{touch("marker")}
	plans, err := PrepareBuiltin(steps, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		answer string
		ran    bool
	}{{"", false}, {"n\n", false}, {"maybe\n", false}, {"y\n", true}} {
		marker := filepath.Join(home, "marker")
		if err := os.RemoveAll(marker); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err := RunBuiltin(context.Background(), plans, Options{Home: home, Input: strings.NewReader(test.answer), Output: &output, Errors: io.Discard})
		if err != nil {
			t.Fatalf("answer %q: %v", test.answer, err)
		}
		// The step is shown exactly before the prompt, and runs in the home.
		if !strings.Contains(output.String(), "  run: sh -c") || !strings.Contains(output.String(), "  in:  "+home) || !strings.Contains(output.String(), "[y/N]") {
			t.Errorf("answer %q: the step was not shown before the prompt: %q", test.answer, output.String())
		}
		if exists(marker) != test.ran {
			t.Errorf("answer %q: ran %v, want %v", test.answer, exists(marker), test.ran)
		}
	}
}

func TestRunBuiltinEnvironment(t *testing.T) {
	_, home := fixture(t)
	steps := []config.Step{{Name: "env", Run: config.Commands{{"sh", "-c",
		`printf '%s\n' "$(pwd -P)" "$HOME" "$HOMESKILLET_HOME" "${HOMESKILLET_REPO-unset}" > "$HOME/env"`}}}}
	plans, err := PrepareBuiltin(steps, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOMESKILLET_REPO", "")
	if err := os.Unsetenv("HOMESKILLET_REPO"); err != nil {
		t.Fatal(err)
	}
	if err := RunBuiltin(context.Background(), plans, Options{Home: home, Yes: true, Output: io.Discard, Errors: io.Discard}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "env"))
	if err != nil {
		t.Fatal(err)
	}
	// There is no repository: steps run in the home, and none is named.
	if got, want := strings.Fields(string(data)), []string{home, home, home, "unset"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v (working directory, HOME, HOMESKILLET_HOME, HOMESKILLET_REPO)", got, want)
	}
}

func TestBuiltinChecks(t *testing.T) {
	_, home := fixture(t)
	// A step that is done is not offered.
	done := config.Step{Name: "sh", Run: config.Commands{{"false"}}, Provides: "sh"}
	plans, err := PrepareBuiltin([]config.Step{done}, home)
	if err != nil || len(plans) != 1 || plans[0].State != Done || plans[0].Found == "" {
		t.Fatalf("a step whose program is found: %+v, %v", plans, err)
	}
	var output bytes.Buffer
	if err := RunBuiltin(context.Background(), plans, Options{Home: home, Input: strings.NewReader("y\n"), Output: &output}); err != nil || strings.Contains(output.String(), "[y/N]") {
		t.Errorf("a done step was offered: %q, %v", output.String(), err)
	}
	// Built-in steps have commands and no inputs, and run in the home only.
	for _, step := range []config.Step{{Name: "empty"}, {Name: "inputs", Run: config.Commands{{"true"}}, Inputs: []string{"list"}},
		{Name: "empty command", Run: config.Commands{{"true"}, {}}}, {Name: "empty word", Run: config.Commands{{""}}}} {
		if _, err := PrepareBuiltin([]config.Step{step}, home); err == nil {
			t.Errorf("%s was accepted", step.Name)
		}
	}
	if err := RunBuiltin(context.Background(), plans, Options{Repo: home, Home: home}); err == nil {
		t.Error("a repository was accepted for built-in steps")
	}
	if err := RunBuiltin(context.Background(), plans, Options{}); err == nil {
		t.Error("an empty home was accepted")
	}
	// The home must still be private: steps run with HOME set to it.
	if err := os.Chmod(home, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareBuiltin([]config.Step{touch("x")}, home); err == nil {
		t.Error("a home others can write was accepted")
	}
}
