//go:build linux || darwin

// Tests of the help text and usage errors: every command and option listed
// once, descriptions in one column, and the line width.

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

// TestHelp checks that help goes to the output, opens with the version, fits
// helpWidth, and starts every description in one column, just past the
// longest name.
func TestHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"link", "--help"}, {"self", "use", "latest", "-h"}, {"help"}, {"help", "link"}, {"help", "--help"},
	} {
		var output, diagnostics bytes.Buffer
		if err := Run(context.Background(), args, nil, &output, &diagnostics); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		text := output.String()
		if diagnostics.Len() != 0 || !strings.HasPrefix(text, "homeskillet "+version.Current().Version+"\n") {
			t.Fatalf("%v: help did not open with the version on the output:\n%s\ndiagnostics: %s", args, text, diagnostics.String())
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
		if !maps.Equal(columns, want) || !maps.Equal(indents, want) {
			t.Errorf("%v: descriptions start in columns %v and continue in %v, not just past the longest name, %v", args, columns, indents, want)
		}
	}
}

// TestHelpOptions checks that help lists every option and command once, and
// lists an option under a command's heading only if no other command takes
// it (see scoped).
func TestHelpOptions(t *testing.T) {
	flags := newFlags(new(invocation))
	var listed []string
	for _, group := range optionGroups {
		for _, name := range group.names {
			if flags.Lookup(name) == nil {
				t.Errorf("help lists --%s, which is not an option", name)
			}
			if group.command != "" && !slices.Equal(scoped[name], []string{group.command}) {
				t.Errorf("help lists --%s under %s, but it applies to %v", name, group.command, scoped[name])
			}
			listed = append(listed, name)
		}
	}
	flags.VisitAll(func(f *flag.Flag) {
		if n := len(slices.DeleteFunc(slices.Clone(listed), func(name string) bool { return name != f.Name })); n != 1 {
			t.Errorf("help lists --%s %d times", f.Name, n)
		}
	})
	for _, command := range commands {
		if commandHelp[command] == "" {
			t.Errorf("help does not describe %s", command)
		}
	}
}

// TestUsageError checks that a mistyped option is reported with a pointer to
// --help, rather than buried under the whole help text, and that help refuses
// arguments it would otherwise ignore.
func TestUsageError(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"link", "--froce"}, "--help"},
		{[]string{"help", "lnik"}, "unexpected arguments"},
		{[]string{"help", "self", "list"}, "unexpected arguments"},
	} {
		var output, diagnostics bytes.Buffer
		err := Run(context.Background(), test.args, nil, &output, &diagnostics)
		if err == nil || !strings.Contains(err.Error(), test.want) || output.Len() != 0 || diagnostics.Len() != 0 {
			t.Errorf("%v: error %v, output %q, diagnostics %q", test.args, err, output.String(), diagnostics.String())
		}
	}
}
