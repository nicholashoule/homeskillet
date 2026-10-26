//go:build linux || darwin

// Tests of shell completion: the candidates __complete offers, the completion
// command, and the bash and zsh scripts loaded into real shells, with the test
// binary standing in for homeskillet.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// asHomeskillet names the environment variable that makes the test binary
// run as homeskillet (see TestMain).
const asHomeskillet = "HOMESKILLET_TEST_AS_HOMESKILLET"

// completionFixture returns a repository with setup steps for every platform,
// and an empty home.
func completionFixture(t *testing.T) (repo, home string) {
	t.Helper()
	repo, home = repository(t, "home/.bashrc")
	step := func(name string) config.Step { return config.Step{Name: name, Run: config.Commands{{"true"}}} }
	data, err := json.Marshal(config.Manifest{Setup: []config.Step{step("fonts"), step("fzf"), step("uv")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, home
}

// tree lists every path under the roots with its mode and size, to show that
// completing changed nothing.
func tree(t *testing.T, roots ...string) []string {
	t.Helper()
	var entries []string
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			entries = append(entries, fmt.Sprintf("%s %v %d", path, info.Mode(), info.Size()))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return entries
}

func TestCompletions(t *testing.T) {
	repo, home := completionFixture(t)
	before := tree(t, repo, home)
	for _, test := range []struct {
		words      []string
		directive  string
		candidates []string
	}{
		// Only commands come first; directories only once the word looks
		// like a repository path.
		{[]string{"li"}, ":none", []string{"link"}},
		{[]string{""}, ":none", commands},
		{[]string{"./do"}, ":dirs", nil},
		{[]string{"~/do"}, ":dirs", nil},
		{[]string{"link", ""}, ":dirs", nil},
		{[]string{"setup", "--home", home, "./"}, ":dirs", nil},
		{[]string{"link", "--pl"}, ":none", []string{"--platform"}},
		{[]string{"link", "-d"}, ":none", []string{"-data-dir", "-dry-run"}},
		{[]string{"link", "--for"}, ":none", []string{"--force"}},
		{[]string{"status", "--for"}, ":none", nil},
		{[]string{"link", "--platform", "darwin/"}, ":none", []string{"darwin/amd64", "darwin/arm64"}},
		{[]string{"link", "--platform", "=", "darwin/"}, ":none", []string{"darwin/amd64", "darwin/arm64"}},
		{[]string{"link", "--platform=darwin/a"}, ":none", []string{"darwin/amd64", "darwin/arm64"}},
		{[]string{"link", "--home", "/t"}, ":dirs", nil},
		{[]string{"link", "--home", "="}, ":dirs", nil},
		{[]string{"link", "--repo", repo, ".ba"}, ":files", nil},
		{[]string{"./dotfiles", ".ba"}, ":files", nil},
		{[]string{"lnik", ""}, ":none", nil},
		{[]string{"setup", "--repo", repo, "--home", home, "f"}, ":none", []string{"fonts", "fzf"}},
		{[]string{"setup", repo, "--home", home, "fonts", ""}, ":none", []string{"fzf", "uv"}},
		{[]string{"setup", "--home", home, "--repo", repo, "--platform", "windows", "u"}, ":none", []string{"uv"}},
		{[]string{"init", ""}, ":dirs", nil},
		{[]string{"self", ""}, ":none", selfCommands},
		{[]string{"self", "use", "--home", home, "l"}, ":none", []string{"latest"}},
		{[]string{"version", "-"}, ":none", nil},
		{[]string{"completion", ""}, ":none", []string{"bash", "zsh"}},
		{[]string{"help", "se"}, ":none", []string{"setup", "self"}},
		// After the terminator, nothing is an option: --d is the repository.
		{[]string{"link", "--", "--d"}, ":dirs", nil},
		{[]string{"link", "--", "r", "--d"}, ":files", nil},
	} {
		directive, candidates := completions(test.words)
		if directive != test.directive || !slices.Equal(candidates, test.candidates) {
			t.Errorf("%q: got %s %q, want %s %q", test.words, directive, candidates, test.directive, test.candidates)
		}
	}
	// Completing reads; it never creates a data directory or a record.
	if after := tree(t, repo, home); !slices.Equal(before, after) {
		t.Errorf("completing changed files:\nbefore %q\nafter  %q", before, after)
	}
}

// TestMatchingDropsUnprintable checks that a candidate that could rewrite the
// terminal, such as a version directory named with an escape sequence, is
// never offered to the shell.
func TestMatchingDropsUnprintable(t *testing.T) {
	if got := matching([]string{"v0.1.0", "v0.2.0\x1b[2J", "v0.3.0\nrm"}, "v"); !slices.Equal(got, []string{"v0.1.0"}) {
		t.Fatalf("got %q", got)
	}
}

func TestCompletionCommand(t *testing.T) {
	for shell := range completionScripts {
		var output bytes.Buffer
		if err := writeCompletion(shell, &output); err != nil || !strings.Contains(output.String(), "homeskillet __complete") {
			t.Errorf("%s: %v", shell, err)
		}
	}
	for args, want := range map[string]string{
		"completion":                "needs a shell",
		"completion bash --dry-run": "takes no options",
		"completion bash zsh":       "unexpected arguments",
	} {
		if _, err := parse(strings.Fields(args), new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", args, err)
		}
	}
	if err := writeCompletion("fish", new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "bash or zsh") {
		t.Errorf("fish: %v", err)
	}
	// The hidden command is never offered or listed.
	if slices.Contains(commands, completeCommand) {
		t.Error("the hidden completion command is listed")
	}
}

// homeskillet returns a directory holding a homeskillet command that runs
// the test binary as homeskillet (see TestMain), for scripts that call it.
func homeskillet(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + asHomeskillet + "=1 exec '" + executable + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "homeskillet"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// bashes returns each distinct bash to test with: the one on PATH, and
// /bin/bash, which on macOS is bash 3.2 even when Homebrew's comes first.
func bashes() []string {
	var found []string
	candidates := []string{"/bin/bash"}
	if bash, err := exec.LookPath("bash"); err == nil {
		candidates = append(candidates, bash)
	}
	var seen []string
	for _, bash := range candidates {
		resolved, err := filepath.EvalSymlinks(bash)
		if err != nil || slices.Contains(seen, resolved) {
			continue
		}
		seen = append(seen, resolved)
		found = append(found, bash)
	}
	return found
}

// TestBashCompletion loads the bash script into each bash, as eval in
// ~/.bashrc would, and completes command lines the way bash splits them,
// through the real homeskillet __complete, under set -u. On macOS it also
// runs under /bin/bash, which is bash 3.2.
func TestBashCompletion(t *testing.T) {
	if len(bashes()) == 0 {
		t.Skip("bash is not installed")
	}
	repo, home := completionFixture(t)
	dirs := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirs, "alpha"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory named like a command, as a repository's setup/ is: its
	// name must complete as the command, with no slash added.
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "setup"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := homeskillet(t) + string(filepath.ListSeparator) + os.Getenv("PATH")
	// COMP_WORDS holds the words as bash splits them; the cursor is on the last.
	const driver = `eval "$1"; shift
complete -p homeskillet > /dev/null || exit 1
if type compopt >/dev/null 2>&1; then case $(complete -p homeskillet) in *filenames*) echo 'filenames on for every candidate'; exit 1;; esac; fi
set -u
COMP_WORDS=(homeskillet "$@"); COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
_homeskillet
if ((${#COMPREPLY[@]})); then printf '%s\n' "${COMPREPLY[@]}"; fi`
	for _, test := range []struct {
		words []string
		want  []string
	}{
		{[]string{"li"}, []string{"link"}},
		// The setup command, though the current directory has a setup/.
		{[]string{"set"}, []string{"setup"}},
		// Nothing but commands, though the directory bash runs in has files.
		{[]string{""}, commands},
		{[]string{"setup", "--repo", repo, "--home", home, "f"}, []string{"fonts", "fzf"}},
		{[]string{"link", "--platform", "=", "darwin/a"}, []string{"darwin/amd64", "darwin/arm64"}},
		{[]string{"link", "--home", filepath.Join(dirs, "al")}, []string{filepath.Join(dirs, "alpha")}},
		{[]string{"link", "--home", "=", filepath.Join(dirs, "al")}, []string{filepath.Join(dirs, "alpha")}},
		{[]string{"completion", ""}, []string{"bash", "zsh"}},
		// Nothing to offer, or no homeskillet at all, offers nothing.
		{[]string{"version", ""}, nil},
		{[]string{"missing", ""}, nil},
	} {
		for _, bash := range bashes() {
			args := append([]string{"--noprofile", "--norc", "-c", driver, "bash", bashCompletion}, test.words...)
			cmd := exec.CommandContext(t.Context(), bash, args...) //nolint:gosec // G204: runs bash on the script under test with fixed arguments
			search := path
			if test.words[0] == "missing" {
				search = "/nonexistent"
			}
			cmd.Env = append(os.Environ(), "PATH="+search, "HOME="+home)
			cmd.Dir = cwd
			output, err := cmd.CombinedOutput()
			got := strings.Fields(string(output))
			if err != nil || !slices.Equal(got, test.want) {
				t.Errorf("%s %q: got %q, want %q (%v)", bash, test.words, got, test.want, err)
			}
		}
	}
}

// TestZshCompletion loads the zsh script after compinit, as eval in ~/.zshrc
// would, and checks that it registers for homeskillet. Running a completion
// needs zsh's line editor, so the candidates themselves are covered by
// TestCompletions. It runs where zsh is installed, as on macOS.
func TestZshCompletion(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	const driver = `autoload -Uz compinit && compinit -u -D -d /dev/null
eval "$1"
print -r -- ${_comps[homeskillet]}`
	cmd := exec.CommandContext(t.Context(), zsh, "-f", "-c", driver, "zsh", zshCompletion) //nolint:gosec // G204: runs zsh on the script under test with fixed arguments
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "_homeskillet" {
		t.Fatalf("zsh did not register the completion: %q, %v", output, err)
	}
}
