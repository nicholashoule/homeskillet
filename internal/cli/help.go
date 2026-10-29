//go:build linux || darwin || windows

// The help text for --help and the help command. Options are described by
// their flag definitions, so the text cannot drift from what parse accepts,
// and every description starts in one column, wrapped to fit a terminal.

package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// helpWidth is the widest line help writes before wrapping.
const helpWidth = 120

const usageLines = `  homeskillet [status|link|unlink] [REPOSITORY [FILE...]] [options]
  homeskillet setup [REPOSITORY] [options]
  homeskillet init [REPOSITORY] [options]
  homeskillet check [REPOSITORY] [options]
  homeskillet prepare|prep [options]
  homeskillet wsl install [options]
  homeskillet wsl status|link|unlink|setup [REPOSITORY [FILE...|STEP...]] [options]
  homeskillet backups [list|purge] [options]
  homeskillet update [VERSION] [options]
  homeskillet install [VERSION] [options]
  homeskillet use latest|previous|VERSION [options]
  homeskillet list [options]
  homeskillet list-remote [options]
  homeskillet self install [VERSION] [options]
  homeskillet self list [options]
  homeskillet self update [VERSION] [options]
  homeskillet self releases [options]
  homeskillet self use latest|previous|VERSION [options]
  homeskillet self remove VERSION [options]
  homeskillet version
  homeskillet completion bash|zsh|pwsh
  homeskillet help
`

// docsURL is where help points for the details its one-line descriptions
// leave out.
const docsURL = "https://github.com/nicholashoule/homeskillet/blob/main/docs/usage.md"

// commandHelp describes each of commands in one line; help points to the
// docs for the rest (see docsURL).
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
	"self":        "The version commands above, as self install, self list, and so on, plus self remove",
	"version":     "Show the version and build details",
	"completion":  "Print a completion script for bash, zsh, or pwsh",
	"help":        "Show this help",
}

var argumentHelp = [][2]string{
	{"REPOSITORY", "A directory with a manifest, " + config.FileName + " or " + config.DotfilesFileName + " or both, a " +
		config.HomeDir + "/ directory, or both " +
		"(default: the current directory, else the one linked into the home). Without a command, write it as a path: . or ./dotfiles"},
	{"FILE", "Limits status, link, or unlink to this file or directory, named by link (.bashrc) or source (" +
		config.HomeDir + "/.bashrc). Put -- before a name starting with -"},
	{"VERSION", "An installed version, such as v0.1.1 or 0.1.1, for self use and self remove; self list shows them all. For self update and self install, " +
		"a published release to install, three numbers with no pre-release part; self releases shows them all"},
}

// optionGroups orders the options in help. The first group applies to several
// commands; each other group applies only to its command (see scoped).
var optionGroups = []struct {
	command string
	names   []string
}{
	{"", []string{"dry-run", "home", "repo", "platform", "quiet", "verbose", "data-dir", "yes", "allow-root"}},
	{"link", []string{"force"}},
	{"self", []string{"prefix"}},
	{"backups", []string{"older-than"}},
	{"wsl", []string{"distribution", "from"}},
}

// help writes the help text to w: the version, how to run each command, and
// the options, described by their usage strings. Every description starts in
// one column, just past the longest option, and wraps at helpWidth.
func help(w io.Writer, flags *flag.FlagSet) error {
	type section struct {
		heading string
		rows    [][2]string
	}
	commandRows := make([][2]string, 0, len(commands))
	for _, name := range commands {
		commandRows = append(commandRows, [2]string{name, commandHelp[name]})
	}
	sections := []section{{"Commands", commandRows}, {"Arguments", argumentHelp}}
	for _, group := range optionGroups {
		heading := "Options"
		if group.command != "" {
			heading = group.command + " options"
		}
		var rows [][2]string
		for _, name := range group.names {
			f := flags.Lookup(name)
			if f == nil {
				continue
			}
			// A word in backquotes in the usage string names the value.
			value, text := flag.UnquoteUsage(f)
			label := "--" + f.Name
			if value != "" {
				label += " " + value
			}
			rows = append(rows, [2]string{label, text})
		}
		sections = append(sections, section{heading, rows})
	}
	column := 0
	for _, s := range sections {
		for _, row := range s.rows {
			column = max(column, len(row[0]))
		}
	}
	// Two spaces before each name, and at least two after the longest.
	column += 4

	var b strings.Builder
	fmt.Fprintf(&b, "homeskillet %s\n", version.Current().Version)
	wrap(&b, "", "Links dotfiles from a configuration repository into your home directory.")
	b.WriteString("\nUsage:\n" + usageLines)
	for _, s := range sections {
		fmt.Fprintf(&b, "\n%s:\n", s.heading)
		for _, row := range s.rows {
			wrap(&b, "  "+row[0]+strings.Repeat(" ", column-2-len(row[0])), row[1])
		}
	}
	// The details the descriptions leave out, such as how to load a
	// completion script or turn off the update note, are in the docs.
	fmt.Fprintf(&b, "\nEvery command and option in full: %s\n", docsURL)
	_, err := io.WriteString(w, b.String())
	return err
}

// wrap writes text to b after prefix, breaking lines between words so none is
// wider than helpWidth, and indents each later line to prefix's width.
func wrap(b *strings.Builder, prefix, text string) {
	line := prefix
	for i, word := range strings.Fields(text) {
		switch {
		case i == 0:
			line += word
		case len(line)+1+len(word) > helpWidth:
			b.WriteString(line + "\n")
			line = strings.Repeat(" ", len(prefix)) + word
		default:
			line += " " + word
		}
	}
	b.WriteString(line + "\n")
}
