//go:build linux || darwin

// Package setup runs the setup steps a manifest declares, such as installing
// oh-my-posh, after the user has reviewed and approved each one.
//
// A setup step is code from the configuration repository, which may be
// someone else's. Its safety rests on review, not on sandboxing:
//
//   - The whole repository must pass trust.CheckTree, so nobody else can have
//     changed what the user reviews, or a script that a step runs.
//   - Each step is shown exactly as it will execute, with its argument vector
//     and working directory, before it runs, and the default answer is no.
//   - Steps run as argument vectors, never through an implicit shell, so
//     shell metacharacters in an argument are inert unless the step itself
//     invokes a shell for everyone to see.
//   - HOME is set to the selected home directory, so a trial run against a
//     scratch home does not install into the real one through tools that
//     honor HOME.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// State describes a step as Prepare found it.
type State string

const (
	// Done means the step's Creates path exists or its Provides command is on
	// PATH, so it is not offered again.
	Done State = "done"
	// Pending means the step will be offered when setup runs.
	Pending State = "pending"
)

// Plan is the prepared state of one setup step.
type Plan struct {
	Step  config.Step
	State State
}

// Prepare checks that the repository is private and reports each step's
// state. repo and home must be absolute with symlinks resolved. Deciding a
// step's state never runs anything: it only looks for files and searches
// PATH.
//
// With no steps, Prepare returns nothing and skips the checks. They protect
// what a step runs, so a repository without setup steps for this platform,
// or without a manifest at all, must not fail setup over files it never uses.
func Prepare(steps []config.Step, repo, home string) ([]Plan, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	if err := trust.CheckRepository(repo); err != nil {
		return nil, err
	}
	if err := trust.CheckTree(repo); err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(steps))
	for _, step := range steps {
		state, err := stepState(step, home)
		if err != nil {
			return nil, err
		}
		plans = append(plans, Plan{Step: step, State: state})
	}
	return plans, nil
}

func stepState(step config.Step, home string) (State, error) {
	if step.Creates != "" {
		_, err := os.Lstat(filepath.Join(home, step.Creates))
		if err == nil {
			return Done, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	if step.Provides != "" {
		if _, err := exec.LookPath(step.Provides); err == nil {
			return Done, nil
		}
	}
	return Pending, nil
}

// Options configures Run.
type Options struct {
	Repo string // working directory for every step
	Home string // HOME for every step
	// Yes approves every pending step without asking. It is for unattended
	// bootstrap, such as a Dockerfile, where whoever wrote the build already
	// chose the repository and its revision.
	Yes    bool
	Input  io.Reader // answers to prompts, then each step's stdin
	Output io.Writer // descriptions, prompts, results, and each step's stdout
	Errors io.Writer // each step's stderr
}

// Run offers each pending step in order and runs the approved ones. A
// declined step is reported as skipped and the rest are still offered. A
// failing step stops Run, because later steps may depend on it.
func Run(ctx context.Context, plans []Plan, opts Options) error {
	for _, plan := range plans {
		if plan.State != Pending {
			continue
		}
		if err := Describe(opts.Output, plan, opts.Repo); err != nil {
			return err
		}
		if !opts.Yes {
			approved, err := confirm(opts.Input, opts.Output)
			if err != nil {
				return err
			}
			if !approved {
				if _, err := fmt.Fprintf(opts.Output, "skipped\t%s\n", plan.Step.Name); err != nil {
					return err
				}
				continue
			}
		}
		if err := execute(ctx, plan.Step, opts); err != nil {
			return fmt.Errorf("setup step %s: %w", plan.Step.Name, err)
		}
		if _, err := fmt.Fprintf(opts.Output, "ran\t%s\n", plan.Step.Name); err != nil {
			return err
		}
	}
	return nil
}

// Describe writes a step's state and exactly what it would run. Run uses it
// before each prompt, and dry runs use it to list every step.
func Describe(w io.Writer, plan Plan, dir string) error {
	_, err := fmt.Fprintf(w, "%s\t%s\n  run: %s\n  in:  %s\n", plan.State, plan.Step.Name, Quote(plan.Step.Run), dir)
	return err
}

func execute(ctx context.Context, step config.Step, opts Options) error {
	// A relative command path such as ./setup/fonts.sh resolves against Dir,
	// so repository scripts work without a shell. Bare names are searched on
	// PATH; since Go 1.19, a match in the current directory is refused.
	cmd := exec.CommandContext(ctx, step.Run[0], step.Run[1:]...) //nolint:gosec // G204: running the step the user just reviewed and approved is the purpose
	cmd.Dir = opts.Repo
	// Later entries win, so these replace any inherited values.
	cmd.Env = append(os.Environ(),
		"HOME="+opts.Home,
		"HOMESKILLET_HOME="+opts.Home,
		"HOMESKILLET_REPO="+opts.Repo,
	)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Input, opts.Output, opts.Errors
	return cmd.Run()
}

// confirm asks whether to run the step just described. Only "y" or "yes"
// approves. Anything else, including end of input, declines, so a closed or
// redirected stdin can never approve a step by accident. It reads one byte at
// a time so it never consumes input that belongs to the step itself.
func confirm(input io.Reader, output io.Writer) (bool, error) {
	if _, err := fmt.Fprint(output, "Run this step? [y/N] "); err != nil {
		return false, err
	}
	if input == nil {
		return false, nil
	}
	var line []byte
	buffer := make([]byte, 1)
	ended := false
	for {
		n, err := input.Read(buffer)
		if n == 1 {
			if buffer[0] == '\n' {
				break
			}
			line = append(line, buffer[0])
		}
		if errors.Is(err, io.EOF) {
			ended = true
			break
		}
		if err != nil {
			return false, err
		}
	}
	// A terminal echoes the user's Enter, but nothing echoes a newline for a
	// pipe, a file, or input that ended (Ctrl-D, /dev/null). End the prompt
	// line in those cases so the next output starts on its own line.
	if ended || !isTerminal(input) {
		if _, err := fmt.Fprintln(output); err != nil {
			return false, err
		}
	}
	answer := strings.ToLower(strings.TrimSpace(string(line)))
	return answer == "y" || answer == "yes", nil
}

func isTerminal(r io.Reader) bool {
	file, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Quote renders an argument vector as a POSIX shell would need it typed, so
// the text a user reviews is unambiguous: an argument containing spaces or
// quotes cannot pass for several arguments.
func Quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		if arg != "" && strings.Trim(arg, shellSafe) == "" {
			parts[i] = arg
			continue
		}
		parts[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

// shellSafe lists characters that need no quoting in a POSIX shell word.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-"
