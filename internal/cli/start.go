//go:build linux || darwin || windows

// What homeskillet, run alone, shows a person who has nothing to link yet:
// how to begin, step by step, from installing it to the first link, as on a
// machine where it has just been downloaded or where there is no dotfiles
// repository at all.

package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/setup"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// nothingLinked is openRepository's error when no repository was named, the
// current directory is not one, and none is linked into the home yet, which
// is where everyone starts (see startGuide).
type nothingLinked struct{ error }

func (e nothingLinked) Unwrap() error { return e.error }

// examples is where the starter repositories, one for each system, are.
const examples = project + "/tree/main/examples"

// startStep is a line of startGuide: a step, numbered unless it is an
// alternative to the one before, and the command that does it. A step
// without a command is a sentence, and its detail the line under it.
type startStep struct {
	label, command, detail string
	alternative            bool
}

// startGuide writes, for a person who ran homeskillet alone with nothing to
// act on, what it does and how to begin, each step a command to copy:
// checking the machine with prepare; installing it, when what runs is the
// download rather than the installed command; starting a repository with
// init, or cloning one; where files go in it; and linking, first as a dry
// run. The commands name a repository in the home, spelled out in full on
// Windows, where neither cmd nor Git expands ~.
func startGuide(w io.Writer, home string) error {
	color := colorFor(w).on
	name := func(words ...string) string { return paint(color, styleName, strings.Join(words, " ")) }
	repo, file, target := "~/dotfiles", "~/dotfiles/"+config.HomeDir+"/.gitconfig", "~/.gitconfig"
	if runtime.GOOS == "windows" {
		repo = filepath.Join(home, "dotfiles")
		file, target = filepath.Join(repo, config.HomeDir, ".gitconfig"), filepath.Join(home, ".gitconfig")
		repo, file, target = shellWord(repo), shellWord(file), shellWord(target)
	}
	// prepare comes first: on Windows, installing makes a link, which takes
	// the Developer Mode it turns on. Until homeskillet is installed, both
	// run as the download.
	steps := []startStep{{label: "Check this machine is ready", command: name(selfName(), "prepare")}}
	if self := selfName(); self != install.Binary {
		steps = append(steps, startStep{label: "Install it as a command", command: name(self, "install")})
	}
	steps = append(steps,
		startStep{label: "Start a repository", command: name(install.Binary, "init", repo)},
		startStep{label: "or clone the one you have", command: name("git", "clone", "URL", repo), alternative: true},
		startStep{label: "Put your dotfiles in its " + config.HomeDir + " folder, placed as they are in your home:",
			detail: file + " is linked to " + target},
		startStep{label: "Preview the links", command: name(install.Binary, "link", repo, "--dry-run")},
		startStep{label: "Link them", command: name(install.Binary, "link", repo)},
	)
	// Labels are numbered first, so that the commands line up across the
	// steps, sentences between them included.
	var table [][]string
	number := 0
	for i := range steps {
		if steps[i].alternative {
			steps[i].label = "   " + steps[i].label
		} else {
			number++
			steps[i].label = fmt.Sprintf("%d. %s", number, steps[i].label)
		}
		if steps[i].command != "" {
			table = append(table, []string{steps[i].label, steps[i].command})
		}
	}
	widths := columnWidths(table)
	var b strings.Builder
	b.WriteString(paint(color, styleHeading, "homeskillet "+version.Current().Version) + "\n")
	b.WriteString("Links dotfiles from a configuration repository into your home directory.\n\n")
	fmt.Fprintf(&b, "%s there is no repository here, and none is linked into %s yet.\n\n",
		paint(color, styleAttention, "Nothing to link:"), displayPath(home))
	b.WriteString(paint(color, styleHeading, "Get started") + "\n")
	for _, step := range steps {
		if step.command == "" {
			fmt.Fprintf(&b, "  %s\n     %s\n", step.label, step.detail)
			continue
		}
		if err := writeTable(&b, [][]string{{step.label, step.command}}, widths); err != nil {
			return err
		}
	}
	fmt.Fprintf(&b, "\nStarter repositories for each system: %s\nEvery command: %s\n", examples, name(install.Binary, "help"))
	_, err := io.WriteString(w, b.String())
	return err
}

// shellWord returns s as one word to paste into the shell homeskillet's
// advice is written for: PowerShell on Windows, as the Windows guide uses,
// and a POSIX shell elsewhere (see setup.Quote). On Windows that is in double
// quotes when it holds a space or another character PowerShell or cmd would
// read, and in single quotes, PowerShell's literal ones, when it holds $ or
// a backtick, which PowerShell expands even inside double quotes, so a
// folder named $work is not pasted as an empty name. A name with control or
// formatting characters is never safe to paste, so it is shown escaped, as
// displayPath shows it, to be read rather than run.
func shellWord(s string) string {
	switch {
	case !config.Printable(s):
		return displayPath(s)
	case runtime.GOOS != "windows":
		return setup.Quote([]string{s})
	case strings.ContainsAny(s, "$`"):
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case strings.ContainsAny(s, " &()[]{}^=;!'+,~%@#"):
		return `"` + s + `"`
	}
	return s
}

// commandWord returns program as the first word of a command to paste (see
// shellWord). PowerShell reads a quoted first word as a string, not as a
// program to run, so on Windows a quoted one gets the call operator, &.
func commandWord(program string) string {
	word := shellWord(program)
	if runtime.GOOS == "windows" && word != program {
		return "& " + word
	}
	return word
}
