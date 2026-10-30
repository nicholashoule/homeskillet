//go:build linux || darwin || windows

// Tests of the help text and usage errors: the overview and each command's
// own help, the options each lists against those parse accepts, descriptions
// in one column, and the line width.

package cli

import (
	"bytes"
	"context"
	"flag"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/version"
)

// TestHelp checks that help goes to the output, opens with the version for
// the overview and with what the command does for its own, fits helpWidth,
// and starts every description in one column, just past the longest name.
func TestHelp(t *testing.T) {
	overview := "homeskillet " + version.Current().Version + "\n"
	about := func(command string) string { return commandHelp[command] + ".\n" }
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, overview},
		{[]string{"-h"}, overview},
		{[]string{"help"}, overview},
		{[]string{"--home", "/tmp", "--help"}, overview},
		{[]string{"link", "--help"}, about("link")},
		{[]string{"./dotfiles", "-h"}, about("status")},
		{[]string{"use", "-h"}, about("use")},
		{[]string{"list-remote", "--help"}, about("list-remote")},
		{[]string{"self", "use", "latest", "-h"}, about("self")},
		{[]string{"self", "help"}, about("self")},
		{[]string{"backups", "help"}, about("backups")},
		{[]string{"backups", "purge", "--help"}, about("backups")},
		{[]string{"wsl", "help"}, about("wsl")},
		{[]string{"wsl", "--help"}, about("wsl")},
		{[]string{"wsl", "link", "--help"}, about("wsl")},
		{[]string{"prep", "-h"}, about("prepare")},
		{[]string{"help", "prep"}, about("prepare")},
		{[]string{"help", "--help"}, about("help")},
	}
	for _, command := range commands {
		tests = append(tests, struct {
			args []string
			want string
		}{[]string{"help", command}, about(command)})
	}
	for _, test := range tests {
		var output, diagnostics bytes.Buffer
		if err := Run(context.Background(), test.args, nil, &output, &diagnostics); err != nil {
			t.Fatalf("%v: %v", test.args, err)
		}
		text := output.String()
		if diagnostics.Len() != 0 || !strings.HasPrefix(text, test.want) {
			t.Fatalf("%v: help did not open with %q on the output:\n%s\ndiagnostics: %s", test.args, test.want, text, diagnostics.String())
		}
		longest, columns, indents := 0, map[int]bool{}, map[int]bool{}
		for line := range strings.SplitSeq(text, "\n") {
			if len(line) > helpWidth {
				t.Errorf("line wider than %d: %q", helpWidth, line)
			}
			rest, ok := strings.CutPrefix(line, "  ")
			switch {
			case !ok || strings.HasPrefix(rest, "homeskillet "):
			case strings.HasPrefix(rest, " "):
				indents[len(line)-len(strings.TrimLeft(line, " "))] = true
			default:
				name, _, _ := strings.Cut(rest, "  ")
				longest = max(longest, len(name))
				columns[len(line)-len(strings.TrimLeft(rest[len(name):], " "))] = true
			}
		}
		want := map[int]bool{2 + longest + 2: true}
		if longest == 0 {
			want = map[int]bool{}
		}
		if !maps.Equal(columns, want) || len(indents) > 0 && !maps.Equal(indents, want) {
			t.Errorf("%v: descriptions start in columns %v and continue in %v, not just past the longest name, %v", test.args, columns, indents, want)
		}
	}
}

// TestHelpOptions checks that help orders every option once, describes and
// shows how to run every command, and that each command's help lists exactly
// the options parse accepts for it, so help never offers one that is refused
// or leaves one out.
func TestHelpOptions(t *testing.T) {
	flags := newFlags(new(invocation))
	for _, name := range optionOrder {
		if flags.Lookup(name) == nil {
			t.Errorf("help lists --%s, which is not an option", name)
		}
	}
	flags.VisitAll(func(f *flag.Flag) {
		if n := len(slices.DeleteFunc(slices.Clone(optionOrder), func(name string) bool { return name != f.Name })); n != 1 {
			t.Errorf("optionOrder lists --%s %d times", f.Name, n)
		}
	})
	for name, allowed := range scoped {
		for _, command := range allowed {
			if !slices.Contains(commands, command) {
				t.Errorf("--%s is scoped to %q, which is not a command", name, command)
			}
		}
	}
	for _, command := range scoped["yes"] {
		if yesHelp[command] == "" {
			t.Errorf("help does not say what --yes approves for %s", command)
		}
	}
	for _, command := range commands {
		if commandHelp[command] == "" || len(commandUsage[command]) == 0 {
			t.Errorf("help does not describe %s, or how to run it", command)
		}
	}
	// What each command needs before its options, so that only the option
	// decides whether parse accepts the line.
	before := map[string][]string{"use": {"use", "latest"}, "remove": {"remove", "0.1.0"}, "self": {"self", "list"}}
	for _, command := range commands {
		if command == "wsl" || slices.Contains(optionless, command) {
			continue
		}
		listed := map[string]bool{}
		for _, row := range commandOptions(command, flags) {
			name, _, _ := strings.Cut(strings.TrimPrefix(row[0], "--"), " ")
			listed[name] = true
		}
		flags.VisitAll(func(f *flag.Flag) {
			args := slices.Clone(before[command])
			if args == nil {
				args = []string{command}
			}
			args = append(args, "--"+f.Name)
			if !isBool(f) {
				args[len(args)-1] += "=x"
			}
			_, err := parse(args, new(bytes.Buffer))
			refused := err != nil && strings.Contains(err.Error(), "applies only to")
			if listed[f.Name] == refused {
				t.Errorf("%s: help lists --%s: %v, but parse says %v", command, f.Name, listed[f.Name], err)
			}
		})
	}
}

// TestUsageError checks that a mistyped option is reported with a pointer to
// the command's help, rather than buried under the whole help text, and that
// help refuses arguments it would otherwise ignore.
func TestUsageError(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"link", "--froce"}, "run homeskillet help link to see its options"},
		{[]string{"wsl", "link", "--froce"}, "run homeskillet help wsl to see its options"},
		{[]string{"help", "lnk"}, "did you mean 'link'"},
		{[]string{"help", "lnik"}, "'lnik' is not a command"},
		{[]string{"help", "--froce"}, "is not a command"},
		{[]string{"help", "self", "list"}, "unexpected arguments: list"},
		{[]string{"self", "help", "list"}, "unexpected arguments: list"},
		{[]string{"self"}, "see 'homeskillet help self'"},
	} {
		var output, diagnostics bytes.Buffer
		err := Run(context.Background(), test.args, nil, &output, &diagnostics)
		if err == nil || !strings.Contains(err.Error(), test.want) || output.Len() != 0 || diagnostics.Len() != 0 {
			t.Errorf("%v: error %v, output %q, diagnostics %q", test.args, err, output.String(), diagnostics.String())
		}
	}
}

// TestHelpColors checks that help in color, the overview and every command's,
// has colors and says exactly what it says plain, wrapped at the same places:
// colors only add to the words.
func TestHelpColors(t *testing.T) {
	texts := map[string][2]string{"overview": {overview(false), overview(true)}}
	for _, command := range commands {
		texts[command] = [2]string{commandHelpText(command, false), commandHelpText(command, true)}
	}
	for name, text := range texts {
		if strings.Contains(text[0], "\x1b[") || !strings.Contains(text[1], styleName) {
			t.Errorf("%s: help has color codes without color, or none with it", name)
		}
		if got := uncolored(text[1]); got != text[0] {
			t.Errorf("%s: help in color differs from help without it:\n%s\nwant:\n%s", name, got, text[0])
		}
	}
}

// uncolored returns s without the color codes paint adds.
func uncolored(s string) string {
	var b strings.Builder
	for s != "" {
		if rest, ok := strings.CutPrefix(s, "\x1b["); ok {
			if _, after, ok := strings.Cut(rest, "m"); ok {
				s = after
				continue
			}
		}
		b.WriteByte(s[0])
		s = s[1:]
	}
	return b.String()
}
