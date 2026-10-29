//go:build linux || darwin || windows

// Package setup runs the setup steps a manifest declares, such as installing a
// prompt, a font, or a language toolchain, after the user has reviewed and
// approved each one. It knows nothing about any particular tool.
//
// A setup step is code from the configuration repository, which may be
// someone else's. Its safety rests on review, not on sandboxing:
//
//   - The whole repository must pass trust.CheckTree, so nobody else can have
//     changed what the user reviews, or a script that a step runs, and no
//     symlink can lead a step to code outside the repository.
//   - Each step is shown exactly as it will execute, with its argument vector
//     and working directory, before it runs, and the default answer is no.
//   - Steps run as argument vectors, never through an implicit shell, so
//     shell metacharacters in an argument are inert unless the step itself
//     invokes a shell for everyone to see.
//   - HOME is set to the selected home directory, and on Windows USERPROFILE
//     as well, so a trial run against a scratch home does not install into
//     the real one through tools that honor them. The directories where
//     installers put programs, such as that home's .local/bin, are appended
//     to PATH, so one step can use a program an earlier step installed (see
//     searchPath).
//   - Steps approved together with --yes get no input, and on Linux and
//     macOS cannot reach the terminal, so sudo in one cannot use a password
//     cached there (see runDetached).
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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// State describes a step as Prepare found it.
type State string

const (
	// Done means the step's Creates path exists or its Provides command is on
	// PATH, and for a step with inputs, that it last ran with them as they
	// are (see fingerprint), so it is not offered again.
	Done State = "done"
	// Pending means the step will be offered when setup runs.
	Pending State = "pending"
)

// Plan is the prepared state of one setup step.
type Plan struct {
	Step  config.Step
	State State
	// Found is, for a Done step, the file that makes it done: its Creates
	// path, or the program its Provides names. Output shows it, so a step
	// that is not offered still says why.
	Found string
	// Ran is, for a Done step with inputs, when it last ran with them as they
	// are now, which output shows when there is nothing to show as Found.
	Ran time.Time
	// fingerprint is, for a step with inputs, its fingerprint as prepared,
	// recorded once the step succeeds.
	fingerprint string
}

// Prepare checks that the repository is private and reports each step's
// state. repo and home must be absolute with symlinks resolved, and data is
// the data directory holding the record of steps with inputs (see
// fingerprint), which may be empty only when no step has inputs. Deciding a
// step's state never runs anything: it only looks for files, searches PATH,
// and reads inputs and the record.
//
// With no steps, Prepare returns nothing and skips the checks. They protect
// what a step runs, so a repository without setup steps for this platform,
// or without a manifest at all, must not fail setup over files it never uses.
func Prepare(steps []config.Step, repo, home, data string) ([]Plan, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	for _, dir := range []string{repo, home} {
		if err := trust.CheckLocation(dir); err != nil {
			return nil, err
		}
	}
	if err := trust.CheckRepository(repo); err != nil {
		return nil, err
	}
	if err := trust.CheckTree(repo); err != nil {
		return nil, err
	}
	// Steps run with HOME set to home and are skipped when their creates
	// path exists there, so a home that others could write to would let
	// them plant a file that makes a step look done, or redirect what a
	// step installs; link requires the same of home.
	if err := trust.CheckDirectory(home); err != nil {
		return nil, err
	}
	rec, err := recordFor(data, slices.ContainsFunc(steps, hasInputs))
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(steps))
	for i := range steps {
		plan, err := stepState(steps[i], repo, home, rec)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// hasInputs reports whether step has inputs, which the record decides.
func hasInputs(step config.Step) bool {
	return step.Inputs != nil
}

// stepState returns step's plan: whether it is done, and if so what shows
// it. A step with inputs is done only when the record, rec, says it last ran
// with the fingerprint it has now, and its Creates or Provides, when it has
// them, also say so.
func stepState(step config.Step, repo, home string, rec record) (Plan, error) {
	plan := Plan{Step: step, State: Pending}
	done, found, err := present(step, home)
	if err != nil {
		return Plan{}, err
	}
	if !hasInputs(step) {
		if done {
			plan.State, plan.Found = Done, found
		}
		return plan, nil
	}
	if plan.fingerprint, err = fingerprint(step, repo); err != nil {
		return Plan{}, fmt.Errorf("setup step %s: %w", step.Name, err)
	}
	last, ok := rec.last(home, repo, step.Name)
	if ok && last.Fingerprint == plan.fingerprint && (done || step.Creates == "" && step.Provides == "") {
		plan.State, plan.Found, plan.Ran = Done, found, last.Time
	}
	return plan, nil
}

// present reports whether step's Creates path exists or its Provides command
// is found, and which.
func present(step config.Step, home string) (bool, string, error) {
	if step.Creates != "" {
		path, err := createsPath(step.Creates, home)
		if err != nil {
			return false, "", fmt.Errorf("setup step %s: %w", step.Name, err)
		}
		_, err = os.Lstat(path)
		if err == nil {
			return true, path, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return false, "", err
		}
	}
	if step.Provides != "" {
		if program := provided(step.Provides, home); program != "" {
			return true, program, nil
		}
	}
	return false, "", nil
}

// createsPath returns the file a step's creates names: a path in home, or,
// after config.DocumentsPrefix, a path in the user's Documents folder (see
// documents).
func createsPath(creates, home string) (string, error) {
	rest, ok := strings.CutPrefix(creates, config.DocumentsPrefix)
	if !ok {
		return filepath.Join(home, creates), nil
	}
	dir, err := documents(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(rest)), nil
}

// Options configures Run. Repo and Home must be set, as Prepare received
// them. A nil Input answers no to every prompt and gives steps no input;
// nil Output and Errors discard what would be written there.
type Options struct {
	Repo string // working directory for every step
	Home string // HOME for every step
	// Data is the data directory, as Prepare received it, where a step with
	// inputs is recorded once it succeeds.
	Data string
	// Again offers every step, even one found done, as for steps a person
	// named: a package uninstalled by hand leaves its list unchanged, and
	// naming the step is how to install it again.
	Again bool
	// Yes approves every pending step without asking. It is for unattended
	// bootstrap, such as a Dockerfile, where whoever wrote the build already
	// chose the repository and its revision. Steps then get no input and no
	// terminal (see runDetached).
	Yes    bool
	Input  io.Reader // answers to prompts, then each step's stdin, unless Yes
	Output io.Writer // descriptions, prompts, results, and each step's stdout
	Errors io.Writer // each step's stderr
	// Report receives each step's outcome: Done for a step not offered
	// because its work exists, Pending just before a step is shown and
	// offered, "ran", or "skipped" for one declined. A nil Report writes one
	// tab-separated line per outcome to Output, the form scripts parse; the
	// CLI's terminal view supplies its own.
	Report func(outcome string, plan Plan) error
	// builtin marks steps homeskillet itself defines, run by RunBuiltin:
	// there is no repository to check or run them in, so they run in Home.
	builtin bool
}

// report writes outcome for plan the way scripts parse it, one tab-separated
// line: the outcome, the step's name, and for a Done step what makes it done.
func report(w io.Writer, outcome string, plan Plan) error {
	if w == nil {
		return nil
	}
	if outcome == string(Done) {
		return List(w, plan)
	}
	_, err := fmt.Fprintf(w, "%s\t%s\n", outcome, plan.Step.Name)
	return err
}

// Run offers each pending step in order and runs the approved ones. A
// declined step is reported as skipped and the rest are still offered. A
// failing step stops Run, because later steps may depend on it.
func Run(ctx context.Context, plans []Plan, opts Options) error {
	// An empty Repo would run steps in the current directory, and an empty
	// Home would set HOME to nothing, so neither may default.
	for _, dir := range []string{opts.Repo, opts.Home} {
		if err := trust.CheckLocation(dir); err != nil {
			return err
		}
	}
	opts.builtin = false
	return run(ctx, plans, opts)
}

// PrepareBuiltin reports the state of steps that homeskillet itself defines,
// such as installing Git with winget for homeskillet prepare, rather than a
// repository. Their commands are part of homeskillet, so the repository
// rules do not apply; the home must still be private, as for Prepare, since
// the steps run with HOME set to it and a Creates path found there would
// mark one done. Built-in steps have no inputs, which need a repository.
func PrepareBuiltin(steps []config.Step, home string) ([]Plan, error) {
	if err := trust.CheckLocation(home); err != nil {
		return nil, err
	}
	if err := trust.CheckDirectory(home); err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(steps))
	for i := range steps {
		// A repository's steps are validated when the manifest is read;
		// these are checked here, so an empty command can never reach
		// execute, which runs a command's first word.
		empty := len(steps[i].Run) == 0 || slices.ContainsFunc(steps[i].Run, func(command []string) bool { return len(command) == 0 || command[0] == "" })
		if hasInputs(steps[i]) || empty {
			return nil, fmt.Errorf("built-in step %s must have commands, none empty, and no inputs", steps[i].Name)
		}
		plan, err := stepState(steps[i], "", home, record{})
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// RunBuiltin offers and runs steps PrepareBuiltin prepared exactly as Run
// does a repository's: each pending one is shown as it will run and runs
// only once approved, with no as the default answer, or under Yes. They run
// in Home, which opts.Repo must leave empty, since there is no repository.
func RunBuiltin(ctx context.Context, plans []Plan, opts Options) error {
	if opts.Repo != "" {
		return errors.New("built-in steps run in the home, not a repository")
	}
	if err := trust.CheckLocation(opts.Home); err != nil {
		return err
	}
	opts.builtin = true
	return run(ctx, plans, opts)
}

// dir returns where opts runs steps: the repository, or for built-in steps
// the home.
func (opts Options) dir() string {
	if opts.builtin {
		return opts.Home
	}
	return opts.Repo
}

// StepError is a step that failed when it ran, so a caller can say what to
// do next without reading the message: the step's own output, just above,
// says why it failed.
type StepError struct {
	Step string
	// Builtin marks a step homeskillet defines (see RunBuiltin).
	Builtin bool
	Err     error
}

func (e *StepError) Error() string {
	label := "setup step"
	if e.Builtin {
		label = "step"
	}
	return label + " " + e.Step + ": " + e.Err.Error()
}

func (e *StepError) Unwrap() error { return e.Err }

// ErrNotFound is the error for a program a step names that is not on PATH.
var ErrNotFound = errors.New("not found on PATH")

// run is Run and RunBuiltin once their options are checked.
func run(ctx context.Context, plans []Plan, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Errors == nil {
		opts.Errors = io.Discard
	}
	if opts.Report == nil {
		opts.Report = func(outcome string, plan Plan) error { return report(opts.Output, outcome, plan) }
	}
	rec, err := recordFor(opts.Data, slices.ContainsFunc(plans, func(plan Plan) bool { return hasInputs(plan.Step) }))
	if err != nil {
		return err
	}
	for i := range plans {
		plan := &plans[i]
		// Every step is accounted for, so one that is not offered still
		// says why rather than vanishing from the output.
		if plan.State == Done && !opts.Again {
			if err := opts.Report(string(Done), *plan); err != nil {
				return err
			}
			continue
		}
		// An earlier step may have done this one's work, as an installer
		// that also brings the program a later step provides, or changed
		// the repository, whose rules every step relies on. Both are checked
		// again before each step rather than once for the whole run.
		current, err := stepState(plan.Step, opts.Repo, opts.Home, rec)
		if err != nil {
			return err
		}
		if current.State == Done && !opts.Again {
			if err := opts.Report(string(Done), current); err != nil {
				return err
			}
			continue
		}
		var checked error
		if !opts.builtin {
			checked = trust.CheckTree(opts.Repo)
		}
		if err := errors.Join(checked, trust.CheckDirectory(opts.Home)); err != nil {
			return fmt.Errorf("before setup step %s: %w", plan.Step.Name, err)
		}
		if err := opts.Report(string(Pending), *plan); err != nil {
			return err
		}
		if err := Describe(opts.Output, *plan, opts.dir()); err != nil {
			return err
		}
		if !opts.Yes {
			approved, err := confirm(opts.Input, opts.Output)
			if err != nil {
				return err
			}
			if !approved {
				if err := opts.Report("skipped", *plan); err != nil {
					return err
				}
				continue
			}
		}
		if err := execute(ctx, plan.Step, opts); err != nil {
			return &StepError{Step: plan.Step.Name, Builtin: opts.builtin, Err: err}
		}
		// The fingerprint taken before the step ran, of what was approved:
		// inputs changed meanwhile offer it again next time.
		if hasInputs(plan.Step) {
			if rec, err = recordRan(opts.Data, opts.Home, opts.Repo, plan.Step.Name, current.fingerprint); err != nil {
				return fmt.Errorf("setup step %s ran, but recording it failed, so it will be offered again: %w", plan.Step.Name, err)
			}
		}
		if err := opts.Report("ran", *plan); err != nil {
			return err
		}
	}
	return nil
}

// Describe writes exactly what a step will run, every command and the working
// directory, for the review before the prompt: what is shown is what runs.
// Run reports the step's name first (see Options.Report).
func Describe(w io.Writer, plan Plan, dir string) error {
	if w == nil {
		return nil
	}
	// Built whole, then written once, so the one write error is the only one.
	var text strings.Builder
	for _, command := range plan.Step.Run {
		fmt.Fprintf(&text, "  run: %s\n", Quote(command))
	}
	fmt.Fprintf(&text, "  in:  %s\n", printable(dir))
	_, err := io.WriteString(w, text.String())
	return err
}

// List writes one tab-separated line for plan, the form scripts parse: the
// state, the name, and what Detail says. Run uses it for steps it does not
// offer, and dry runs for every step when output is not a terminal.
func List(w io.Writer, plan Plan) error {
	if w == nil {
		return nil
	}
	_, err := fmt.Fprintf(w, "%s\t%s\t%s\n", plan.State, plan.Step.Name, Detail(plan))
	return err
}

// Detail returns what to show beside a step in a listing: for a Done step the
// file or program that makes it done, or for a step with inputs alone when it
// last ran with them as they are, and for a Pending step its commands, in
// shell quoting, separated by "; " when there are several. The prompt before
// a step runs shows them one per line instead (see Describe).
func Detail(plan Plan) string {
	if plan.State == Done && plan.Found == "" && !plan.Ran.IsZero() {
		return "inputs unchanged since it ran " + plan.Ran.Local().Format("2006-01-02 15:04")
	}
	if plan.State == Done {
		return printable(plan.Found)
	}
	commands := make([]string, len(plan.Step.Run))
	for i, command := range plan.Step.Run {
		commands[i] = Quote(command)
	}
	return strings.Join(commands, "; ")
}

// execute runs a step's commands in order, stopping at the first that fails,
// since a later one may rely on it.
func execute(ctx context.Context, step config.Step, opts Options) error {
	for i, command := range step.Run {
		if err := executeCommand(ctx, command, opts); err != nil {
			if len(step.Run) > 1 {
				return fmt.Errorf("command %d of %d, %s: %w", i+1, len(step.Run), Quote(command), err)
			}
			return err
		}
	}
	return nil
}

func executeCommand(ctx context.Context, command []string, opts Options) error {
	// A relative command path such as ./setup/fonts.sh resolves against Dir,
	// so repository scripts work without a shell. A bare name is searched on
	// the same PATH the step receives (see searchPath and lookPath),
	// computed again for each command, so one can use what the one before it
	// installed into .local/bin.
	path := searchPath(opts.Home)
	program, err := lookPath(command[0], path)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, program, command[1:]...) //nolint:gosec // G204: running the step the user just reviewed and approved is the purpose
	// argv[0] stays the name the user reviewed, not the resolved path.
	cmd.Args[0] = command[0]
	cmd.Dir = opts.dir()
	// Later entries win, so these replace any inherited values, on Windows
	// whatever their case, such as Path for PATH.
	cmd.Env = append(os.Environ(),
		"HOME="+opts.Home,
		"HOMESKILLET_HOME="+opts.Home,
		"PATH="+path,
	)
	if !opts.builtin {
		cmd.Env = append(cmd.Env, "HOMESKILLET_REPO="+opts.Repo)
	}
	// Windows programs find the profile through USERPROFILE; HOME, which
	// Windows does not set, is for Git and other programs from Unix.
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "USERPROFILE="+opts.Home)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Input, opts.Output, opts.Errors
	// Unattended, nobody can answer a step that prompts, so it reads end of
	// input (a nil Stdin is the null device) rather than waiting forever.
	if opts.Yes {
		cmd.Stdin = nil
		return runDetached(cmd)
	}
	return cmd.Run()
}

// confirm asks whether to run the step just described (see Confirm).
func confirm(input io.Reader, output io.Writer) (bool, error) {
	return Confirm(input, output, "Run this step?")
}

// Confirm asks question, followed by [y/N], and reads the answer from input.
// Only "y" or "yes" approves. Anything else, including end of input,
// declines, so a closed or redirected stdin can never approve by accident.
// It reads one byte at a time so it never consumes input that belongs to a
// step run after it.
func Confirm(input io.Reader, output io.Writer, question string) (bool, error) {
	const maxAnswer = 64
	if _, err := fmt.Fprint(output, question+" [y/N] "); err != nil {
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
			// Only "y" or "yes", with some space around it, approves, so
			// the rest of a long line is read but not kept: input without
			// a newline cannot exhaust memory.
			if len(line) < maxAnswer {
				line = append(line, buffer[0])
			}
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

// printable returns path for display, quoted when it holds control or
// formatting characters, so a path cannot rewrite the terminal, as the CLI's
// view does for its own paths.
func printable(path string) string {
	if !config.Printable(path) {
		return strconv.Quote(path)
	}
	return path
}

// shellSafe lists characters that need no quoting in a POSIX shell word.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-"
