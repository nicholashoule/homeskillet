//go:build linux || darwin || windows

// Tests of what homeskillet, run alone with nothing to link yet, shows: the
// steps to start, for a person, and the error, for a script.

package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestStartGuide(t *testing.T) {
	home := t.TempDir()
	repo := "~/dotfiles"
	if runtime.GOOS == "windows" {
		repo = copyable(filepath.Join(home, "dotfiles"))
	}
	var output bytes.Buffer
	if err := startGuide(&output, home); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	// The installed command needs no installing; each step is numbered in
	// order, and the commands are ready to copy.
	for _, want := range []string{
		"Nothing to link:", "1. Check this machine is ready", "homeskillet prepare\n",
		"2. Start a repository", "homeskillet init " + repo + "\n", "or clone the one you have", "git clone URL " + repo + "\n",
		"3. Put your dotfiles in its home folder", "4. Preview the links", "homeskillet link " + repo + " --dry-run\n",
		"5. Link them", examples, "Every command: homeskillet help",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is missing from:\n%s", want, text)
		}
	}
	if strings.Contains(text, " install") {
		t.Errorf("the installed command is told to install itself:\n%s", text)
	}
	// The download, run before it is installed, says to install it first,
	// named as it was run.
	saved := selfName
	t.Cleanup(func() { selfName = saved })
	selfName = func() string { return `.\homeskillet-windows-amd64.exe` }
	output.Reset()
	if err := startGuide(&output, home); err != nil {
		t.Fatal(err)
	}
	// prepare comes first, since installing on Windows needs Developer Mode.
	if text := output.String(); !strings.Contains(text, `.\homeskillet-windows-amd64.exe prepare`) ||
		!strings.Contains(text, `2. Install it as a command`) || !strings.Contains(text, `.\homeskillet-windows-amd64.exe install`) ||
		!strings.Contains(text, "3. Start a repository") {
		t.Errorf("the download is not told to prepare, then install itself:\n%s", text)
	}
}

func TestCopyable(t *testing.T) {
	for path, want := range map[string]string{
		`C:\Users\you\dotfiles`:        `C:\Users\you\dotfiles`,
		`C:\Users\Jo Smith\dotfiles`:   `"C:\Users\Jo Smith\dotfiles"`,
		`C:\Users\you&me\dotfiles`:     `"C:\Users\you&me\dotfiles"`,
		"C:\\Users\\you\x1b\\dotfiles": `"C:\\Users\\you\x1b\\dotfiles"`,
	} {
		if got := copyable(path); got != want {
			t.Errorf("copyable(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestBareWithNothingLinked(t *testing.T) {
	// A script that runs homeskillet alone where there is nothing to link
	// still gets the error, with how to name a repository and how to start.
	home := t.TempDir()
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	err := Run(context.Background(), []string{"--home", home}, nil, &output, &output)
	if !errors.Is(err, config.ErrNothingToManage) || !strings.Contains(err.Error(), "name one: homeskillet status") ||
		!strings.Contains(err.Error(), "start one with homeskillet init") {
		t.Fatalf("%v\n%s", err, output.String())
	}
}
