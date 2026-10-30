//go:build linux || darwin || windows

// The help text for --help and the help command: an overview of every
// command, and each command's own help. Options are described by their flag
// definitions, and each command's are those parse accepts for it, so the
// text cannot drift from what runs; every description starts in one column,
// wrapped to fit a terminal.

package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// helpWidth is the widest line help writes before wrapping.
const helpWidth = 120

// docsURL is where help points for the details its one-line descriptions
// leave out.
const docsURL = "https://github.com/nicholashoule/homeskillet/blob/main/docs/usage.md"

// commandHelp describes each of commands in one line, in the list of
// commands and atop the command's own help.
var commandHelp = map[string]string{
	"status":      "Show whether each file is linked (the default)",
	"link":        "Link the repository's files into the home directory",
	"unlink":      "Remove the repository's links from the home directory",
	"setup":       "Show each setup step and run the ones you approve",
	"init":        "Write a starter " + config.FileName + " in a repository without a manifest",
	"check":       "List every issue in a repository, for every platform, or say it is valid",
	"prepare":     "Check for what homeskillet needs, such as Developer Mode and Git, and offer what is missing",
	"wsl":         "Install homeskillet in a WSL distribution, or run status, link, unlink, or setup there",
	"backups":     "List the files link --force moved aside; backups purge deletes them after asking",
	"update":      "Install the newest release and run it, keeping the old one to go back to",
	"install":     "Install this build as a version of its own, or with a VERSION, that release",
	"use":         "Run latest, previous, or an installed VERSION",
	"list":        "List the installed versions and which one runs",
	"list-remote": "List the published versions and which are installed",
	"remove":      "Delete installed versions, unless one is the version that runs",
	"self":        "The version commands, named as self install, self list, and so on",
	"version":     "Show the version and build details",
	"info":        "Show which homeskillet runs, the system, and the terminal, to paste into a bug report",
	"term":        "Show what homeskillet detects about this terminal, such as whether colors are on and why",
	"completion":  "Print a completion script for bash, zsh, or pwsh",
	"help":        "Show the commands, or with a COMMAND, its arguments and options",
}

// commandUsage shows how to run each of commands, atop its own help.
var commandUsage = map[string][]string{
	"status":      {"homeskillet [status] [REPOSITORY [FILE...]] [options]"},
	"link":        {"homeskillet link [REPOSITORY [FILE...]] [options]"},
	"unlink":      {"homeskillet unlink [REPOSITORY [FILE...]] [options]"},
	"setup":       {"homeskillet setup [REPOSITORY] [STEP...] [options]"},
	"init":        {"homeskillet init [REPOSITORY] [options]"},
	"check":       {"homeskillet check [REPOSITORY] [options]"},
	"prepare":     {"homeskillet prepare|prep [options]"},
	"wsl":         {"homeskillet wsl install [options]", "homeskillet wsl status|link|unlink|setup [REPOSITORY [FILE...|STEP...]] [options]"},
	"backups":     {"homeskillet backups [list] [options]", "homeskillet backups purge [options]"},
	"update":      {"homeskillet update [VERSION] [options]"},
	"install":     {"homeskillet install [VERSION] [options]"},
	"use":         {"homeskillet use latest|previous|VERSION [options]"},
	"list":        {"homeskillet list [options]"},
	"list-remote": {"homeskillet list-remote [options]"},
	"remove":      {"homeskillet remove VERSION... [options]"},
	"self": {
		"homeskillet self install [VERSION] [options]", "homeskillet self update [VERSION] [options]",
		"homeskillet self releases [options]", "homeskillet self list [options]",
		"homeskillet self use latest|previous|VERSION [options]", "homeskillet self remove VERSION... [options]",
	},
	"version":    {"homeskillet version"},
	"info":       {"homeskillet info"},
	"term":       {"homeskillet term"},
	"completion": {"homeskillet completion bash|zsh|pwsh"},
	"help":       {"homeskillet help [COMMAND]", "homeskillet COMMAND --help"},
}

// optionOrder is the order help lists options in, most used first.
var optionOrder = []string{
	"dry-run", "home", "repo", "platform", "force", "quiet", "verbose", "data-dir", "prefix", "older-than",
	"distribution", "from", "yes", "allow-root",
}

// yesHelp says what --yes approves for each command that takes it, in that
// command's help, in place of the flag's usage string, which covers them all.
var yesHelp = map[string]string{
	"setup":   "Run every pending step without asking, for unattended builds such as a Dockerfile",
	"prepare": "Run the installs without asking; winget's own repair still asks",
	"init":    "Create the starter files without asking",
	"backups": "For backups purge, delete every backup it lists without asking",
}

// argumentHelp describes the arguments command takes, for its own help.
func argumentHelp(command string) [][2]string {
	repository := [2]string{"REPOSITORY", "A directory with a manifest, " + config.FileName + " or " + config.DotfilesFileName +
		" or both, a " + config.HomeDir + "/ directory, or both (default: the current directory, else the one linked into the home)"}
	file := [2]string{"FILE", "Limits " + command + " to this file or directory, named by link (.bashrc) or source (" +
		config.HomeDir + "/.bashrc). Put -- before a name starting with -"}
	step := [2]string{"STEP", "Offers only the step with this name, even when it is done, so naming a step runs it again. " +
		"Write a REPOSITORY before it as a path, as ./dotfiles, so it is never read as a step"}
	installed := "such as 0.4.0 or v0.4.0; homeskillet list shows them"
	published := "three numbers with no pre-release part, such as 0.5.0 or v0.5.0; homeskillet list-remote shows them"
	switch command {
	case "status":
		repository[1] += ". Without the command, write it as a path: . or ./dotfiles"
		return [][2]string{repository, file}
	case "link", "unlink":
		return [][2]string{repository, file}
	case "setup":
		return [][2]string{repository, step}
	case "init":
		return [][2]string{{"REPOSITORY", "The directory to start a repository in, which has no manifest yet, created if it does not exist (default: the current directory)"}}
	case "check":
		return [][2]string{repository}
	case "wsl":
		return [][2]string{
			{"REPOSITORY", "A Windows clone, which wsl copies into the distribution, or written / or ~ first, a clone already there"},
			{"FILE", "Limits status, link, or unlink to this file or directory, as they do outside WSL"},
			{"STEP", "Offers only the setup step with this name, as setup does outside WSL"},
		}
	case "update":
		return [][2]string{{"VERSION", "A published release to install, leaving which version runs as it is: " + published}}
	case "install":
		return [][2]string{{"VERSION", "A published release to install, checked as update checks it: " + published}}
	case "use":
		return [][2]string{{"VERSION", "An installed version, " + installed + ". latest is the newest, and previous the one before it"}}
	case "remove":
		return [][2]string{{"VERSION", "An installed version to delete, other than the one that runs, " + installed}}
	case "self":
		return [][2]string{{"VERSION", "For install and update, a published release, three numbers such as 0.5.0; " +
			"for use and remove, an installed version, as self list shows"}}
	case "help":
		return [][2]string{{"COMMAND", "A command to show in full, such as link or self"}}
	}
	return nil
}

// commandOptions returns the options command takes, in optionOrder, those
// scoped to it and those every command takes, read from the same table
// parse checks (see scoped), so help never offers an option the command
// refuses. The self commands named on their own, such as update, take
// self's.
func commandOptions(command string, flags *flag.FlagSet) [][2]string {
	if _, ok := selfAliases[command]; ok {
		command = "self"
	}
	var rows [][2]string
	for _, name := range optionOrder {
		allowed, ok := scoped[name]
		if f := flags.Lookup(name); f != nil && (!ok && command != "wsl" || slices.Contains(allowed, command)) {
			row := optionRow(f)
			if text := yesHelp[command]; name == "yes" && text != "" {
				row[1] = text
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// optionRow is the help line for option f: its name, the value it takes,
// and its usage string.
func optionRow(f *flag.Flag) [2]string {
	// A word in backquotes in the usage string names the value.
	value, text := flag.UnquoteUsage(f)
	label := "--" + f.Name
	if value != "" {
		label += " " + value
	}
	return [2]string{label, text}
}

// helpSection is a heading in help and the rows under it, each a name and
// its description.
type helpSection struct {
	heading string
	rows    [][2]string
}

// help writes the overview help shows without a command (see overview).
func help(w io.Writer) error {
	_, err := io.WriteString(w, overview(colorFor(w).on))
	return err
}

// overview is the help without a command: the version, every command in a
// line, and the options most commands take, those no command is denied (see
// scoped). Each command's own help has the rest (see helpFor). With color,
// it follows the standard (see styleName).
func overview(color bool) string {
	flags := newFlags(new(invocation))
	commandRows := make([][2]string, 0, len(commands))
	for _, name := range commands {
		commandRows = append(commandRows, [2]string{name, commandHelp[name]})
	}
	var common [][2]string
	for _, name := range optionOrder {
		if _, ok := scoped[name]; !ok {
			common = append(common, optionRow(flags.Lookup(name)))
		}
	}
	name := func(s string) string { return paint(color, styleName, s) }
	var b strings.Builder
	b.WriteString(paint(color, styleHeading, "homeskillet "+version.Current().Version) + "\n")
	wrap(&b, "", "Links dotfiles from a configuration repository into your home directory.")
	b.WriteString(project + "\n")
	b.WriteString("\n" + paint(color, styleHeading, "Usage:") + "\n  " + name("homeskillet COMMAND [ARGUMENTS] [options]") + "\n")
	writeSections(&b, []helpSection{{"Commands", commandRows}, {"Options most commands take", common}}, color)
	b.WriteString("\n")
	wrap(&b, "", "Run "+name("homeskillet help COMMAND")+", or "+name("homeskillet COMMAND --help")+", for a command's arguments and options.")
	// The details the descriptions leave out, such as how to load a
	// completion script or turn off the update note, are in the docs.
	fmt.Fprintf(&b, "Every command and option in full: %s\n", docsURL)
	return b.String()
}

// helpFor writes command's own help (see commandHelpText).
func helpFor(w io.Writer, command string) error {
	_, err := io.WriteString(w, commandHelpText(command, colorFor(w).on))
	return err
}

// commandHelpText is command's own help: what it does, how to run it, its
// arguments, and only the options it takes. A short name, such as prep, is
// its command's help. With color, it follows the standard (see styleName).
func commandHelpText(command string, color bool) string {
	if full, ok := shortNames[command]; ok {
		command = full
	}
	var b strings.Builder
	b.WriteString(paint(color, styleHeading, commandHelp[command]+".") + "\n")
	b.WriteString("\n" + paint(color, styleHeading, "Usage:") + "\n")
	for _, line := range commandUsage[command] {
		b.WriteString("  " + paint(color, styleName, line) + "\n")
	}
	sections := []helpSection{{"Arguments", argumentHelp(command)}}
	if !slices.Contains(optionless, command) {
		sections = append(sections, helpSection{"Options", commandOptions(command, newFlags(new(invocation)))})
	}
	writeSections(&b, sections, color)
	if command == "wsl" {
		b.WriteString("\n")
		wrap(&b, "", "Every other option is passed on to homeskillet in the distribution, where it means what it means there: "+
			"--home names a Linux directory. See "+paint(color, styleName, "homeskillet help status")+" and the others for theirs.")
	}
	fmt.Fprintf(&b, "\nIn full: %s\n", docsURL)
	return b.String()
}

// writeSections writes each section that has rows to b, under its heading.
// Every description starts in one column, just past the longest name in any
// section, and wraps at helpWidth. With color, headings are bold and names
// cyan, as the standard has it.
func writeSections(b *strings.Builder, sections []helpSection, color bool) {
	column := 0
	for _, s := range sections {
		for _, row := range s.rows {
			column = max(column, len(row[0]))
		}
	}
	// Two spaces before each name, and at least two after the longest.
	column += 4
	for _, s := range sections {
		if len(s.rows) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n%s\n", paint(color, styleHeading, s.heading+":"))
		for _, row := range s.rows {
			wrap(b, "  "+paint(color, styleName, row[0])+strings.Repeat(" ", column-2-len(row[0])), row[1])
		}
	}
}

// wrap writes text to b after prefix, breaking lines between words so none is
// wider than helpWidth, and indents each later line to prefix's width. Widths
// are what the screen shows (see visibleWidth), so painted words wrap where
// plain ones would.
func wrap(b *strings.Builder, prefix, text string) {
	line := prefix
	for i, word := range strings.Fields(text) {
		switch {
		case i == 0:
			line += word
		case visibleWidth(line)+1+visibleWidth(word) > helpWidth:
			b.WriteString(line + "\n")
			line = strings.Repeat(" ", visibleWidth(prefix)) + word
		default:
			line += " " + word
		}
	}
	b.WriteString(line + "\n")
}
