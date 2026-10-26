//go:build linux || darwin

// Tests of pointing out linked commands that run instead of others on PATH, in
// the view and on standard error.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/link"
)

// installed creates dir holding an executable for each name, as a PATH
// directory such as /usr/bin would.
func installed(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShadowed(t *testing.T) {
	repo, home := repository(t, "tool", "plain", "unique")
	for _, name := range []string{"tool", "unique"} {
		if err := os.Chmod(filepath.Join(repo, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	system := filepath.Join(filepath.Dir(home), "system")
	installed(t, system, "tool", "plain")
	// tools is a PATH directory in home other than userBins, which count as
	// first wherever PATH lists them.
	tools := filepath.Join(home, "tools")
	path := func(dirs ...string) string { return strings.Join(dirs, string(filepath.ListSeparator)) }
	plan := func(target, source string, state link.State) link.Plan {
		return link.Plan{Source: filepath.Join(repo, source), Target: filepath.Join(home, target), State: state}
	}
	shadows := filepath.Join(system, "tool")
	for _, test := range []struct {
		name  string
		path  string
		plan  link.Plan
		force bool
		want  string
	}{
		{"to be linked", path(system), plan(".local/bin/tool", "tool", link.Missing), false, shadows},
		{"linked", path(system), plan("bin/tool", "tool", link.Linked), false, shadows},
		{"user bin listed last", path(system, filepath.Join(home, ".local", "bin")), plan(".local/bin/tool", "tool", link.Missing), false, shadows},
		{"ahead on PATH", path(tools, system), plan("tools/tool", "tool", link.Missing), false, shadows},
		{"behind on PATH", path(system, tools), plan("tools/tool", "tool", link.Missing), false, ""},
		{"not on PATH", path(system), plan(".config/tool", "tool", link.Missing), false, ""},
		{"not executable", path(system), plan(".local/bin/plain", "plain", link.Missing), false, ""},
		{"no other command", path(system), plan(".local/bin/unique", "unique", link.Missing), false, ""},
		{"relative entry", path(".", "relative"), plan(".local/bin/tool", "tool", link.Missing), false, ""},
		{"conflict", path(system), plan(".local/bin/tool", "tool", link.Conflict), false, ""},
		{"conflict under force", path(system), plan(".local/bin/tool", "tool", link.Conflict), true, shadows},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PATH", test.path)
			if got := shadowed([]link.Plan{test.plan}, home, test.force)[test.plan.Target]; got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

// TestShadowNotes checks that status and link point out a command that runs
// instead of another, on the view or else on diagnostics, leaving the lines
// scripts parse alone, and that quiet and unlink say nothing.
func TestShadowNotes(t *testing.T) {
	repo, home := repository(t, "home/.local/bin/tool")
	if err := os.Chmod(filepath.Join(repo, "home", ".local", "bin", "tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(filepath.Dir(home), "system")
	installed(t, system, "tool")
	t.Setenv("PATH", system)
	target, other := filepath.Join(home, ".local", "bin", "tool"), filepath.Join(system, "tool")

	manifest, err := config.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := parse([]string{"status", repo, "--home", home}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runLinks(inv, manifest.Files, repo, home, filepath.Join(home, ".local", "share", "homeskillet"), &output, new(bytes.Buffer), &view{}); err != nil {
		t.Fatal(err)
	}
	if want := "Shadowing commands (1): a shell runs these first; link them only from a repository you trust\n  " + target + "  instead of " + other; !strings.Contains(output.String(), want) {
		t.Errorf("view lacks %q:\n%s", want, output.String())
	}

	for _, test := range []struct {
		args []string
		note bool
	}{
		{[]string{"status"}, true},
		{[]string{"link", "--dry-run"}, true},
		{[]string{"link", "--quiet"}, false},
		{[]string{"unlink"}, false},
	} {
		var output, diagnostics bytes.Buffer
		err := Run(context.Background(), append(test.args, repo, "--home", home), nil, &output, &diagnostics)
		note := strings.Contains(diagnostics.String(), target+" runs instead of "+other)
		if err != nil || note != test.note || strings.Contains(output.String(), "instead of") {
			t.Errorf("%v: error %v, output %q, diagnostics %q", test.args, err, output.String(), diagnostics.String())
		}
	}
}
