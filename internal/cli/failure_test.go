//go:build linux || darwin || windows

// Tests of how a failure is shown: in brief, keeping homeskillet's own
// indented lines and adding a hint for causes it recognizes, or whole with
// --verbose.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/setup"
)

func TestFailure(t *testing.T) {
	joined := errors.Join(errors.New("linking .bashrc: something broke"), errors.New("removing the temporary file: it was busy"))
	for _, test := range []struct {
		name    string
		err     error
		brief   string
		verbose string
	}{
		{"own advice kept", errors.New("manifest: 2 issues\n  files[0]: no source\n  setup[1]: no name"),
			"manifest: 2 issues\n  files[0]: no source\n  setup[1]: no name", ""},
		{"joined error dropped", joined,
			"linking .bashrc: something broke\n  run it again with --verbose to see the whole error",
			"linking .bashrc: something broke\nremoving the temporary file: it was busy"},
		{"failed step", &setup.StepError{Step: "uv", Err: errors.New("exit status 1")},
			"setup step uv: exit status 1\n  its output above says why; fix that, then run setup again, naming uv to run only it", ""},
		{"failed built-in step", &setup.StepError{Step: "git", Builtin: true, Err: errors.New("exit status 2")},
			"step git: exit status 2\n  its output above says why; fix that, then run homeskillet prepare again", ""},
		{"program not found", &setup.StepError{Step: "fonts", Err: fmt.Errorf("%q %w", "fc-cache", setup.ErrNotFound)},
			"setup step fonts: \"fc-cache\" not found on PATH\n  install the program, or put its directory on PATH, then run setup again, naming fonts to run only it", ""},
		{"permission", fmt.Errorf("data directory: %w", &fs.PathError{Op: "mkdir", Path: "/home/you/.local/share", Err: fs.ErrPermission}),
			"data directory: mkdir /home/you/.local/share: permission denied\n  check its owner and permissions; homeskillet runs as you, not as root", ""},
		{"nothing recognized", errors.New("'lnk' is not a command; did you mean 'link'?"), "'lnk' is not a command; did you mean 'link'?", ""},
	} {
		if got := (failure{err: test.err}).Error(); got != test.brief {
			t.Errorf("%s, in brief:\n%s\nwant:\n%s", test.name, got, test.brief)
		}
		// Verbose is the whole error, and the hint still.
		want := test.verbose
		if want == "" {
			want = test.err.Error()
		}
		if hint := hintFor(test.err); hint != "" {
			want += "\n  " + hint
		}
		if got := (failure{err: test.err, verbose: true}).Error(); got != want {
			t.Errorf("%s, verbose:\n%s\nwant:\n%s", test.name, got, want)
		}
	}
	// The cause is still there for errors.Is and errors.As.
	var step *setup.StepError
	if f := (failure{err: &setup.StepError{Step: "uv", Err: setup.ErrNotFound}}); !errors.As(f, &step) || !errors.Is(f, setup.ErrNotFound) {
		t.Error("a failure hides its cause")
	}
}

func TestVerboseOption(t *testing.T) {
	// Run presents what fails; --verbose shows it whole, anywhere on the
	// command line, and a view's own report is left alone.
	_, home := repository(t)
	var output bytes.Buffer
	err := Run(context.Background(), []string{"self", "use", "v9.9.9", "--home", home}, strings.NewReader(""), &output, &output)
	var f failure
	if !errors.As(err, &f) || f.verbose {
		t.Errorf("a failure: %#v", err)
	}
	err = Run(context.Background(), []string{"--verbose", "self", "use", "v9.9.9", "--home", home}, strings.NewReader(""), &output, &output)
	if !errors.As(err, &f) || !f.verbose {
		t.Errorf("a failure with --verbose: %#v", err)
	}
	if err := Run(context.Background(), []string{"version"}, nil, &output, &output); err != nil {
		t.Error(err)
	}
	// wsl passes --verbose on to the Linux build it runs.
	inv, err := parse([]string{"wsl", "link", "--verbose"}, new(bytes.Buffer))
	if err != nil || !strings.Contains(strings.Join(inv.wsl.forward, " "), "--verbose") {
		t.Errorf("wsl link --verbose: %+v, %v", inv.wsl, err)
	}
}
