//go:build linux || darwin || windows

// Tests of colors, homeskillet info, and homeskillet term: scripts never get
// color codes, a style wraps only what it is given, and info and term report
// what decides it.

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/install"
)

func TestColorFor(t *testing.T) {
	// A buffer, like a pipe or a file, is not a terminal: no colors, ever,
	// whatever the environment says.
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if c := colorFor(new(bytes.Buffer)); c.on || c.reason != "output is not a terminal" {
		t.Errorf("a buffer: %+v", c)
	}
	if got := paint(true, styleGood, "Ready:"); got != "\x1b[32mReady:\x1b[0m" {
		t.Errorf("paint on: %q", got)
	}
	if got := paint(false, styleGood, "Ready:"); got != "Ready:" {
		t.Errorf("paint off: %q", got)
	}
	if got := paint(true, styleHeading, ""); got != "" {
		t.Errorf("paint of nothing: %q", got)
	}
	var output bytes.Buffer
	PrintError(&output, errors.New("'lnk' is not a command"))
	if output.String() != "homeskillet: 'lnk' is not a command\n" {
		t.Errorf("an error in a pipe: %q", output.String())
	}
}

// TestInfo checks that info reports homeskillet, the system, and the
// terminal, and that term reports the terminal alone, each in plain lines
// for a script, and that neither takes an option.
func TestInfo(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("SHELL", "/bin/bash")
	terminalLines := []string{
		"output\tnot a terminal: a pipe or file, so results are one tab-separated line each, for scripts\n",
		"input\tnot a terminal: prompts read their answers from it, so echo y | approves and empty input declines\n",
		"LANG\ten_US.UTF-8\n",
		"colors\toff: output is not a terminal; NO_COLOR is set\n",
		"TERM\txterm-256color\n",
		"COLORTERM\tnot set\n",
		"program\t",
	}
	if runtime.GOOS != "windows" {
		terminalLines = append(terminalLines, "shell\t/bin/bash\n")
	}
	rest := []string{"version\t", "installed\t", "command\t", "os\t", "kernel\t", "platform\t"}
	for command, want := range map[string][]string{"info": append(slices.Clone(terminalLines), rest...), "term": terminalLines} {
		var output bytes.Buffer
		if err := Run(context.Background(), []string{command}, strings.NewReader(""), &output, &output); err != nil {
			t.Fatal(err)
		}
		for _, line := range want {
			if !strings.Contains(output.String(), line) {
				t.Errorf("%s lacks %q:\n%s", command, line, output.String())
			}
		}
		if command == "term" {
			for _, line := range rest {
				if strings.Contains(output.String(), line) {
					t.Errorf("term reports more than the terminal, %q:\n%s", line, output.String())
				}
			}
		}
		if strings.Contains(output.String(), "\x1b[") {
			t.Errorf("%s wrote color codes to a pipe: %q", command, output.String())
		}
		if err := Run(context.Background(), []string{command, "--dry-run"}, nil, &output, &output); err == nil || !strings.Contains(err.Error(), command+" takes no options") {
			t.Errorf("%s with an option: %v", command, err)
		}
	}
}

func TestUpToDate(t *testing.T) {
	for _, test := range []struct {
		state  install.State
		v      string
		newest bool
		want   string
	}{
		{install.State{Current: "v0.4.0", Following: true}, "v0.4.0", true, "homeskillet is up to date: v0.4.0 runs, following latest."},
		{install.State{Current: "v0.4.0"}, "v0.4.0", true, "homeskillet is up to date: v0.4.0 runs."},
		{install.State{Current: "v0.3.1"}, "v0.3.1", false, "v0.3.1 is installed, and it runs."},
		{install.State{Current: "v0.4.0", Following: true}, "v0.3.1", false, "v0.3.1 is already installed; homeskillet use v0.3.1 runs it."},
	} {
		if got := upToDate(test.state, test.v, test.newest, false); got != test.want {
			t.Errorf("%+v %s: %q, want %q", test.state, test.v, got, test.want)
		}
	}
	if got := upToDate(install.State{Current: "v0.4.0", Following: true}, "v0.4.0", true, true); !strings.HasPrefix(got, "\x1b[32mhomeskillet is up to date:\x1b[0m ") {
		t.Errorf("in color: %q", got)
	}
}

func TestUseView(t *testing.T) {
	requireSymlinks(t)
	_, home := repository(t)
	layout := installVersions(t, home, "v0.3.1", "v0.4.0")
	var output bytes.Buffer
	if err := useView(layout, "v0.3.1", false, &output); err != nil || output.String() != "v0.3.1 runs now, pinned; homeskillet use latest follows the newest again.\n" {
		t.Errorf("use v0.3.1: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := useView(layout, "latest", true, &output); err != nil || output.String() != "Dry run: nothing changed; homeskillet use latest would choose it.\n" {
		t.Errorf("use latest --dry-run: %q, %v", output.String(), err)
	}
	if state, _ := layout.Inspect(); state.Following {
		t.Error("the dry run followed latest")
	}
	output.Reset()
	if err := useView(layout, "latest", false, &output); err != nil || output.String() != "v0.4.0 runs now, following latest.\n" {
		t.Errorf("use latest: %q, %v", output.String(), err)
	}
}

func TestRemoveView(t *testing.T) {
	requireSymlinks(t)
	_, home := repository(t)
	layout := installVersions(t, home, "v0.2.0", "v0.3.0", "v0.3.1", "v0.4.0")
	var output bytes.Buffer
	if err := removeView(layout, []string{"v0.3.0"}, true, &output); err != nil || output.String() != "Dry run: nothing changed; homeskillet remove would delete v0.3.0.\n" {
		t.Errorf("remove --dry-run: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := removeView(layout, []string{"v0.3.0"}, false, &output); err != nil || output.String() != "Removed v0.3.0.\n" {
		t.Errorf("remove: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := removeView(layout, []string{"v0.2.0", "v0.3.1"}, false, &output); err != nil || output.String() != "Removed v0.2.0 and v0.3.1.\n" {
		t.Errorf("remove two: %q, %v", output.String(), err)
	}
	// The version that runs is refused, with what to do.
	if err := removeView(layout, []string{"v0.4.0"}, false, &output); err == nil || !strings.Contains(err.Error(), "is the version that runs") {
		t.Errorf("removing what runs: %v", err)
	}
}

func TestTilde(t *testing.T) {
	home := filepath.Join(string(filepath.Separator)+"home", "you")
	for path, want := range map[string]string{
		home: "~",
		filepath.Join(home, ".local", "bin", "homeskillet"): filepath.Join("~", ".local", "bin", "homeskillet"),
		home + "2":                  home + "2",
		filepath.Join("usr", "bin"): filepath.Join("usr", "bin"),
	} {
		if got := tilde(path, home); got != want {
			t.Errorf("tilde(%q) = %q, want %q", path, got, want)
		}
	}
	if got := tilde(home, ""); got != home {
		t.Errorf("no home: %q", got)
	}
	// Every spelling of the home counts, as where /home leads to /var/home.
	canonical := filepath.Join(string(filepath.Separator)+"var", "home", "you")
	if got := tilde(filepath.Join(home, "bin"), canonical, home); got != filepath.Join("~", "bin") {
		t.Errorf("a home spelled another way: %q", got)
	}
	if runtime.GOOS == "windows" {
		if got := tilde(`c:\users\you\bin`, `C:\Users\You`); got != `~\bin` {
			t.Errorf("a home in another case: %q", got)
		}
	}
}

// TestInstalledRow checks that info names what is wrong with an install, and
// what repairs it, rather than calling it nothing installed.
func TestInstalledRow(t *testing.T) {
	requireSymlinks(t)
	_, home := repository(t)
	layout := installVersions(t, home, "v0.3.0", "v0.4.0")
	if got := installedRow(layout); got != "v0.4.0, following latest" {
		t.Errorf("installed: %q", got)
	}
	if err := layout.Use("v0.3.0", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(layout.Program("v0.3.0"))); err != nil {
		t.Fatal(err)
	}
	if got := installedRow(layout); !strings.HasPrefix(got, "broken: the command points to") || !strings.Contains(got, "v0.3.0") || !strings.Contains(got, "use latest repairs it") {
		t.Errorf("a pinned version deleted by hand: %q", got)
	}
}

// TestPersonNotes checks that update words the install's notes for a person,
// with Note: in color only when colors are on.
func TestPersonNotes(t *testing.T) {
	lines := "note\t/home/you/.local/bin is not on PATH; add it\n"
	if got := personNotes(lines, false); got != "Note: /home/you/.local/bin is not on PATH; add it\n" {
		t.Errorf("without color: %q", got)
	}
	if got := personNotes(lines, true); got != styleAttention+"Note:"+styleReset+" /home/you/.local/bin is not on PATH; add it\n" {
		t.Errorf("with color: %q", got)
	}
}

// TestPrintErrorNil checks that PrintError, being exported, discards its
// output for a nil writer rather than panicking.
func TestPrintErrorNil(t *testing.T) {
	PrintError(nil, errors.New("discarded"))
}
