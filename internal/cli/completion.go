//go:build linux || darwin

// Shell completion: the bash and zsh scripts the completion command prints,
// and the hidden __complete command they call. Candidates come from the same
// commands, options, manifests, and installed versions the rest of the CLI
// uses, and working them out only reads.

package cli

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// completeCommand is the hidden command the completion scripts run. It is
// not in commands, so help and errors never offer it.
const completeCommand = "__complete"

// completionScripts holds the script completion writes for each shell: bash
// and zsh, the shells homeskillet supports (see the README). Each is a thin
// wrapper that asks homeskillet for its candidates (see complete), so
// completion matches the installed version and needs no second copy of the
// commands and options to keep in step.
var completionScripts = map[string]string{"bash": bashCompletion, "zsh": zshCompletion}

// bashCompletion is the bash script. It avoids what bash 4 added, such as
// mapfile and compopt, so it also works with bash 3.2, which macOS ships.
const bashCompletion = `# bash completion for homeskillet, written by: homeskillet completion bash
# Copyright 2026 nicholashoule
# SPDX-License-Identifier: Apache-2.0
# Load it from ~/.bashrc:
#   command -v homeskillet >/dev/null && eval "$(homeskillet completion bash)"
# homeskillet works out the candidates itself, so they match the installed
# version. It works with bash 3.2, which macOS ships, and later.
_homeskillet() {
    local cur word directive marked
    local -a reply
    # Assigned, not just declared, so set -u counts it as set when empty.
    reply=()
    cur=${COMP_WORDS[COMP_CWORD]}
    # bash splits --option=value at the =, so the value is completed alone.
    if [[ $cur == = ]]; then
        cur=
    fi
    COMPREPLY=()
    while IFS= read -r word; do
        reply+=("$word")
    done < <(homeskillet __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null)
    directive=${reply[0]-}
    # Before bash 4.4, an empty array counts as unset under set -u.
    if ((${#reply[@]} > 1)); then
        for word in "${reply[@]:1}"; do
            COMPREPLY+=("$word")
        done
    fi
    case $directive in
        :dirs | :files)
            # Names on disk get filename handling: quoting of spaces, and a
            # slash after a directory. compopt turns it on for this one
            # completion where bash has it (4.0 and later); bash 3.2, which
            # macOS ships, gets the same by hand. It is never on for commands
            # and step names, so a directory called setup cannot turn the
            # setup command into the path setup/.
            if ! compopt -o filenames 2>/dev/null; then
                marked=yes
            fi
            while IFS= read -r word; do
                if [[ -n ${marked-} ]]; then
                    if [[ -d $word ]]; then
                        word=$word/
                    fi
                    printf -v word '%q' "$word"
                fi
                COMPREPLY+=("$word")
            done < <(if [[ $directive == :dirs ]]; then compgen -d -- "$cur"; else compgen -f -- "$cur"; fi)
            ;;
    esac
}
complete -F _homeskillet homeskillet
`

// zshCompletion is the zsh script, usable with eval or saved on fpath.
const zshCompletion = `#compdef homeskillet
# zsh completion for homeskillet, written by: homeskillet completion zsh
# Copyright 2026 nicholashoule
# SPDX-License-Identifier: Apache-2.0
# Load it from ~/.zshrc, after compinit:
#   command -v homeskillet >/dev/null && eval "$(homeskillet completion zsh)"
# or save it as _homeskillet in a directory on fpath. homeskillet works out
# the candidates itself, so they match the installed version.
_homeskillet() {
    local -a reply candidates
    local directive
    reply=("${(@f)$(homeskillet __complete "${(@)words[2,CURRENT]}" 2>/dev/null)}")
    directive=${reply[1]}
    candidates=("${(@)reply[2,-1]}")
    # zsh keeps --option=value as one word, so the value is completed alone.
    if [[ ${words[CURRENT]} == -*=* ]]; then
        compset -P '*='
    fi
    case $directive in
        :dirs) _files -/ ;;
        :files) compadd -a candidates; _files ;;
        *) compadd -a candidates ;;
    esac
}
if [[ ${funcstack[1]} == _homeskillet ]]; then
    _homeskillet "$@"
elif (( $+functions[compdef] )); then
    compdef _homeskillet homeskillet
fi
`

// writeCompletion writes the completion script for shell, for completion.
func writeCompletion(shell string, output io.Writer) error {
	script, ok := completionScripts[shell]
	if !ok {
		return fmt.Errorf("no completion for %q; use %s", shell, strings.Join(slices.Sorted(maps.Keys(completionScripts)), " or "))
	}
	_, err := io.WriteString(output, script)
	return err
}

// complete writes the completions for words, the command line after the
// program name ending with the word being completed, for the scripts
// completion writes. The first line tells the script what it should add
// itself: ":files" or ":dirs" for names on disk, or ":none". Each further
// line is a candidate that starts with the word being completed.
//
// It reads only what status and self list would, never runs a setup step,
// and fails quietly: a shell completing a word has nowhere to show an error.
func complete(words []string, output io.Writer) error {
	directive, candidates := completions(words)
	var text strings.Builder
	text.WriteString(directive + "\n")
	for _, candidate := range candidates {
		text.WriteString(candidate + "\n")
	}
	_, err := io.WriteString(output, text.String())
	return err
}

// completionLine is a command line being completed, read as parse would
// read it.
type completionLine struct {
	inv        invocation    // the options given so far
	flags      *flag.FlagSet // the options, set to write into inv
	positional []string      // the words before the last that are not options
	pending    string        // an option whose value is the word being completed
	terminated bool          // whether "--" came before the word being completed
	current    string        // the word being completed
}

// readCompletionLine reads words, all but the last as parse would, collecting
// options and the words that are not options.
func readCompletionLine(words []string) *completionLine {
	words = joinValues(words)
	line := &completionLine{}
	line.flags = newFlags(&line.inv)
	if len(words) > 0 {
		line.current, words = words[len(words)-1], words[:len(words)-1]
	}
	set := func(name, value string) {
		line.inv.repoGiven = line.inv.repoGiven || name == "repo"
		_ = line.flags.Set(name, value)
	}
	for _, word := range words {
		switch {
		case line.pending != "":
			set(line.pending, word)
			line.pending = ""
		case line.terminated || word == "-" || !strings.HasPrefix(word, "-"):
			line.positional = append(line.positional, word)
		case word == "--":
			line.terminated = true
		default:
			name, value, hasValue := strings.Cut(strings.TrimLeft(word, "-"), "=")
			switch f := line.flags.Lookup(name); {
			case f == nil:
			case hasValue:
				set(name, value)
			case isBool(f):
				set(name, "true")
			default:
				line.pending = name
			}
		}
	}
	return line
}

// completions returns the directive and candidates for words (see complete):
// a value for the option before the last word, an option, or else whatever
// the command takes where the last word stands.
func completions(words []string) (string, []string) {
	line := readCompletionLine(words)
	if line.pending != "" {
		return optionValues(line.pending, line.current)
	}
	command, rest := "", line.positional
	if len(rest) > 0 {
		command, rest = rest[0], rest[1:]
		if !slices.Contains(commands, command) {
			// A path in place of a command is the repository for status.
			if !isPath(command) {
				return ":none", nil
			}
			command, rest = "status", line.positional
		}
	}
	if !line.terminated && strings.HasPrefix(line.current, "-") {
		name, value, hasValue := strings.Cut(strings.TrimLeft(line.current, "-"), "=")
		if !hasValue {
			return ":none", options(command, line.current)
		}
		if f := line.flags.Lookup(name); f != nil && !isBool(f) {
			return optionValues(name, value)
		}
		return ":none", nil
	}
	return arguments(line.inv, command, rest, line.current)
}

// arguments returns the directive and candidates for current, a word that is
// not an option, after command and the other such words in rest.
func arguments(inv invocation, command string, rest []string, current string) (string, []string) {
	switch {
	case command == "":
		// The first word is a command, or for status a repository, which must
		// plainly be a path there (see isPath). Directories are offered only
		// once the word looks like a path, so they never crowd out the
		// commands.
		if pathLike(current) {
			return ":dirs", nil
		}
		return ":none", matching(commands, current)
	case slices.Contains(selecting, command):
		// The repository, a directory, then the files to act on.
		if !inv.repoGiven && len(rest) == 0 {
			return ":dirs", nil
		}
		return ":files", nil
	case command == "setup":
		// Step names, after a repository that must plainly be a path here,
		// so only a word that looks like one completes as a directory.
		if !inv.repoGiven && len(rest) == 0 && pathLike(current) {
			return ":dirs", nil
		}
		if !inv.repoGiven && len(rest) > 0 && isPath(rest[0]) {
			inv.repo, inv.repoGiven, rest = rest[0], true, rest[1:]
		}
		names := slices.DeleteFunc(stepNames(inv), func(name string) bool { return slices.Contains(rest, name) })
		return ":none", matching(names, current)
	case command == "init" && len(rest) == 0:
		return ":dirs", nil
	case command == "self" && len(rest) == 0:
		return ":none", matching(selfCommands, current)
	case command == "self" && len(rest) == 1 && rest[0] == "use":
		return ":none", matching(append([]string{install.Latest, install.Previous}, installedVersions(inv)...), current)
	case command == "self" && len(rest) == 1 && rest[0] == "remove":
		return ":none", matching(installedVersions(inv), current)
	case command == "help" && len(rest) == 0:
		return ":none", matching(commands, current)
	case command == "completion" && len(rest) == 0:
		return ":none", matching(slices.Sorted(maps.Keys(completionScripts)), current)
	}
	return ":none", nil
}

// pathLike reports whether word, partly typed, is heading for a path rather
// than a command or setup step name, neither of which starts with ".", "/",
// or "~" or contains "/".
func pathLike(word string) bool {
	return strings.HasPrefix(word, ".") || strings.HasPrefix(word, "/") || strings.HasPrefix(word, "~") ||
		strings.Contains(word, "/")
}

// joinValues puts back together an option and its value that bash split at
// "=", as in --home = /tmp, so --home=/tmp reads as one word, as zsh keeps it.
func joinValues(words []string) []string {
	var joined []string
	for i := 0; i < len(words); i++ {
		if last := len(joined) - 1; words[i] == "=" && last >= 0 && strings.HasPrefix(joined[last], "-") && !strings.Contains(joined[last], "=") {
			joined[last] += "="
			if i+1 < len(words) {
				i++
				joined[last] += words[i]
			}
			continue
		}
		joined = append(joined, words[i])
	}
	return joined
}

// options returns the options command takes that start with current, in the
// same one-dash or two-dash form current uses. Without a command, they are
// status's, which is what runs.
func options(command, current string) []string {
	if command == "version" || command == "help" || command == "completion" {
		return nil
	}
	scope := cmp.Or(command, "status")
	dash := "--"
	if len(current) > 1 && current[1] != '-' {
		dash = "-"
	}
	names := []string{dash + "help"}
	newFlags(new(invocation)).VisitAll(func(f *flag.Flag) {
		if allowed, ok := scoped[f.Name]; !ok || slices.Contains(allowed, scope) {
			names = append(names, dash+f.Name)
		}
	})
	return matching(names, current)
}

// optionValues completes the value of option name: a platform for
// --platform, and a directory for every other option that takes a value.
func optionValues(name, current string) (string, []string) {
	if name == "platform" {
		return ":none", matching(config.PlatformValues(), current)
	}
	return ":dirs", nil
}

// matching returns the candidates that start with prefix, leaving out any a
// script could not show on one line.
func matching(candidates []string, prefix string) []string {
	var kept []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, prefix) && config.Printable(candidate) {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// isBool reports whether f is a flag that takes no value.
func isBool(f *flag.Flag) bool {
	boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && boolean.IsBoolFlag()
}

// stepNames returns the names of the setup steps for the platform setup
// would act for, in the repository inv names or else the one setup would use
// (see openRepository). The names are validated when the manifest is read,
// so they are safe to print.
func stepNames(inv invocation) []string {
	home, ok := completionHome(inv)
	if !ok {
		return nil
	}
	inv.command = "setup"
	_, manifest, _, err := openRepository(inv, home)
	if err != nil {
		return nil
	}
	platform, _ := host.Detect()
	if inv.platform != "" {
		if platform, err = config.ParsePlatform(inv.platform); err != nil {
			return nil
		}
	}
	var names []string
	for _, step := range manifest.SetupFor(platform) {
		names = append(names, step.Name)
	}
	return names
}

// installedVersions returns the versions self list would show.
func installedVersions(inv invocation) []string {
	home, ok := completionHome(inv)
	if !ok {
		return nil
	}
	layout, err := selfLayout(inv, home)
	if err != nil {
		return nil
	}
	state, err := layout.Inspect()
	if err != nil {
		return nil
	}
	return state.Versions
}

// completionHome returns the home directory a command would use, as Run
// resolves it. Running as root, completion reads nothing without
// --allow-root, as every command refuses root without it.
func completionHome(inv invocation) (string, bool) {
	if os.Geteuid() == 0 && !inv.root {
		return "", false
	}
	path := inv.home
	if path == "" {
		var err error
		if path, err = os.UserHomeDir(); err != nil {
			return "", false
		}
	}
	home, err := canonicalDirectory(path)
	return home, err == nil && trust.CheckLocation(home) == nil
}
