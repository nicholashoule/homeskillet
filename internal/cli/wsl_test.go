//go:build linux || darwin || windows

// Tests of the wsl command's arguments and helpers, and of receiving a copy
// of a repository through the hidden command wsl runs in a distribution.

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/mirror"
)

func TestParseWSL(t *testing.T) {
	for _, test := range []struct {
		args    []string
		sub     string
		repo    string
		forward []string
		err     string
	}{
		{args: []string{"wsl"}, err: "wsl needs a command"},
		{args: []string{"wsl", "bogus"}, err: "unknown command wsl bogus"},
		{args: []string{"wsl", "install", "--from", "bin/x", "--distribution", "Ubuntu", "--home", "~/scratch"},
			sub: "install", repo: ".", forward: []string{"--home=./scratch"}},
		{args: []string{"wsl", "install", "extra"}, err: "unexpected arguments"},
		{args: []string{"wsl", "link", ".", ".bashrc", "--force"}, sub: "link", repo: ".", forward: []string{"--force", ".bashrc"}},
		{args: []string{"wsl", "link", "~/dotfiles-wsl"}, sub: "link", repo: "~/dotfiles-wsl"},
		{args: []string{"wsl", "status", "/home/you/dotfiles", "--dry-run"}, sub: "status", repo: "/home/you/dotfiles", forward: []string{"--dry-run"}},
		// After setup, a word that is not plainly a path names a step.
		{args: []string{"wsl", "setup", "fonts"}, sub: "setup", repo: ".", forward: []string{"fonts"}},
		{args: []string{"wsl", "setup", "./dotfiles", "fonts", "--yes"}, sub: "setup", repo: "./dotfiles", forward: []string{"--yes", "fonts"}},
		{args: []string{"wsl", "link", ".", "--", "-odd"}, sub: "link", repo: ".", forward: []string{"--", "-odd"}},
		{args: []string{"wsl", "link", "--data-dir", "/tmp/data"}, sub: "link", repo: ".", forward: []string{"--data-dir=/tmp/data"}},
		// setup refuses --data-dir; wsl reads it only to find the copy.
		{args: []string{"wsl", "setup", ".", "--data-dir", "/tmp/data", "--dry-run"}, sub: "setup", repo: ".", forward: []string{"--dry-run"}},
		{args: []string{"wsl", "link", "--from", "x"}, err: "--from applies only to wsl install"},
		{args: []string{"wsl", "link", "--prefix", "x"}, err: "--prefix applies only to wsl install"},
		{args: []string{"wsl", "link", "--repo", "x"}, err: "name the repository after the command"},
		{args: []string{"wsl", "link", "--home"}, err: "needs a value"},
		{args: []string{"wsl", "link", "--home="}, err: "--home is empty"},
		{args: []string{"wsl", "link", "--bogus"}, err: "unknown option --bogus"},
		{args: []string{"wsl", "link", "--distribution", "-x"}, err: "--distribution must name a distribution"},
		{args: []string{"wsl", "link", ""}, err: "an argument is empty"},
	} {
		inv, err := parse(test.args, new(bytes.Buffer))
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("%q: got %v, want %q", test.args, err, test.err)
			}
			continue
		}
		if err != nil || inv.command != "wsl" || inv.sub != test.sub || inv.repo != test.repo || !slices.Equal(inv.wsl.forward, test.forward) {
			t.Errorf("%q: got %s %s %q %q, %v", test.args, inv.command, inv.sub, inv.repo, inv.wsl.forward, err)
		}
	}
	inv, err := parse([]string{"wsl", "install", "--from", "bin/x", "--distribution", "Ubuntu", "--dry-run", "--allow-root"}, new(bytes.Buffer))
	if err != nil || inv.wsl.from != "bin/x" || inv.wsl.distribution != "Ubuntu" || !inv.dryRun || !inv.root {
		t.Fatalf("wsl's own options: %+v, %v", inv, err)
	}
	if inv, err = parse([]string{"wsl", "setup", ".", "--data-dir", "/tmp/data"}, new(bytes.Buffer)); err != nil || inv.data != "/tmp/data" {
		t.Fatalf("setup's data directory, for the copy: %q, %v", inv.data, err)
	}
}

func TestWSLHelpers(t *testing.T) {
	for p, want := range map[string]string{"~": ".", "~/x": "./x", "/home/you/x": "/home/you/x"} {
		if got := inDistribution(p); got != want || !linuxPath(p) {
			t.Errorf("inDistribution(%q) = %q, want %q", p, got, want)
		}
	}
	for _, p := range []string{".", "dotfiles", `C:\dotfiles`, "~user/x"} {
		if linuxPath(p) {
			t.Errorf("%q taken for a path in the distribution", p)
		}
	}
	for home, want := range map[string]string{"": ".local/bin/homeskillet", "/tmp/h": "/tmp/h/.local/bin/homeskillet", "./scratch": "scratch/.local/bin/homeskillet"} {
		if got := linuxCommand(home); got != want {
			t.Errorf("linuxCommand(%q) = %q, want %q", home, got, want)
		}
	}
	a, b := mirrorName(filepath.FromSlash("/Users/you/dotfiles-windows")), mirrorName(filepath.FromSlash("/Users/you/src/dotfiles-windows"))
	if !strings.HasPrefix(a, "dotfiles-windows-") || a == b || a != mirrorName(filepath.FromSlash("/users/YOU/dotfiles-windows")) {
		t.Errorf("mirror names %q and %q", a, b)
	}
	if name := mirrorName(filepath.FromSlash("/Users/you/My Dotfiles!")); !strings.HasPrefix(name, "my-dotfiles--") {
		t.Errorf("unsafe characters kept: %q", name)
	}
	if v := versionOf("homeskillet v0.2.0\n  commit: abc\n"); v != "v0.2.0" {
		t.Errorf("versionOf: %q", v)
	}
	for v, want := range map[string]bool{"v0.2.0": true, "v0.2.0-rc.1": false, "v0.1.2-0.20261026184039-6b31e005a8b2+dirty": false, "devel": false} {
		if release(v) != want {
			t.Errorf("release(%q) = %v", v, !want)
		}
	}
}

// TestInstallNotes checks what wsl install shows of self install's output:
// the line about which version runs is left out, as wsl install chooses that
// itself, and the note about PATH no longer claims a directory is missing,
// since wsl.exe's PATH is not a shell's.
func TestInstallNotes(t *testing.T) {
	bin := "/home/you/.local/bin"
	installed := "installed\tv0.2.0\n" +
		"note\tv0.2.0+dirty still runs because it is latest; homeskillet self use v0.2.0 runs v0.2.0\n" +
		fmt.Sprintf(notOnPathLine, bin, "add it in your shell startup file") +
		"note\tsomething else\n"
	want := "installed\tv0.2.0\n" +
		"note\tif a new shell in Ubuntu cannot run homeskillet, add " + bin + " to PATH in your shell startup file\n" +
		"note\tsomething else\n"
	if got := installNotes(installed, "Ubuntu"); got != want {
		t.Errorf("installNotes:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range []string{"note\t is not on PATH; x", "note\t/x is on PATH", "installed\t/x is not on PATH; x"} {
		if dir, missing := missingFromPath(line); missing {
			t.Errorf("missingFromPath(%q) = %q", line, dir)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"~": home, "~/dotfiles": filepath.Join(home, "dotfiles"), "~user/x": "~user/x", "x~": "x~", "./~": "./~"}
	if runtime.GOOS == "windows" {
		cases[`~\dotfiles`] = filepath.Join(home, "dotfiles")
	}
	for path, want := range cases {
		if got, err := expandHome(path); err != nil || got != want {
			t.Errorf("expandHome(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

// TestReceiveMirror runs the hidden command a distribution receives a copy
// through, as wsl runs it there: update writes the copy into the data
// directory, compare reports how a stream differs from it and changes
// nothing, and temporary writes a copy of its own.
func TestReceiveMirror(t *testing.T) {
	repo, home := repository(t, "home/wsl/.bashrc", "bootstrap.json")
	send := func() *bytes.Buffer {
		var b bytes.Buffer
		if err := mirror.Send(context.Background(), &b, repo); err != nil {
			t.Fatal(err)
		}
		return &b
	}
	receive := func(mode string, operands ...string) string {
		t.Helper()
		var output bytes.Buffer
		args := append(append([]string{mirrorCommand, mode, "dotfiles-1"}, operands...), "--home", home)
		if err := Run(context.Background(), args, send(), &output, new(bytes.Buffer)); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		return strings.TrimSpace(output.String())
	}
	if got := receive("compare"); got != "none" {
		t.Fatalf("compare with no copy: %q", got)
	}
	copied := filepath.Join(home, ".local", "share", "homeskillet", mirrorsDir, "dotfiles-1")
	source := `C:\Users\you\dotfiles`
	if got := receive("update", source); got != "new "+copied {
		t.Fatalf("first update: %q, want the new copy at %q", got, copied)
	}
	if data, err := os.ReadFile(filepath.Join(copied, "home", "wsl", ".bashrc")); err != nil || string(data) != "config\n" {
		t.Fatalf("copied file: %q, %v", data, err)
	}
	if got := receive("compare"); got != "0 0 0 "+copied {
		t.Fatalf("compare with the copy: %q", got)
	}
	if got := receive("update", source); got != "0 0 0 "+copied {
		t.Fatalf("update with nothing new: %q", got)
	}
	// The copy says whose it is, for the Linux build's header.
	if note := describeCopy(invocation{}, home, copied); !strings.HasPrefix(note, source+", updated ") ||
		!strings.HasSuffix(note, "homeskillet.exe wsl link updates it from Windows") {
		t.Fatalf("describeCopy: %q", note)
	}
	if note := describeCopy(invocation{}, home, repo); note != "" {
		t.Fatalf("describeCopy of a repository that is no copy: %q", note)
	}
	temporary := receive("temporary")
	defer os.RemoveAll(filepath.Dir(temporary)) //nolint:errcheck // a temporary directory the test made
	if !strings.HasPrefix(filepath.Base(filepath.Dir(temporary)), "homeskillet-mirror-") {
		t.Fatalf("temporary copy at %q", temporary)
	}
	for _, args := range [][]string{
		{mirrorCommand, "update"},
		{mirrorCommand, "erase", "x"},
		{mirrorCommand, "update", "x", "--force"},
		{mirrorCommand, "compare", "x", `C:\source`},
		{mirrorCommand, "update", "x", "bad\x1b[2Ksource"},
	} {
		if err := Run(context.Background(), args, send(), new(bytes.Buffer), new(bytes.Buffer)); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}
