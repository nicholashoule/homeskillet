//go:build linux || darwin || windows

// How a failure is shown: in brief, what failed and a hint where the cause is
// one homeskillet recognizes, or whole with --verbose.

package cli

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/setup"
)

// failure is an error Run returns, shown for a person. homeskillet's own
// errors already say what to do, sometimes on indented lines after the
// first, such as each issue in a manifest, and those are kept. What is
// dropped in brief is the rest: further errors joined onto the first, as
// when cleaning up after a failure fails too, or a long chain of low-level
// causes. When a cause is one homeskillet recognizes, such as a permission
// it lacks or a step that failed, a hint says what to do. --verbose shows
// the error whole.
type failure struct {
	err     error
	verbose bool
}

func (f failure) Unwrap() error { return f.err }

func (f failure) Error() string {
	text := f.err.Error()
	hint := hintFor(f.err)
	if f.verbose {
		if hint != "" {
			text += "\n  " + hint
		}
		return text
	}
	lines := strings.Split(text, "\n")
	kept := lines[:1]
	omitted := false
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "  ") {
			kept = append(kept, line)
		} else if line != "" {
			omitted = true
		}
	}
	if hint != "" {
		kept = append(kept, "  "+hint)
	}
	if omitted {
		kept = append(kept, "  run it again with --verbose to see the whole error")
	}
	return strings.Join(kept, "\n")
}

// hintFor returns what to do about err when its cause is one homeskillet
// recognizes, or "". Errors homeskillet words itself carry their own advice
// and get none.
func hintFor(err error) string {
	var step *setup.StepError
	if errors.As(err, &step) {
		again := "run setup again, naming " + step.Step + " to run only it"
		if step.Builtin {
			again = "run " + selfName() + " prepare again"
		}
		if errors.Is(err, setup.ErrNotFound) {
			return "install the program, or put its directory on PATH, then " + again
		}
		return "its output above says why; fix that, then " + again
	}
	switch {
	case diskFull(err):
		return "the disk is full; free some space, then run it again"
	case errors.Is(err, fs.ErrPermission):
		return "check its owner and permissions; homeskillet runs as you, not as root"
	}
	return ""
}
