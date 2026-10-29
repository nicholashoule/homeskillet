//go:build linux || darwin || windows

// Package cli implements homeskillet's command line: argument parsing,
// process-wide policy such as refusing to run as root, and printed output. It
// delegates reading the repository's declarations to package config, linking
// to package link, setup steps to package setup, and managing installed
// versions of homeskillet itself to package install.
package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/setup"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
)

var (
	commands = []string{"status", "link", "unlink", "setup", "init", "check", "prepare", "wsl", "backups",
		"update", "install", "use", "list", "list-remote", "self", "version", "completion", "help"}
	// selfAliases maps the self commands that are also commands of their own
	// to the self command each runs: homeskillet update is homeskillet self
	// update, and homeskillet list-remote is homeskillet self releases, named
	// as version managers name it.
	selfAliases = map[string]string{"update": "update", "install": "install", "use": "use", "list": "list", "list-remote": "releases"}
	// shortNames maps a command's short form to the command, which it runs
	// in every way; help and errors list only the full name.
	shortNames      = map[string]string{"prep": "prepare"}
	selfCommands    = []string{"install", "update", "releases", "use", "list", "remove"}
	backupsCommands = []string{"list", "purge"}
	// scoped lists the commands that accept each flag that only some
	// commands use. Accepting such a flag silently elsewhere would suggest
	// it changes something there, and --yes in particular approves running
	// code.
	scoped = map[string][]string{
		"repo": {"status", "link", "unlink", "setup", "init", "check"},
		// init writes only into the repository.
		"home": {"status", "link", "unlink", "setup", "prepare", "self", "backups"},
		// --yes approves running steps, prepare's installs, or deleting
		// backups, without asking.
		"yes": {"setup", "prepare", "backups"},
		// self installs the running build and version prints its own
		// details, so neither acts for another platform. init writes it
		// into the starter manifest.
		"platform": {"status", "link", "unlink", "setup", "init"},
		"prefix":   {"self"},
		// version keeps nothing in the data directory; setup keeps the record
		// of steps with inputs there.
		"data-dir": {"status", "link", "unlink", "setup", "self", "backups"},
		// Only link replaces anything, and only by moving it aside.
		"force":      {"link"},
		"older-than": {"backups"},
		// Only wsl reaches into a WSL distribution.
		"distribution": {"wsl"},
		"from":         {"wsl"},
		// setup must show every step exactly before it is approved, so it
		// cannot be quiet.
		"quiet": {"status", "link", "unlink", "self", "init", "check"},
	}
	// selecting lists the commands that take files after the repository,
	// acting on those alone.
	selecting = []string{"status", "link", "unlink"}
)

// invocation is a parsed command line.
type invocation struct {
	command string // one of commands
	sub     string // for self, one of selfCommands
	operand string // for self use, self update, self install, and self remove, the version
	repo    string
	// repoGiven says the repository was named rather than defaulted (see
	// openRepository).
	repoGiven bool
	// files selects entries for status, link, and unlink: targets in home,
	// sources in the repository, or directories holding either. Empty
	// means every entry.
	files []string
	// steps selects setup steps by name. Empty means every step.
	steps []string
	// words is the command line a completion script asks about (see
	// complete).
	words  []string
	force  bool // --force: move conflicts aside, then link
	quiet  bool // --quiet: print nothing but errors
	home   string
	prefix string
	data   string // --data-dir
	dryRun bool
	// verbose is --verbose: a failure is shown whole (see failure).
	verbose bool
	root    bool // --allow-root
	yes     bool
	// platform is --platform, the platform to act for instead of the
	// detected one.
	platform string
	// olderThan is --older-than, which limits backups to those moved longer
	// ago (see parseAge).
	olderThan string
	// wsl holds what only the wsl command reads (see parseWSL).
	wsl *wslOptions
}

// wslOptions are the wsl command's own: the distribution (--distribution,
// default WSL's own), the Linux build to install (--from), and the
// arguments passed on to homeskillet in the distribution after the
// repository.
type wslOptions struct {
	distribution string
	from         string
	forward      []string
}

// Run executes one homeskillet command. args excludes the program name. input
// answers setup prompts and is passed to setup steps. Results and the help
// text go to output, and setup steps' stderr to diagnostics. A nil
// ctx means context.Background, a nil input answers no to every prompt, and
// nil output or diagnostics discard what would be written there.
func Run(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) error {
	verbose := false
	err := run(ctx, args, input, output, diagnostics, &verbose)
	if err == nil || Reported(err) {
		return err
	}
	return failure{err: err, verbose: verbose}
}

// run is Run before its error is presented (see failure); it sets verbose
// once the command line says.
func run(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer, verbose *bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == nil {
		output = io.Discard
	}
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	inv, err := parse(args, output)
	if err != nil || inv.command == "" {
		return err
	}
	*verbose = inv.verbose
	// Errors still reach main, which prints them; only results are dropped.
	if inv.quiet {
		output = io.Discard
	}
	switch inv.command {
	case completeCommand:
		return complete(inv.words, output)
	case "completion":
		return writeCompletion(inv.operand, output)
	case "version":
		return writeVersion(output)
	}
	// Most sudo configurations reset HOME to /root, so "sudo homeskillet link"
	// would point root's shell startup files at files the invoking user owns,
	// letting that account run code as root, and "sudo homeskillet self
	// install" would put a binary the user owns on root's PATH. The checks in
	// package trust already reject both; refusing root outright also makes
	// managing root's own dotfiles, as in a container, a deliberate choice.
	// On Windows, an elevated process is refused the same way: it may change
	// what every user runs, and the checks cannot tell it from the same user
	// unelevated, since both have one identity.
	if privileged() && !inv.root {
		return errors.New(rootRefusal)
	}
	if handled, err := runApart(ctx, inv, input, output, diagnostics); handled {
		return err
	}
	homePath := inv.home
	if homePath == "" {
		homePath, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	home, err := canonicalDirectory(homePath)
	if err != nil {
		return fmt.Errorf("home: %w", err)
	}
	// Checked here, before anything reads or writes there, as well as in
	// each package, so that HOME=/ in a container fails with a clear error.
	if err := trust.CheckLocation(home); err != nil {
		return fmt.Errorf("home: %w", err)
	}
	// The check for a newer release runs alongside the command. Its note, if
	// any, follows the output of a command that succeeded; after one that
	// failed, it is only let finish, so its answer is kept for next time.
	check := startUpdateCheck(ctx, inv, home, output)
	if err := runWithHome(ctx, inv, home, input, output, diagnostics); err != nil {
		check.wait()
		return err
	}
	return check.print(output)
}

// runWithHome runs inv against home, which has passed the location checks:
// the commands on the home's own files (see runInHome), or else the ones on
// a repository and the home.
func runWithHome(ctx context.Context, inv invocation, home string, input io.Reader, output, diagnostics io.Writer) error {
	if handled, err := runInHome(ctx, inv, home, input, output, diagnostics); handled {
		return err
	}
	repo, manifest, remembered, err := openRepository(inv, home)
	if err != nil {
		return err
	}
	platform, err := choosePlatform(inv)
	if err != nil {
		return err
	}
	files, steps := manifest.FilesFor(platform), manifest.SetupFor(platform)
	if err := leftOut(inv.command, manifest, len(files)+len(steps), repo, platform); err != nil {
		return err
	}
	// A person at a terminal sees first what is running and where; scripts
	// get only the results (see terminal).
	v := newView(output)
	if v != nil {
		v.copyOf = describeCopy(inv, home, repo)
		if err := v.header(output, inv.command, repo, remembered, home, platform, inv.dryRun); err != nil {
			return err
		}
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return err
	}
	if inv.command == "setup" {
		if steps, err = selectSteps(inv.steps, steps, manifest.Setup, repo, platform); err != nil {
			return err
		}
		return runSetup(ctx, steps, repo, home, inv.dryRun, setup.Options{
			Repo: repo, Home: home, Data: data, Again: len(inv.steps) > 0, Yes: inv.yes, Input: input, Output: output, Errors: diagnostics,
		}, v)
	}
	// A person who has just linked a repository may not know it has setup
	// steps waiting, such as the one that makes their shell load what was
	// linked, so the view says what to run next.
	if v != nil && (inv.command == "link" || inv.command == "status") && len(steps) > 0 {
		v.next = nextSetup(steps, repo, home, data, remembered)
	}
	return runLinks(inv, files, repo, home, data, output, diagnostics, v)
}

// runApart runs the commands that do not act on a repository and a home
// here, as the rest do, and reports whether inv was one of them: init and
// check, which need only the repository, and wsl and the copy and record it
// sends, which work in a WSL distribution's home.
func runApart(ctx context.Context, inv invocation, input io.Reader, output, diagnostics io.Writer) (bool, error) {
	switch inv.command {
	case "init":
		return true, runInit(inv, output)
	case "check":
		return true, runCheck(inv, output)
	case "wsl":
		return true, runWSL(ctx, inv, input, output, diagnostics)
	case mirrorCommand:
		return true, runMirror(inv, input, output)
	case windowsBuildCommand:
		return true, runWindowsBuild(inv)
	}
	return false, nil
}

// runInHome runs the commands that act on the home's own files rather than
// a repository's, and reports whether inv was one of them: self, on installed
// versions, and backups, on what link --force moved aside.
func runInHome(ctx context.Context, inv invocation, home string, input io.Reader, output, diagnostics io.Writer) (bool, error) {
	switch inv.command {
	case "self":
		return true, runSelf(ctx, inv, home, output, diagnostics)
	case "backups":
		return true, runBackups(inv, home, input, output)
	case "prepare":
		return true, runPrepare(ctx, inv, home, input, output, diagnostics)
	}
	return false, nil
}

// leftOut returns an error when the repository has files or steps, yet none
// apply to platform, applying being how many do. Such a repository would otherwise
// do nothing and report success, as if everything were linked. unlink is not
// checked, so links made before the platforms changed can be removed, and a
// repository with nothing in it yet, as init leaves one, has nothing for its
// platforms to leave out.
func leftOut(command string, manifest config.Manifest, applying int, repo string, platform config.Platform) error {
	if command == "unlink" || applying > 0 || len(manifest.Platforms) == 0 || len(manifest.Files)+len(manifest.Setup) == 0 {
		return nil
	}
	return fmt.Errorf("nothing in %s applies to %s: its platforms are %s; pass --platform to act for another platform",
		repo, platform, strings.Join(manifest.Platforms, ", "))
}

// writeVersion writes the version and build details, for version.
func writeVersion(output io.Writer) error {
	info := version.Current()
	detected, translated := host.Detect()
	platform := fmt.Sprintf("  %-10s %s\n", "platform:", info.Platform)
	// The build's platform and the host's differ only when the binary runs
	// translated, as an amd64 build does under Rosetta 2 on Apple silicon;
	// then both are shown, with the build to install instead.
	if detected.String() != info.Platform {
		hostLine := detected.String()
		if translated {
			hostLine += " (translated by " + translator() + "; install a " + detected.String() + " build)"
		}
		platform = fmt.Sprintf("  %-10s %s\n  %-10s %s\n", "built for:", info.Platform, "host:", hostLine)
	}
	_, err := fmt.Fprintf(output, "homeskillet %s\n%s%s", info.Version, info.Details(), platform)
	return err
}

// choosePlatform returns the platform to act for: --platform if given,
// otherwise the detected one (see host.Detect). An override is allowed, since
// detection is best effort, as when an amd64 build runs under Rosetta on an
// Apple silicon Mac and the user wants amd64 steps. But setup steps for
// another operating system cannot work here and could do damage, so they may
// only be listed with --dry-run.
//
// Nothing is printed: scripts often read output and errors together, and the
// user asked for any override. version and self install report translation.
func choosePlatform(inv invocation) (config.Platform, error) {
	detected, _ := host.Detect()
	if inv.platform == "" {
		return detected, nil
	}
	platform, err := config.ParsePlatform(inv.platform)
	if err != nil {
		return config.Platform{}, fmt.Errorf("--platform: %w", err)
	}
	if platform.OS != detected.OS && inv.command == "setup" && !inv.dryRun {
		return config.Platform{}, fmt.Errorf("refusing to run %s setup steps on %s; add --dry-run to list them", platform.OS, detected.OS)
	}
	return platform, nil
}

// parse reads a command line. It returns an invocation with no command when
// help was requested, after writing the help text to output.
func parse(args []string, output io.Writer) (invocation, error) {
	if inv, hidden, err := parseHidden(args); hidden {
		return inv, err
	}
	// --version is the conventional spelling of the version command.
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		args = []string{"version"}
	}
	inv := invocation{}
	var err error
	if inv.command, args, err = takeCommand(args); err != nil {
		return invocation{}, err
	}
	switch inv.command {
	case "help":
		return parseHelp(args, output)
	case "wsl":
		return parseWSL(args, output)
	}
	if _, ok := subcommands[inv.command]; ok {
		inv.sub, args = word(args, "")
	}
	flags := newFlags(&inv)
	operands, err := parseFlags(flags, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return invocation{}, help(output, flags)
		}
		return invocation{}, fmt.Errorf("%w\n  run homeskillet --help to see the commands and options", err)
	}
	var misplaced, empty error
	repoFlag := false
	flags.Visit(func(f *flag.Flag) {
		repoFlag = repoFlag || f.Name == "repo"
		inv.repoGiven = inv.repoGiven || f.Name == "repo"
		if allowed, ok := scoped[f.Name]; ok && !slices.Contains(allowed, inv.command) && misplaced == nil {
			misplaced = fmt.Errorf("--%s applies only to %s", f.Name, strings.Join(allowed, ", "))
		}
		// An empty value, as from an unset variable in --home "$DIR", would
		// otherwise mean the default: the real home instead of a scratch one.
		boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
		if (!ok || !boolean.IsBoolFlag()) && f.Value.String() == "" && empty == nil {
			empty = fmt.Errorf("--%s is empty, as from an unset variable; give a value, or leave the option out", f.Name)
		}
	})
	// The same goes for arguments: an empty repository would mean the
	// current directory, and an empty file name every file, so
	// homeskillet unlink repo "$FILE" would unlink everything.
	if slices.Contains(operands, "") && empty == nil {
		empty = errors.New("an argument is empty, as from an unset variable; name a path, or leave it out")
	}
	// The first operand is the repository unless --repo named it; any after
	// it select files, for the commands that act on files, or steps, for
	// setup. After setup the repository must be plainly a path, which a
	// step name never is (see isPath), so setup dotnet runs that step of the
	// default repository.
	if slices.Contains(scoped["repo"], inv.command) && !repoFlag && len(operands) > 0 &&
		(inv.command != "setup" || isPath(operands[0])) {
		inv.repo, operands = operands[0], operands[1:]
		inv.repoGiven = true
	}
	if operands, err = takeSubcommand(&inv, operands); err != nil {
		return invocation{}, err
	}
	switch {
	case misplaced != nil:
		return invocation{}, misplaced
	case empty != nil:
		return invocation{}, empty
	case (slices.Contains([]string{"use", "remove", "update", "install"}, inv.sub) || inv.command == "completion") && len(operands) == 1:
		inv.operand = operands[0]
		if inv.command == "self" {
			inv.operand = withV(inv.operand)
		}
	case slices.Contains(selecting, inv.command):
		inv.files = operands
	case inv.command == "setup":
		inv.steps = operands
	case len(operands) != 0:
		return invocation{}, fmt.Errorf("unexpected arguments: %s", strings.Join(operands, " "))
	}
	switch {
	case (inv.command == "version" || inv.command == "completion") && flags.NFlag() != 0:
		return invocation{}, fmt.Errorf("%s takes no options", inv.command)
	case inv.command == "completion" && inv.operand == "":
		return invocation{}, fmt.Errorf("completion needs a shell: %s", strings.Join(slices.Sorted(maps.Keys(completionScripts)), ", "))
	case inv.command == "self" && inv.sub == "":
		return invocation{}, fmt.Errorf("self needs a command: %s", strings.Join(selfCommands, ", "))
	case (inv.sub == "use" || inv.sub == "remove") && inv.operand == "":
		return invocation{}, fmt.Errorf("self %s needs a version", inv.sub)
	}
	return inv, nil
}

// withV returns a version written without its leading v, as 0.2.0, with it,
// as self use, self update, self install, and self remove name versions, and
// anything else as it is.
func withV(v string) string {
	if !version.Valid(v) && version.Valid("v"+v) {
		return "v" + v
	}
	return v
}

// takeCommand takes the command from args, status when there is none, and
// returns it with the arguments left. A path in place of a command means
// status for that repository: only a word that is plainly a path counts (see
// isPath), so a mistyped command is reported rather than read as a
// directory, and status never changes anything either way. A self alias,
// such as update, becomes self with the alias as its command.
func takeCommand(args []string) (string, []string, error) {
	command, rest := word(args, "status")
	if full, ok := shortNames[command]; ok {
		command = full
	}
	if sub, ok := selfAliases[command]; ok {
		return "self", append([]string{sub}, rest...), nil
	}
	switch {
	case slices.Contains(commands, command):
		return command, rest, nil
	case isPath(command):
		return "status", args, nil
	}
	return "", nil, unknownCommand(command)
}

// unknownCommand says that word is not a command, briefly: the nearest
// command when word is likely a typo of it, or else where the commands are
// listed. A bare word is never read as a repository (see isPath), so when a
// directory by that name is here, the error also says how to name it.
func unknownCommand(word string) error {
	shown := word
	if !config.Printable(shown) {
		shown = strconv.Quote(shown)
	}
	message := fmt.Sprintf("'%s' is not a command; see 'homeskillet help'", shown)
	if near := nearestCommand(word); near != "" {
		message = fmt.Sprintf("'%s' is not a command; did you mean '%s'?", shown, near)
	}
	if info, err := os.Stat(word); err == nil && info.IsDir() && config.Printable(word) { //nolint:gosec // G703: only whether a directory by the typed name exists, to word the error; nothing is read or acted on
		message += fmt.Sprintf("\n  for the repository in %s, write it as a path: ./%s", word, word)
	}
	return errors.New(message)
}

// nearestCommand returns the command word is most likely a typo of: the one
// at the smallest edit distance, if that is at most two and less than half
// the word, so that a short or unrelated word suggests nothing. Ties go to
// the command listed first.
func nearestCommand(word string) string {
	best, bestDistance := "", 3
	for _, command := range commands {
		if d := editDistance(strings.ToLower(word), command); d < bestDistance && 2*d < len(word) {
			best, bestDistance = command, d
		}
	}
	return best
}

// editDistance returns the Levenshtein distance between a and b, by bytes,
// which is enough for command names.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

// subcommands lists, for each command that takes a command of its own after
// it, the ones it takes.
var subcommands = map[string][]string{"self": selfCommands, "backups": backupsCommands}

// takeSubcommand takes the command after self or backups from operands when
// options came first, as in self --dry-run list, and checks it.
func takeSubcommand(inv *invocation, operands []string) ([]string, error) {
	allowed, ok := subcommands[inv.command]
	if !ok {
		return operands, nil
	}
	if inv.sub == "" && len(operands) > 0 {
		inv.sub, operands = operands[0], operands[1:]
	}
	if inv.sub != "" && !slices.Contains(allowed, inv.sub) {
		return nil, fmt.Errorf("unknown command %s %s; use %s %s", inv.command, inv.sub, inv.command, strings.Join(allowed, ", "+inv.command+" "))
	}
	return operands, nil
}

// parseHidden parses the lines of the hidden commands, which are not
// command lines with options, and reports whether args is one.
func parseHidden(args []string) (invocation, bool, error) {
	if len(args) == 0 {
		return invocation{}, false, nil
	}
	switch args[0] {
	// Completion scripts pass the line being completed as it stands, options
	// and all, so it is never parsed as a command line here.
	case completeCommand:
		return invocation{command: completeCommand, words: args[1:]}, true, nil
	case mirrorCommand:
		inv, err := parseMirror(args[1:])
		return inv, true, err
	case windowsBuildCommand:
		inv, err := parseWindowsBuild(args[1:])
		return inv, true, err
	}
	return invocation{}, false, nil
}

// parseHelp writes the help text for help. It covers every command, so a
// command named after help gets the same text.
func parseHelp(args []string, output io.Writer) (invocation, error) {
	if len(args) > 1 || len(args) == 1 && !slices.Contains(commands, args[0]) && args[0] != "--help" && args[0] != "-h" {
		return invocation{}, fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
	}
	return invocation{}, help(output, newFlags(new(invocation)))
}

// newFlags returns the options, set to write into inv. Their usage strings
// are the descriptions help shows, where a word in backquotes names the value.
func newFlags(inv *invocation) *flag.FlagSet {
	flags := flag.NewFlagSet("homeskillet", flag.ContinueOnError)
	// help prints the help text, and an error reaches main, which prints it,
	// so the flag package writes nothing itself.
	flags.SetOutput(io.Discard)
	flags.BoolVar(&inv.dryRun, "dry-run", false, "Preview: change no files and run no setup steps")
	flags.BoolVar(&inv.verbose, "verbose", false, "On failure, show the whole error, not only its first line")
	flags.StringVar(&inv.home, "home", "", "Use `DIR` as the home directory (default: yours)")
	flags.StringVar(&inv.repo, "repo", ".", "Use `DIR` as the repository, as if named after the command")
	flags.StringVar(&inv.platform, "platform", "", "Act for `OS[/ARCH]`, such as darwin or linux/arm64 (default: this machine)")
	flags.BoolVar(&inv.quiet, "quiet", false, "Print only errors; the exit status reports the result (not for setup)")
	flags.StringVar(&inv.data, "data-dir", "", "Keep the link record, backups, and installed versions in `DIR`, relative to the home unless absolute "+
		"(default: $"+dataDirVariable+", else .local/share/homeskillet)")
	flags.BoolVar(&inv.root, "allow-root", false, "Allow running as root, or elevated on Windows, for root's own dotfiles; root must own everything homeskillet reads")
	flags.BoolVar(&inv.force, "force", false, "Move conflicting files into a backup, then link")
	flags.BoolVar(&inv.yes, "yes", false, "Run every pending step, or for backups purge delete every backup listed, without asking")
	flags.StringVar(&inv.olderThan, "older-than", "", "Only backups moved more than `AGE` ago, in days or hours, such as 30d or 12h")
	flags.StringVar(&inv.prefix, "prefix", "", "Install into `DIR`/bin (default: .local in the home directory)")
	if inv.wsl == nil {
		inv.wsl = new(wslOptions)
	}
	flags.StringVar(&inv.wsl.distribution, "distribution", "", "Act in the WSL distribution `NAME` (default: WSL's default)")
	flags.StringVar(&inv.wsl.from, "from", "", "For wsl install, install the Linux build in `FILE`, as make cross builds, instead of downloading this version's release")
	return flags
}

// word returns the first argument and the rest when the first is not an
// option, and fallback with all arguments otherwise.
func word(args []string, fallback string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return fallback, args
}

// isPath reports whether a word in place of a command is plainly a path: . or
// .., or anything containing a slash, and on Windows a backslash or a drive
// too, as in .\dotfiles or C:dotfiles. It looks only at the text, so whether a
// word is read as a command never depends on what the current directory holds.
func isPath(word string) bool {
	return word == "." || word == ".." || strings.ContainsRune(word, '/') ||
		runtime.GOOS == "windows" && strings.ContainsAny(word, `\:`)
}

// homeRelative returns the rest of name after ~ when name is ~ or starts
// with ~ and a separator, as ~/.bashrc does, or ~\.bashrc on Windows; ok
// reports whether it does. Shells expand ~ only at the start of a word, so
// homeskillet expands it where they leave it alone, as after --data-dir=.
func homeRelative(name string) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(name, "~")
	return rest, ok && (rest == "" || os.IsPathSeparator(rest[0]))
}

// parseFlags parses args with flags, accepting options both before and after
// operands, which the flag package alone stops parsing at, and returns the
// operands in order. Everything after a "--" terminator is an operand.
func parseFlags(flags *flag.FlagSet, args []string) ([]string, error) {
	var operands []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		rest := flags.Args()
		// A "--" that was an option's value is also read as the
		// terminator here. That only turns later options into operands,
		// which are then rejected as unexpected, never silently dropped.
		parsed := args[:len(args)-len(rest)]
		if len(rest) == 0 || len(parsed) > 0 && parsed[len(parsed)-1] == "--" {
			return append(operands, rest...), nil
		}
		operands = append(operands, rest[0])
		args = rest[1:]
	}
}

// openRepository returns the repository to act on, canonical, and its
// manifest: the one named, or else the current directory. When none is named
// and the current directory is not a repository, it uses the one repository
// the record says is linked into home (see link.Repositories), so that
// homeskillet link works from any directory after the first link; remembered
// reports that. With several recorded, it lists them rather than choose.
//
// A named directory is never replaced by a recorded one. But naming the home
// itself is an easy slip, as if the argument chose where to link, so that
// error also names the repositories linked into it.
//
// An unnamed current directory that homeskillet refuses to act on (see
// trust.CheckLocation) is no more the repository than one with nothing in
// it, so it falls back the same way. Windows programs run from a WSL shell
// start in the distribution's files, so homeskillet.exe there still finds
// the Windows repository.
func openRepository(inv invocation, home string) (repo string, manifest config.Manifest, remembered bool, err error) {
	repo, manifest, err = loadRepository(inv.repo)
	var refused refusedLocation
	unusable := errors.Is(err, config.ErrNothingToManage) || !inv.repoGiven && errors.As(err, &refused)
	if err == nil || !unusable || inv.repoGiven && repo != home {
		return repo, manifest, false, err
	}
	var recorded []string
	if data, dataErr := dataDir(inv, home); dataErr == nil {
		recorded, _ = link.Repositories(data, home)
	}
	if inv.repoGiven {
		if len(recorded) == 0 {
			return "", config.Manifest{}, false, fmt.Errorf("%w\n  that is the home directory; name a repository instead: homeskillet %s /path/to/dotfiles", err, inv.command)
		}
		var choices strings.Builder
		choices.WriteString("\n  that is the home directory; name the repository linked into it instead:")
		for _, recordedRepo := range recorded {
			fmt.Fprintf(&choices, "\n    homeskillet %s %s", inv.command, recordedRepo)
		}
		return "", config.Manifest{}, false, fmt.Errorf("%w%s", err, choices.String())
	}
	switch len(recorded) {
	case 0:
		return "", config.Manifest{}, false, fmt.Errorf("%w\n  name one: homeskillet %s /path/to/dotfiles", err, inv.command)
	case 1:
		repo, manifest, err = loadRepository(recorded[0])
		if err != nil {
			return "", config.Manifest{}, false, fmt.Errorf("the repository linked into %s before: %w", home, err)
		}
		return repo, manifest, true, nil
	}
	var choices strings.Builder
	fmt.Fprintf(&choices, "\n  several repositories are linked into %s; name one:", home)
	for _, recordedRepo := range recorded {
		fmt.Fprintf(&choices, "\n    homeskillet %s %s", inv.command, recordedRepo)
	}
	return "", config.Manifest{}, false, fmt.Errorf("%w%s", err, choices.String())
}

// loadRepository resolves path, refuses system locations, and loads the
// repository there.
func loadRepository(path string) (string, config.Manifest, error) {
	repo, err := canonicalDirectory(path)
	if err != nil {
		return "", config.Manifest{}, fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckLocation(repo); err != nil {
		return "", config.Manifest{}, fmt.Errorf("repository: %w", refusedLocation{err})
	}
	manifest, err := config.Load(repo)
	return repo, manifest, err
}

// refusedLocation is a repository trust.CheckLocation refuses, which
// openRepository may replace with a recorded one.
type refusedLocation struct{ error }

func (e refusedLocation) Unwrap() error { return e.error }

// runLinks reports or carries out status, link, or unlink for the files
// inv.files names, or for every entry when it names none (see matcher). v
// writes for a person at a terminal; when it is nil, output gets one
// tab-separated line per entry or change, for scripts, and notes that would
// break that format go to diagnostics.
func runLinks(inv invocation, files []config.Entry, repo, home, data string, output, diagnostics io.Writer, v *view) error {
	unlinking := inv.command == "unlink"
	names := newMatcher(inv.files, repo, home)
	var selected func(target, source string) bool
	if names != nil {
		selected = names.matches
	}
	plans, err := link.PrepareSelected(files, repo, home, data, unlinking, selected)
	if err != nil {
		return err
	}
	if err := names.check(plans); err != nil {
		return err
	}
	// A link a shell would run in place of another command is allowed, but
	// pointed out before anything changes (see shadowed).
	if !unlinking {
		shadows := shadowed(plans, home, inv.force)
		switch {
		case v != nil:
			v.shadows = shadows
		case !inv.quiet:
			for _, plan := range plans {
				if other := shadows[plan.Target]; other != "" {
					// Both paths come from the user's PATH and repository, not
					// from homeskillet, so they are quoted if they could
					// rewrite the terminal.
					if _, err := fmt.Fprintf(diagnostics, "homeskillet: %s runs instead of %s on PATH; link it only from a repository you trust\n", displayPath(plan.Target), displayPath(other)); err != nil {
						return err
					}
				}
			}
		}
	}
	// Conflicts and absent sources fail status as well as link, so status
	// doubles as a check that link would succeed. They never block unlink (see
	// link.Remove), and link --force moves conflicts aside instead (see
	// link.Backup). Orphaned links fail nothing: link and unlink remove them.
	blocked := !unlinking && slices.ContainsFunc(plans, func(plan link.Plan) bool {
		return plan.State == link.NoSource || plan.State == link.Conflict && !inv.force
	})
	// --force would refuse a directory anyway; saying so before a dry run
	// keeps the preview honest.
	if inv.force {
		if err := link.CheckForce(plans); err != nil {
			return err
		}
	}
	// Windows lets a process make symlinks only in Developer Mode. Finding
	// that out from the first link would leave its directories made, and
	// under --force files moved, so link asks first, in a dry run too.
	linking := slices.ContainsFunc(plans, func(plan link.Plan) bool {
		return plan.State == link.Missing || plan.State == link.Conflict && inv.force
	})
	if inv.command == "link" && linking && !blocked {
		if err := host.CheckSymlinks(); err != nil {
			return err
		}
	}
	if inv.command == "status" || blocked || inv.dryRun {
		return report(inv, plans, blocked, output, v)
	}
	// Scripts get a line per change as it happens; the view reports once
	// everything is done.
	log := output
	if v != nil {
		log = nil
	}
	applied := plans
	var moved map[string]string
	if unlinking {
		err = link.Remove(applied, log)
	} else {
		if inv.force {
			moved, applied, err = link.Backup(applied, home, data, log)
		}
		if err == nil {
			err = link.Create(applied, log)
		}
		// Put back whatever linking did not replace, so no target is left
		// with neither its original file nor its new link.
		if err != nil && len(moved) > 0 {
			if restoreErr := link.Restore(moved); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("files not restored are in %s: %w", filepath.Join(data, link.BackupDir), restoreErr))
			}
		}
	}
	// The record is what lets a later status find links whose files have
	// since left the repository (see link.Save). It is written even after a
	// failure, from the targets as they now are, so links made before the
	// failure are not forgotten.
	saveErr := record(repo, home, data, plans, names != nil)
	if err != nil {
		return errors.Join(err, saveErr)
	}
	var reportErr error
	if v != nil {
		reportErr = v.links(output, inv.command, plans, true, inv.force, moved)
	}
	return errors.Join(reportErr, saveErr)
}

// report prints plans that are only checked, for status, a dry run, or a
// blocked link, and returns the error a blocked link exits with.
func report(inv invocation, plans []link.Plan, blocked bool, output io.Writer, v *view) error {
	var err error
	if v != nil {
		err = v.links(output, inv.command, plans, false, inv.force, nil)
	} else {
		// A failed write is an error: a status that could not be fully
		// printed must not exit successfully, or a redirected report could
		// be silently truncated.
		for _, plan := range plans {
			if _, err = fmt.Fprintf(output, "%s\t%s -> %s\n", plan.State, plan.Target, plan.Source); err != nil {
				break
			}
		}
	}
	switch {
	case err != nil:
		return err
	case !blocked:
		return nil
	case inv.command == "link" && !inv.dryRun:
		err = fmt.Errorf("%w; nothing changed", link.ErrBlocked)
	default:
		err = fmt.Errorf("link would be %w", link.ErrBlocked)
	}
	// The view's closing line has said why and what to do.
	if v != nil {
		err = reportedError{err}
	}
	return err
}

// record saves the links plans cover as they now exist (see link.Refresh and
// link.Save). scoped says the plans cover only the files a command named.
func record(repo, home, data string, plans []link.Plan, scoped bool) error {
	refreshed, err := link.Refresh(plans)
	if err != nil {
		return err
	}
	return link.Save(repo, home, data, refreshed, scoped)
}

// matcher selects entries by the names given after the repository. A name is
// a target in home (.bashrc, or ~/.bashrc as the shell expands it), a source
// in the repository (home/.bashrc), or a directory holding either, and
// selects every entry whose target or source it is or contains.
type matcher struct {
	names []string
	// paths holds, for each name, the paths it may mean.
	paths [][]string
}

// newMatcher returns a matcher for names, or nil, which selects everything,
// when there are none. Each path a name may mean is tried as written and with
// the symlinks in its existing part resolved (see trust.Canonical): plans
// name canonical paths, so a path given through a symlink, such as macOS's
// /tmp, would otherwise match nothing.
func newMatcher(names []string, repo, home string) *matcher {
	if len(names) == 0 {
		return nil
	}
	m := &matcher{names: names}
	for _, name := range names {
		var paths []string
		rest, homeName := homeRelative(name)
		switch {
		case filepath.IsAbs(name):
			paths = []string{filepath.Clean(name)}
		case homeName:
			paths = []string{filepath.Join(home, rest)}
		default:
			paths = []string{filepath.Join(home, name), filepath.Join(repo, name)}
		}
		for _, path := range slices.Clone(paths) {
			if canonical, err := trust.Canonical(path); err == nil && canonical != path {
				paths = append(paths, canonical)
			}
		}
		m.paths = append(m.paths, paths)
	}
	return m
}

// matches reports whether any name selects the entry with target and source.
func (m *matcher) matches(target, source string) bool {
	for i := range m.names {
		if m.selects(i, target, source) {
			return true
		}
	}
	return false
}

// selects reports whether name i selects the entry with target and source.
func (m *matcher) selects(i int, target, source string) bool {
	for _, path := range m.paths[i] {
		if trust.Within(path, target) || trust.Within(path, source) {
			return true
		}
	}
	return false
}

// check returns an error for the first name that selected none of plans, so
// a typo cannot quietly act on nothing. It runs before anything changes.
func (m *matcher) check(plans []link.Plan) error {
	if m == nil {
		return nil
	}
	for i, name := range m.names {
		if !slices.ContainsFunc(plans, func(plan link.Plan) bool { return m.selects(i, plan.Target, plan.Source) }) {
			return fmt.Errorf("%q matches no file in the repository", name)
		}
	}
	return nil
}

// selectSteps returns the steps among applicable, the ones for this platform,
// that names names, in the manifest's order, since a later step may rely on
// an earlier one; or every applicable step when names is empty. A name that
// matches no step, or only a step for other platforms, fails before anything
// runs, so a typo cannot quietly run nothing.
func selectSteps(names []string, applicable, all []config.Step, repo string, platform config.Platform) ([]config.Step, error) {
	if len(names) == 0 {
		return applicable, nil
	}
	for _, name := range names {
		named := func(step config.Step) bool { return step.Name == name }
		switch {
		case slices.ContainsFunc(applicable, named):
		case slices.ContainsFunc(all, named):
			return nil, fmt.Errorf("setup step %s is not for %s", name, platform)
		default:
			var known []string
			for i := range applicable {
				known = append(known, applicable[i].Name)
			}
			return nil, fmt.Errorf("%s has no setup step %s; its steps for %s are %s\n  to name a repository, write it as a path, such as ./%s",
				repo, name, platform, cmp.Or(strings.Join(known, ", "), "none"), name)
		}
	}
	return slices.DeleteFunc(slices.Clone(applicable), func(step config.Step) bool { return !slices.Contains(names, step.Name) }), nil
}

// runSetup lists the steps for a dry run, grouped for a person at a terminal
// (v) or one line each for scripts, or runs them. Steps a person named are
// offered even when done (see setup.Options.Again), and listed so.
func runSetup(ctx context.Context, steps []config.Step, repo, home string, dryRun bool, opts setup.Options, v *view) error {
	plans, err := setup.Prepare(steps, repo, home, opts.Data)
	if err != nil {
		return err
	}
	if opts.Again {
		for i := range plans {
			plans[i].State = setup.Pending
		}
	}
	if !dryRun {
		if v != nil {
			// The steps already done are shown as a group first; the
			// reporter then covers what happens to the pending ones.
			if err := v.doneSteps(opts.Output, plans); err != nil {
				return err
			}
			opts.Report = v.stepReporter(opts.Output, plans)
		}
		return setup.Run(ctx, plans, opts)
	}
	if v != nil {
		return v.steps(opts.Output, plans, opts.Yes)
	}
	for i := range plans {
		if err := setup.List(opts.Output, plans[i]); err != nil {
			return err
		}
	}
	return nil
}

// dataDirVariable names the environment variable that sets homeskillet's data
// directory for every command, so a choice made once in a shell startup file
// applies to all of them.
const dataDirVariable = "HOMESKILLET_DATA_DIR"

// dataDir returns homeskillet's data directory, which holds installed
// versions and the record of links: --data-dir if given; else, for self with
// --prefix, share/homeskillet under the prefix; else $HOMESKILLET_DATA_DIR;
// else .local/share/homeskillet in the home directory. Explicit flags win
// over the environment, as everywhere.
//
// A value starting with ~/, or relative, names a directory in the selected
// home, such as ~/.config/homeskillet, so the same setting works for every
// home, including a scratch one given with --home. ~ is expanded here because
// shells leave it alone after --data-dir=. An absolute value is used as is,
// for any home; the record keeps each home's links apart.
//
// The result is canonical (see trust.Canonical) and must pass
// trust.CheckLocation.
func dataDir(inv invocation, home string) (string, error) {
	value := inv.data
	if value == "" && inv.command == "self" && inv.prefix != "" {
		prefix, err := filepath.Abs(underHome(inv.prefix, home))
		if err != nil {
			return "", err
		}
		value = filepath.Join(prefix, "share", install.Binary)
	}
	if value == "" {
		value = os.Getenv(dataDirVariable)
	}
	if value == "" {
		value = filepath.Join(".local", "share", install.Binary)
	}
	if rest, ok := homeRelative(value); ok {
		value = "." + rest
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(home, value)
	}
	data, err := trust.Canonical(value)
	if err != nil {
		return "", fmt.Errorf("data directory: %w", err)
	}
	if err := trust.CheckLocation(data); err != nil {
		return "", fmt.Errorf("data directory: %w", err)
	}
	return data, nil
}

// underHome returns value with a leading ~ (see homeRelative) meaning home,
// the selected home, as for --prefix and --data-dir.
func underHome(value, home string) string {
	if rest, ok := homeRelative(value); ok {
		return filepath.Join(home, rest)
	}
	return value
}

// expandHome returns path with a leading ~ (see homeRelative) replaced by
// the user's own home, as Unix shells do, but cmd never and PowerShell not
// everywhere, so ~/dotfiles, or ~\dotfiles on Windows, names the same
// directory from any shell. A ~ anywhere else, or ~user, is left alone.
func expandHome(path string) (string, error) {
	rest, ok := homeRelative(path)
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, rest), nil
}

// canonicalDirectory makes path absolute, with a leading ~ expanded (see
// expandHome), and resolves its symlinks. Packages link and trust compare
// path strings to decide containment and ownership, which is only sound for
// canonical paths.
func canonicalDirectory(path string) (string, error) {
	path, err := expandHome(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	//nolint:gosec // G703: resolving the path the user named is the purpose; every caller checks the result with trust.CheckLocation before acting on it
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return resolved, nil
}
