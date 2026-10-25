//go:build linux || darwin

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
	"os"
	"path/filepath"
	"slices"
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
	commands     = []string{"status", "link", "unlink", "setup", "self", "version"}
	selfCommands = []string{"install", "use", "list", "remove"}
	// scoped lists the commands that accept each flag that only some
	// commands use. Accepting such a flag silently elsewhere would suggest
	// it changes something there, and --yes in particular approves running
	// code.
	scoped = map[string][]string{
		"repo": {"status", "link", "unlink", "setup"},
		"yes":  {"setup"},
		// self installs the running build and version prints its own
		// details, so neither acts for another platform.
		"platform": {"status", "link", "unlink", "setup"},
		"prefix":   {"self"},
		// setup and version keep nothing in the data directory.
		"data-dir": {"status", "link", "unlink", "self"},
		// Only link replaces anything, and only by moving it aside.
		"force": {"link"},
		// setup must show every step exactly before it is approved, so it
		// cannot be quiet.
		"quiet": {"status", "link", "unlink", "self"},
	}
	// selecting lists the commands that take files after the repository,
	// acting on those alone.
	selecting = []string{"status", "link", "unlink"}
)

// invocation is a parsed command line.
type invocation struct {
	command string // one of commands
	sub     string // for self, one of selfCommands
	operand string // for self use and self remove, the version
	repo    string
	// repoGiven says the repository was named rather than defaulted (see
	// openRepository).
	repoGiven bool
	// files selects entries for status, link, and unlink: targets in home,
	// sources in the repository, or directories holding either. Empty
	// means every entry.
	files  []string
	force  bool // --force: move conflicts aside, then link
	quiet  bool // --quiet: print nothing but errors
	home   string
	prefix string
	data   string // --data-dir
	dryRun bool
	root   bool // --allow-root
	yes    bool
	// platform is --platform, the platform to act for instead of the
	// detected one.
	platform string
}

// Run executes one homeskillet command. args excludes the program name. input
// answers setup prompts and is passed to setup steps. Results go to output;
// usage text, flag errors, and setup steps' stderr go to diagnostics. A nil
// ctx means context.Background, a nil input answers no to every prompt, and
// nil output or diagnostics discard what would be written there.
func Run(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == nil {
		output = io.Discard
	}
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	inv, err := parse(args, diagnostics)
	if err != nil || inv.command == "" {
		return err
	}
	// Errors still reach main, which prints them; only results are dropped.
	if inv.quiet {
		output = io.Discard
	}
	if inv.command == "version" {
		info := version.Current()
		detected, translated := host.Detect()
		platform := fmt.Sprintf("  %-10s %s\n", "platform:", info.Platform)
		// The build's platform and the host's differ only when the binary
		// runs translated, as an amd64 build does under Rosetta 2 on Apple
		// silicon; then both are shown, with the build to install instead.
		if detected.String() != info.Platform {
			hostLine := detected.String()
			if translated {
				hostLine += " (translated by Rosetta; install a " + detected.String() + " build)"
			}
			platform = fmt.Sprintf("  %-10s %s\n  %-10s %s\n", "built for:", info.Platform, "host:", hostLine)
		}
		_, err := fmt.Fprintf(output, "homeskillet %s\n%s%s", info.Version, info.Details(), platform)
		return err
	}
	// Most sudo configurations reset HOME to /root, so "sudo homeskillet link"
	// would point root's shell startup files at files the invoking user owns,
	// letting that account run code as root, and "sudo homeskillet self
	// install" would put a binary the user owns on root's PATH. The checks in
	// package trust already reject both; refusing root outright also makes
	// managing root's own dotfiles, as in a container, a deliberate choice.
	if os.Geteuid() == 0 && !inv.root {
		return errors.New("refusing to run as root; pass --allow-root to manage root's own dotfiles")
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
	if inv.command == "self" {
		return runSelf(inv, home, output)
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
	// A repository whose platforms leave out this one would otherwise do
	// nothing and report success, as if everything were linked. unlink
	// still runs, so links made before the platforms changed can be removed.
	if len(files) == 0 && len(steps) == 0 && len(manifest.Platforms) > 0 && inv.command != "unlink" {
		return fmt.Errorf("nothing in %s applies to %s: its platforms are %s; pass --platform to act for another platform",
			repo, platform, strings.Join(manifest.Platforms, ", "))
	}
	// A person at a terminal sees first what is running and where; scripts
	// get only the results (see terminal).
	v := newView(output)
	if v != nil {
		if err := v.header(output, inv.command, repo, remembered, home, platform, inv.dryRun); err != nil {
			return err
		}
	}
	if inv.command == "setup" {
		return runSetup(ctx, steps, repo, home, inv.dryRun, setup.Options{
			Repo: repo, Home: home, Yes: inv.yes, Input: input, Output: output, Errors: diagnostics,
		})
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return err
	}
	return runLinks(inv, files, repo, home, data, output, v)
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
// help was requested, after printing usage to diagnostics.
func parse(args []string, diagnostics io.Writer) (invocation, error) {
	// --version is the conventional spelling of the version command.
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		args = []string{"version"}
	}
	inv := invocation{command: "status"}
	inv.command, args = word(args, inv.command)
	if !slices.Contains(commands, inv.command) {
		// A path in place of a command means status for that repository.
		// Only a word that is plainly a path counts, so a mistyped command
		// is reported rather than read as a directory, and status never
		// changes anything either way.
		if !isPath(inv.command) {
			return invocation{}, fmt.Errorf("unknown command %q; use %s, or a repository path such as ./%s",
				inv.command, strings.Join(commands, ", "), inv.command)
		}
		args = append([]string{inv.command}, args...)
		inv.command = "status"
	}
	if inv.command == "self" {
		inv.sub, args = word(args, "")
		if inv.sub != "" && !slices.Contains(selfCommands, inv.sub) {
			return invocation{}, fmt.Errorf("unknown command self %s; use self %s", inv.sub, strings.Join(selfCommands, ", self "))
		}
	}
	flags := flag.NewFlagSet("homeskillet", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.StringVar(&inv.repo, "repo", ".", "configuration repository, holding "+config.FileName+", a "+config.HomeDir+" directory, or both; also given as the argument after the command")
	flags.StringVar(&inv.home, "home", "", "target home directory (default: current user's home)")
	flags.StringVar(&inv.prefix, "prefix", "", "self only: installation prefix (default: .local in the home directory)")
	flags.StringVar(&inv.data, "data-dir", "", "homeskillet's data directory; ~/ or a relative path means in the home directory (default: $"+dataDirVariable+", else ~/.local/share/homeskillet)")
	flags.BoolVar(&inv.dryRun, "dry-run", false, "preview without changing files or running setup steps")
	flags.BoolVar(&inv.root, "allow-root", false, "allow running as root; root must own everything homeskillet reads")
	flags.BoolVar(&inv.yes, "yes", false, "setup only: run every pending step without asking")
	flags.BoolVar(&inv.force, "force", false, "link only: move conflicting files into a backup in the data directory, then link")
	flags.BoolVar(&inv.quiet, "quiet", false, "status, link, unlink, and self only: print nothing but errors; the exit status reports the result")
	flags.StringVar(&inv.platform, "platform", "", "act for this platform, os or os/arch such as darwin/arm64 (default: detected)")
	flags.Usage = func() {
		// Usage has no way to return an error, and flag.PrintDefaults
		// discards its own write errors the same way.
		_, _ = fmt.Fprint(diagnostics, usage)
		flags.PrintDefaults()
	}
	operands, err := parseFlags(flags, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return invocation{}, nil
		}
		return invocation{}, err
	}
	var misplaced error
	repoFlag := false
	flags.Visit(func(f *flag.Flag) {
		repoFlag = repoFlag || f.Name == "repo"
		inv.repoGiven = inv.repoGiven || f.Name == "repo"
		if allowed, ok := scoped[f.Name]; ok && !slices.Contains(allowed, inv.command) && misplaced == nil {
			misplaced = fmt.Errorf("--%s applies only to %s", f.Name, strings.Join(allowed, ", "))
		}
	})
	// The first operand is the repository unless --repo named it; any after
	// it select files, for the commands that act on files.
	if slices.Contains(scoped["repo"], inv.command) && !repoFlag && len(operands) > 0 {
		inv.repo, operands = operands[0], operands[1:]
		inv.repoGiven = true
	}
	switch {
	case misplaced != nil:
		return invocation{}, misplaced
	case (inv.sub == "use" || inv.sub == "remove") && len(operands) == 1:
		inv.operand = operands[0]
	case slices.Contains(selecting, inv.command):
		inv.files = operands
	case len(operands) != 0:
		return invocation{}, fmt.Errorf("unexpected arguments: %s", strings.Join(operands, " "))
	}
	switch {
	case inv.command == "version" && flags.NFlag() != 0:
		return invocation{}, errors.New("version takes no options")
	case inv.command == "self" && inv.sub == "":
		return invocation{}, fmt.Errorf("self needs a command: %s", strings.Join(selfCommands, ", "))
	case (inv.sub == "use" || inv.sub == "remove") && inv.operand == "":
		return invocation{}, fmt.Errorf("self %s needs a version", inv.sub)
	}
	return inv, nil
}

const usage = `Usage: homeskillet [status|link|unlink] [<repository> [<file>...]] [options]
       homeskillet setup [<repository>] [options]
       homeskillet self install|list [options]
       homeskillet self use latest|previous|<version> [options]
       homeskillet self remove <version> [options]
       homeskillet version
The repository defaults to the current directory or, when that is not one,
to the one repository already linked into the home. Without a command, give it
as a path such as . or ./dotfiles; status is the default command. Files after
the repository limit the command to those files, or to the files in those
directories, named by target (.bashrc) or source (home/.bashrc).
`

// word returns the first argument and the rest when the first is not an
// option, and fallback with all arguments otherwise.
func word(args []string, fallback string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return fallback, args
}

// isPath reports whether a word in place of a command is plainly a path: . or
// .., or anything containing a slash. It looks only at the text, so whether a
// word is read as a command never depends on what the current directory holds.
func isPath(word string) bool {
	return word == "." || word == ".." || strings.ContainsRune(word, '/')
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
func openRepository(inv invocation, home string) (repo string, manifest config.Manifest, remembered bool, err error) {
	repo, manifest, err = loadRepository(inv.repo)
	if err == nil || inv.repoGiven || !errors.Is(err, config.ErrNothingToManage) {
		return repo, manifest, false, err
	}
	var recorded []string
	if data, dataErr := dataDir(inv, home); dataErr == nil {
		recorded, _ = link.Repositories(data, home)
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
		return "", config.Manifest{}, fmt.Errorf("repository: %w", err)
	}
	manifest, err := config.Load(repo)
	return repo, manifest, err
}

// runLinks reports or carries out status, link, or unlink for the files
// inv.files names, or for every entry when it names none (see matcher). v
// writes for a person at a terminal; when it is nil, output gets one
// tab-separated line per entry or change, for scripts.
func runLinks(inv invocation, files []config.Entry, repo, home, data string, output io.Writer, v *view) error {
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
		switch {
		case filepath.IsAbs(name):
			paths = []string{filepath.Clean(name)}
		case name == "~" || strings.HasPrefix(name, "~/"):
			paths = []string{filepath.Join(home, name[1:])}
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

func runSetup(ctx context.Context, steps []config.Step, repo, home string, dryRun bool, opts setup.Options) error {
	plans, err := setup.Prepare(steps, repo, home)
	if err != nil {
		return err
	}
	if dryRun {
		for _, plan := range plans {
			if err := setup.Describe(opts.Output, plan, repo); err != nil {
				return err
			}
		}
		return nil
	}
	return setup.Run(ctx, plans, opts)
}

// runSelf manages installed versions of homeskillet under the prefix,
// .local in the home directory unless --prefix says otherwise.
func runSelf(inv invocation, home string, output io.Writer) error {
	prefix := inv.prefix
	if prefix == "" {
		prefix = filepath.Join(home, ".local")
	}
	prefix, err := filepath.Abs(prefix)
	if err != nil {
		return err
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return err
	}
	layout, err := install.New(filepath.Join(prefix, "bin"), data)
	if err != nil {
		return err
	}
	switch inv.sub {
	case "list":
		return listVersions(layout, output)
	case "install":
		// The running program installs itself, so the version recorded in
		// it and the bytes copied cannot disagree.
		var executable string
		if executable, err = os.Executable(); err != nil {
			return err
		}
		// An installed build should match the hardware, not the
		// translation layer it happens to run under.
		if detected, translated := host.Detect(); translated {
			if _, err := fmt.Fprintf(output, "note\tinstalling a %s build on %s, where it runs translated; build for %s\n", host.Build(), detected, detected); err != nil {
				return err
			}
		}
		err = layout.Install(executable, version.Current(), inv.dryRun, output)
		if err == nil && !onPath(layout.Bin) {
			_, err = fmt.Fprintf(output, "note\t%s is not on PATH; add it in your shell startup file\n", layout.Bin)
		}
	case "use":
		err = layout.Use(inv.operand, inv.dryRun, output)
	case "remove":
		err = layout.Remove(inv.operand, inv.dryRun, output)
	}
	if err == nil && inv.dryRun {
		_, err = fmt.Fprintln(output, "dry run: nothing changed")
	}
	return err
}

// listVersions prints one line per installed version: the version, its role
// (latest, previous, current), and the build details install recorded for it.
func listVersions(layout install.Layout, output io.Writer) error {
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	if len(state.Versions) == 0 {
		_, err := fmt.Fprintf(output, "no versions installed in %s\n", layout.Data)
		return err
	}
	for _, v := range state.Versions {
		var roles []string
		if v == state.Latest() {
			roles = append(roles, install.Latest)
		}
		if v == state.Previous() {
			roles = append(roles, install.Previous)
		}
		switch {
		case v == state.Current && state.Following:
			roles = append(roles, "current")
		case v == state.Current:
			roles = append(roles, "current (pinned)")
		}
		// The record is a file like any other, so it must not be able to
		// rewrite the terminal either.
		build := "build details unavailable"
		if info, err := layout.Build(v); err == nil && config.Printable(info.Summary()) {
			build = info.Summary()
		}
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\n", v, cmp.Or(strings.Join(roles, ", "), "-"), build); err != nil {
			return err
		}
	}
	return nil
}

// onPath reports whether dir is on PATH. Entries are compared after
// resolving symlinks, so one that reaches dir through a symlink counts.
func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(entry); err == nil && resolved == dir {
			return true
		}
	}
	return false
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
		prefix, err := filepath.Abs(inv.prefix)
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
	if rest, ok := strings.CutPrefix(value, "~"); ok && (rest == "" || rest[0] == '/') {
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

// canonicalDirectory makes path absolute and resolves its symlinks. Packages
// link and trust compare path strings to decide containment and ownership,
// which is only sound for canonical paths.
func canonicalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return resolved, nil
}
