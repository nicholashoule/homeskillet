//go:build linux || darwin

// Tests of wsl on Linux and macOS: refused outside WSL, and inside it handed
// to the Windows build that wsl install recorded.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHandOffArgs(t *testing.T) {
	// The test's own stand-in for wslpath: paths under /mnt/c are on drive C.
	windows := func(p string) (string, error) {
		if rest, ok := strings.CutPrefix(p, "/mnt/c/"); ok {
			return `C:\` + strings.ReplaceAll(rest, "/", `\`), nil
		}
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want []string
		err  string
	}{
		{args: []string{"wsl", "link"}, want: []string{"wsl", "link", "--distribution=Ubuntu"}},
		{args: []string{"wsl", "link", "--distribution", "Debian", "--dry-run"}, want: []string{"wsl", "link", "--distribution=Debian", "--dry-run"}},
		{args: []string{"wsl", "link", "/mnt/c/Users/you/dotfiles", ".bashrc"},
			want: []string{"wsl", "link", "--distribution=Ubuntu", `C:\Users\you\dotfiles`, ".bashrc"}},
		{args: []string{"wsl", "status", "~/dotfiles-wsl"}, want: []string{"wsl", "status", "--distribution=Ubuntu", filepath.Join(home, "dotfiles-wsl")}},
		{args: []string{"wsl", "link", "."}, want: []string{"wsl", "link", "--distribution=Ubuntu", cwd}},
		{args: []string{"wsl", "setup", "--data-dir", "/tmp/data", "fonts"}, want: []string{"wsl", "setup", "--distribution=Ubuntu", "--data-dir=/tmp/data", "fonts"}},
		{args: []string{"wsl", "setup", "--dry-run", "--data-dir", "/tmp/data"}, want: []string{"wsl", "setup", "--distribution=Ubuntu", "--dry-run", "--data-dir=/tmp/data"}},
		{args: []string{"wsl", "install", "--from", "/mnt/c/src/bin/homeskillet-linux-amd64"},
			want: []string{"wsl", "install", "--distribution=Ubuntu", `--from=C:\src\bin\homeskillet-linux-amd64`}},
		{args: []string{"wsl", "install", "--from", "/home/you/homeskillet"}, err: "not on a Windows drive"},
	} {
		inv, err := parse(test.args, new(bytes.Buffer))
		if err != nil {
			t.Fatalf("%q: %v", test.args, err)
		}
		got, err := handOffArgs(inv, "Ubuntu", windows)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("%q: got %q, %v; want %q", test.args, got, err, test.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, test.want) {
			t.Errorf("%q: got %q, %v; want %q", test.args, got, err, test.want)
		}
	}
}

// TestHandOff runs wsl as in a WSL shell, with a script standing in for the
// Windows build: it is refused outside WSL, says how to record a build when
// none is, and otherwise runs the recorded one, saying so, with its exit
// status.
func TestHandOff(t *testing.T) {
	_, home := repository(t)
	run := func(diagnostics *bytes.Buffer) error {
		return Run(context.Background(), []string{"wsl", "link", "--home", home}, nil, new(bytes.Buffer), diagnostics)
	}
	t.Setenv("WSL_DISTRO_NAME", "")
	if err := run(new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "wsl runs on Windows") {
		t.Fatalf("outside WSL: %v", err)
	}
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	if err := run(new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "run homeskillet wsl install from Windows") {
		t.Fatalf("with no Windows build recorded: %v", err)
	}
	program := filepath.Join(home, "windows", "homeskillet.exe")
	if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\nexit 3\n"
	if err := os.WriteFile(program, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{windowsBuildCommand, `C:\Users\you\homeskillet.exe`, program, "--home", home}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	if err := run(&diagnostics); !Reported(err) {
		t.Fatalf("the Windows build's failure: %v", err)
	}
	if !strings.Contains(diagnostics.String(), `running the Windows build, C:\Users\you\homeskillet.exe`) {
		t.Errorf("the hand-off went unsaid: %q", diagnostics.String())
	}
	args, err := os.ReadFile(program + ".args")
	if want := "wsl\nlink\n--distribution=Ubuntu\n--home=" + home + "\n"; err != nil || string(args) != want {
		t.Errorf("the Windows build got %q, %v; want %q", args, err, want)
	}
}
