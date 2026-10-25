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
	"runtime"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/setup"
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
		"repo":   {"status", "link", "unlink", "setup"},
		"yes":    {"setup"},
		"prefix": {"self"},
	}
)

// invocation is a parsed command line.
type invocation struct {
	command string // one of commands
	sub     string // for self, one of selfCommands
	operand string // for self use and self remove, the version
	repo    string
	home    string
	prefix  string
	dryRun  bool
	root    bool // --allow-root
	yes     bool
}

// Run executes one homeskillet command. args excludes the program name. input
// answers setup prompts and is passed to setup steps. Results go to output;
// usage text, flag errors, and setup steps' stderr go to diagnostics.
func Run(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) error {
	inv, err := parse(args, diagnostics)
	if err != nil || inv.command == "" {
		return err
	}
	if inv.command == "version" {
		info := version.Current()
		_, err := fmt.Fprintf(output, "homeskillet %s\n%s", info.Version, info.Details())
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
	if inv.command == "self" {
		return runSelf(inv, home, output)
	}
	repo, err := canonicalDirectory(inv.repo)
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	manifest, err := config.Load(repo)
	if err != nil {
		return err
	}
	if inv.command == "setup" {
		return runSetup(ctx, manifest, repo, home, inv.dryRun, setup.Options{
			Repo: repo, Home: home, Yes: inv.yes, Input: input, Output: output, Errors: diagnostics,
		})
	}
	return runLinks(inv.command, manifest, repo, home, inv.dryRun, output)
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
		return invocation{}, fmt.Errorf("unknown command %q; use %s", inv.command, strings.Join(commands, ", "))
	}
	if inv.command == "self" {
		inv.sub, args = word(args, "")
		if inv.sub != "" && !slices.Contains(selfCommands, inv.sub) {
			return invocation{}, fmt.Errorf("unknown command self %s; use self %s", inv.sub, strings.Join(selfCommands, ", self "))
		}
		if inv.sub == "use" || inv.sub == "remove" {
			inv.operand, args = word(args, "")
		}
	}
	flags := flag.NewFlagSet("homeskillet", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.StringVar(&inv.repo, "repo", ".", "configuration repository, holding "+config.FileName+", a "+config.HomeDir+" directory, or both")
	flags.StringVar(&inv.home, "home", "", "target home directory (default: current user's home)")
	flags.StringVar(&inv.prefix, "prefix", "", "self only: installation prefix (default: .local in the home directory)")
	flags.BoolVar(&inv.dryRun, "dry-run", false, "preview without changing files or running setup steps")
	flags.BoolVar(&inv.root, "allow-root", false, "allow running as root; root must own everything homeskillet reads")
	flags.BoolVar(&inv.yes, "yes", false, "setup only: run every pending step without asking")
	flags.Usage = func() {
		// Usage has no way to return an error, and flag.PrintDefaults
		// discards its own write errors the same way.
		_, _ = fmt.Fprint(diagnostics, usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return invocation{}, nil
		}
		return invocation{}, err
	}
	if flags.NArg() != 0 {
		return invocation{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	var misplaced error
	flags.Visit(func(f *flag.Flag) {
		if allowed, ok := scoped[f.Name]; ok && !slices.Contains(allowed, inv.command) && misplaced == nil {
			misplaced = fmt.Errorf("--%s applies only to %s", f.Name, strings.Join(allowed, ", "))
		}
	})
	switch {
	case misplaced != nil:
		return invocation{}, misplaced
	case inv.command == "version" && flags.NFlag() != 0:
		return invocation{}, errors.New("version takes no options")
	case inv.command == "self" && inv.sub == "":
		return invocation{}, fmt.Errorf("self needs a command: %s", strings.Join(selfCommands, ", "))
	case (inv.sub == "use" || inv.sub == "remove") && inv.operand == "":
		return invocation{}, fmt.Errorf("self %s needs a version", inv.sub)
	}
	return inv, nil
}

const usage = `Usage: homeskillet [status|link|unlink|setup] [options]
       homeskillet self install|list [options]
       homeskillet self use latest|previous|<version> [options]
       homeskillet self remove <version> [options]
       homeskillet version
`

// word returns the first argument and the rest when the first is not an
// option, and fallback with all arguments otherwise.
func word(args []string, fallback string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return fallback, args
}

func runLinks(command string, manifest config.Manifest, repo, home string, dryRun bool, output io.Writer) error {
	plans, err := link.Prepare(manifest.FilesFor(runtime.GOOS), repo, home, command == "unlink")
	if err != nil {
		return err
	}
	// Conflicts and missing sources fail status as well as link, so status
	// doubles as a check that link would succeed. They never block unlink (see
	// link.Remove).
	blocked := command != "unlink" && link.Blocked(plans)
	if command == "status" || blocked || dryRun {
		// A failed write is an error: a status that could not be fully printed
		// must not exit successfully, or a redirected report could be
		// silently truncated.
		for _, plan := range plans {
			if _, err := fmt.Fprintf(output, "%s\t%s -> %s\n", plan.State, plan.Target, plan.Source); err != nil {
				return err
			}
		}
		if blocked {
			return link.ErrBlocked
		}
		return nil
	}
	if command == "link" {
		return link.Create(plans, output)
	}
	return link.Remove(plans, output)
}

func runSetup(ctx context.Context, manifest config.Manifest, repo, home string, dryRun bool, opts setup.Options) error {
	plans, err := setup.Prepare(manifest.SetupFor(runtime.GOOS), repo, home)
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
	layout, err := install.New(prefix)
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
		err = layout.Install(executable, version.Current().Version, inv.dryRun, output)
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
// (latest, previous, current), and the build details recorded in its binary,
// read without running it.
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
		build := "build details unreadable"
		if info, err := version.Read(layout.Program(v)); err == nil {
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
