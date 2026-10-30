//go:build linux || darwin || windows

// Colors for a person at a terminal: whether output gets them, and the
// standard every command follows. Output for scripts never gets them.

package cli

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// The color standard. Each style has one role, so a color means the same in
// every command, and colors only add to what the words already say: with
// colors off, as in a pipe, nothing is lost.
//
// Colors go around homeskillet's own words, such as a heading or the first
// words of a closing sentence, never around a path, a version, or another
// name, which keep the terminal's own color wherever they appear.
const (
	// styleHeading is bold: a command's title, and the headings of its
	// groups and sections.
	styleHeading = "\x1b[1m"
	// styleGood is green: what is done, in place, or ready.
	styleGood = "\x1b[32m"
	// styleAttention is yellow: what still needs doing or did not happen,
	// such as a dry run, a conflict, a pending step, or something missing.
	styleAttention = "\x1b[33m"
	// styleError is red: a failure.
	styleError = "\x1b[31m"
	// styleName is cyan: what help lists to type, its commands, options, and
	// arguments.
	styleName = "\x1b[36m"
	// styleReset ends a style.
	styleReset = "\x1b[0m"
)

// colorChoice is whether output gets colors, and why, as homeskillet term
// shows it.
type colorChoice struct {
	on     bool
	reason string
}

// colorFor decides whether w gets colors: only a terminal does, and not when
// NO_COLOR is set to anything (https://no-color.org), when TERM is dumb, or
// on Windows, when the console does not take color codes. Deciding may turn
// on a Windows console's color codes, once, for this process.
func colorFor(w io.Writer) colorChoice {
	file, ok := w.(*os.File)
	switch {
	case !ok || !isTerminal(file):
		return colorChoice{reason: "output is not a terminal"}
	case os.Getenv("NO_COLOR") != "":
		return colorChoice{reason: "NO_COLOR is set"}
	case os.Getenv("TERM") == "dumb":
		return colorChoice{reason: "TERM is dumb"}
	case !enableColor(file):
		return colorChoice{reason: "the console does not take color codes"}
	}
	return colorChoice{on: true, reason: "output is a terminal"}
}

// paint returns s in style, or in several styles joined, as styleHeading +
// styleGood, when on, and s as it is otherwise. Columns that hold painted
// text are measured with visibleWidth, since the codes take room in a string
// but none on the screen.
func paint(on bool, style, s string) string {
	if !on || s == "" {
		return s
	}
	return style + s + styleReset
}

// visibleWidth is the width of s on the screen: its characters, without the
// color codes paint adds.
func visibleWidth(s string) int {
	width := 0
	for s != "" {
		if rest, ok := strings.CutPrefix(s, "\x1b["); ok {
			if _, after, ok := strings.Cut(rest, "m"); ok {
				s = after
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		width++
	}
	return width
}

// dryRunLead is the start of every closing line of a dry run, "Dry run:", in the
// standard's color for what did not happen.
func dryRunLead(color bool) string {
	return paint(color, styleAttention, "Dry run:")
}

// paintRoles returns roles, a version's roles joined by ", " as list and
// list-remote show them, with current, the version that runs, and
// installed in the standard's color for what is in place; latest, previous,
// newest, and pre-release say what a version is, not whether it is done,
// and stay plain.
func paintRoles(color bool, roles string) string {
	if !color || roles == "" {
		return roles
	}
	words := strings.Split(roles, ", ")
	for i, word := range words {
		if word == "current" || word == "installed" {
			words[i] = paint(color, styleGood, word)
		}
	}
	return strings.Join(words, ", ")
}
