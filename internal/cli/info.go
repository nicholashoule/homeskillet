//go:build linux || darwin || windows

// homeskillet info and homeskillet term: what homeskillet detects about where
// it runs. info is the whole report, to paste into a bug report: which
// homeskillet runs and from where, the system, and the terminal; term is the
// terminal alone. Each terminal finding says what it decides. info writes the
// home directory as ~, so a paste carries no user name, and neither shows
// anything else from the environment.

package cli

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// issuesURL is where info's closing line sends a person with a problem.
const issuesURL = project + "/issues/new"

// infoSection is one group of info's rows: a heading, and label and value
// pairs.
type infoSection struct {
	heading string
	rows    [][]string
}

// runInfo writes what homeskillet detects: which homeskillet runs, the
// system, and the terminal, then where to report a problem.
func runInfo(input io.Reader, output io.Writer) error {
	home, homes := infoHomes()
	return writeInfo(output, "homeskillet info", []infoSection{
		{"homeskillet", homeskilletRows(home, homes)},
		{"System", systemRows()},
		{"Terminal", terminalRows(input, output, homes)},
	}, "To report a problem, paste this into an issue: "+issuesURL)
}

// runTerm writes what homeskillet detects about the terminal alone; info
// adds the rest.
func runTerm(input io.Reader, output io.Writer) error {
	_, homes := infoHomes()
	return writeInfo(output, "homeskillet term", []infoSection{{"", terminalRows(input, output, homes)}}, "")
}

// infoHomes returns the home directory, canonical, as the install is found
// under it, and every spelling of it that a path shown may start with, for
// tilde: the canonical one and the one the environment gives, which differ
// where a symlink leads to the home, as /home leads to /var/home on Fedora
// Silverblue.
func infoHomes() (home string, homes []string) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	if home, err = canonicalDirectory(dir); err != nil {
		return "", []string{dir}
	}
	if home == dir {
		return home, []string{home}
	}
	return home, []string{home, dir}
}

// writeInfo writes sections: for a person, under title, each under its
// heading, in columns shared by every section, then closing when there is
// one; for a script, one tab-separated line per row.
func writeInfo(output io.Writer, title string, sections []infoSection, closing string) error {
	if !terminal(output) {
		for _, section := range sections {
			for _, row := range section.rows {
				if _, err := fmt.Fprintf(output, "%s\t%s\n", row[0], row[1]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	color := colorFor(output).on
	if _, err := fmt.Fprintf(output, "%s\n\n", paint(color, styleHeading, title)); err != nil {
		return err
	}
	var all [][]string
	for _, section := range sections {
		all = append(all, section.rows...)
	}
	widths := columnWidths(labeled(all))
	for i, section := range sections {
		if i > 0 {
			if _, err := fmt.Fprintln(output); err != nil {
				return err
			}
		}
		if section.heading != "" {
			if _, err := fmt.Fprintln(output, paint(color, styleHeading, section.heading)); err != nil {
				return err
			}
		}
		if err := writeTable(output, labeled(section.rows), widths); err != nil {
			return err
		}
	}
	if closing == "" {
		return nil
	}
	_, err := fmt.Fprintf(output, "\n%s\n", closing)
	return err
}

// labeled returns rows with a colon after each label, as a person reads them.
func labeled(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = []string{row[0] + ":", row[1]}
	}
	return out
}

// homeskilletRows describes this build and the installed command: the
// version, commit, and Go that built it, which version the command runs and
// whether it follows latest, and whether its directory is on PATH.
func homeskilletRows(home string, homes []string) [][]string {
	info := version.Current()
	built := info.Version
	if info.Revision != "" {
		commit := info.Revision
		if len(commit) > 7 {
			commit = commit[:7]
		}
		if info.Modified {
			commit += ", modified"
		}
		built += " (" + commit + ")"
	}
	if info.Go != "" {
		built += ", built with " + info.Go
	}
	runs := "nothing installed yet"
	command := "not on PATH"
	if home != "" {
		if layout, err := selfLayout(invocation{}, home); err == nil {
			runs = installedRow(layout)
			if _, err := os.Lstat(layout.Command()); err == nil && onPath(layout.Bin) {
				command = tilde(layout.Command(), homes...)
			}
		}
	}
	if path, err := exec.LookPath("homeskillet"); err == nil && command == "not on PATH" {
		command = tilde(path, homes...) + ", not the installed command"
	}
	return [][]string{{"version", built}, {"installed", runs}, {"command", command}}
}

// installedRow says which installed version runs, and whether it follows
// latest, or what is wrong: versions installed but none chosen, a command
// that points to a version no longer there, or versions that cannot be read,
// each with what repairs it, as a bug report needs it.
func installedRow(layout install.Layout) string {
	state, err := layout.Inspect()
	switch {
	case err != nil:
		return "could not be read: " + oneLine(err)
	case state.Broken != "":
		return "broken: the command points to " + brokenTarget(state) + ", which is not installed; " + selfName() + " use latest repairs it"
	case state.Current == "" && len(state.Versions) > 0:
		return "none chosen to run; " + selfName() + " use latest chooses the newest"
	case state.Current == "":
		return "nothing installed yet"
	case state.Following:
		return state.Current + ", following latest"
	}
	return state.Current + ", pinned"
}

// systemRows describes the operating system: its release and kernel, the
// platform, and WSL or a container when it runs in one.
func systemRows() [][]string {
	system := host.DescribeSystem()
	platform, translated := host.Detect()
	platformText := platform.String()
	if translated {
		platformText += ", running a " + host.Build().String() + " build"
	}
	rows := [][]string{
		{"os", cmp.Or(system.Name, "not detected")},
		{"kernel", cmp.Or(system.Kernel, "not detected")},
		{"platform", platformText},
	}
	if system.WSL != "" {
		rows = append(rows, []string{"wsl", system.WSL})
	}
	if system.Container != "" {
		rows = append(rows, []string{"container", system.Container})
	}
	return rows
}

// terminalRows describes the terminal and what each finding decides: whether
// output is a terminal, which decides between the view for a person and
// lines for scripts; whether input is one, which decides how a prompt gets
// its answer; whether colors are on and why; the locale; the terminal
// program; and the shell, the one to restart after linking.
func terminalRows(input io.Reader, output io.Writer, homes []string) [][]string {
	in, _ := input.(*os.File)
	color := colorFor(output)
	out := "not a terminal: a pipe or file, so results are one tab-separated line each, for scripts"
	if terminal(output) {
		out = "a terminal, so results are grouped for reading"
	}
	answers := "not a terminal: prompts read their answers from it, so echo y | approves and empty input declines"
	if in != nil && isTerminal(in) {
		answers = "a terminal, so homeskillet asks before it changes anything, and waits for your answer"
	}
	colors := "off: " + color.reason
	if color.on {
		colors = paint(true, styleGood, "on")
	}
	if os.Getenv("NO_COLOR") != "" && !strings.Contains(colors, "NO_COLOR") {
		colors += "; NO_COLOR is set"
	}
	return [][]string{
		{"output", out},
		{"input", answers},
		{"colors", colors},
		{"TERM", cmp.Or(os.Getenv("TERM"), "not set")},
		{"COLORTERM", cmp.Or(os.Getenv("COLORTERM"), "not set")},
		// The locale's character set decides whether a terminal shows names
		// that are not ASCII; LC_ALL wins over LANG when set.
		{"LANG", cmp.Or(os.Getenv("LC_ALL"), os.Getenv("LANG"), "not set")},
		{"program", terminalProgram()},
		{"shell", tilde(shellName(), homes...)},
	}
}

// terminalProgram names the terminal program from what terminals set in the
// environment: TERM_PROGRAM, as macOS's Terminal, iTerm2, VS Code, and
// WezTerm set, WT_SESSION for Windows Terminal, KONSOLE_VERSION for Konsole,
// or VTE_VERSION for GNOME Terminal and the other VTE terminals.
func terminalProgram() string {
	switch {
	case os.Getenv("TERM_PROGRAM") != "":
		return strings.TrimSpace(os.Getenv("TERM_PROGRAM") + " " + os.Getenv("TERM_PROGRAM_VERSION"))
	case os.Getenv("WT_SESSION") != "":
		return "Windows Terminal"
	case os.Getenv("KONSOLE_VERSION") != "":
		return "Konsole " + os.Getenv("KONSOLE_VERSION")
	case os.Getenv("VTE_VERSION") != "":
		return "a VTE terminal, such as GNOME Terminal, VTE " + os.Getenv("VTE_VERSION")
	}
	return "not detected"
}

// shellName names the shell: SHELL on Linux and macOS, the login shell to
// restart with exec "$SHELL" -l; on Windows, which PowerShell PSModulePath
// points at, or else the command processor. No shell is run to ask it.
func shellName() string {
	if runtime.GOOS != "windows" {
		return cmp.Or(os.Getenv("SHELL"), "not set")
	}
	modules := strings.ToLower(os.Getenv("PSModulePath"))
	switch {
	case strings.Contains(modules, `\powershell\7`):
		return "PowerShell 7"
	case strings.Contains(modules, `\windowspowershell\`):
		return "Windows PowerShell"
	}
	return cmp.Or(filepath.Base(os.Getenv("ComSpec")), "not detected")
}

// tilde returns path with the home directory written as ~, so what info
// prints carries no user name. homes are every spelling of the home (see
// infoHomes); on Windows, where paths ignore case, so does the match, as
// C:\Users\Alice and c:\users\alice are the same folder.
func tilde(path string, homes ...string) string {
	for _, home := range homes {
		if home == "" || len(path) < len(home) || !samePrefix(path[:len(home)], home) {
			continue
		}
		switch rest := path[len(home):]; {
		case rest == "":
			return "~"
		case rest[0] == filepath.Separator:
			return "~" + rest
		}
	}
	return path
}

// samePrefix reports whether a and b, the start of a path and a home, are
// the same, ignoring case on Windows.
func samePrefix(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
