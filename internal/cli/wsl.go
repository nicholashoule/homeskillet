//go:build linux || darwin || windows

// The wsl command's arguments, and the hidden command through which the
// Linux build receives a repository copied from Windows (see package mirror).

package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/mirror"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
	"github.com/nicholashoule/homeskillet/internal/wsl"
)

// wslCommands are the commands wsl runs in a distribution.
var wslCommands = []string{"install", "status", "link", "unlink", "setup"}

// mirrorCommand is the hidden command wsl runs in a distribution to receive
// a copy of a repository on its standard input. It is not in commands, so
// help and errors never offer it.
const mirrorCommand = "__mirror"

// mirrorModes are what mirrorCommand does with the copy: update the one in
// the data directory, compare the stream with it and change nothing, or
// write it to a temporary directory.
var mirrorModes = []string{"update", "compare", "temporary"}

// mirrorsDir is the data directory's directory of copies.
const mirrorsDir = "mirrors"

// windowsBuildCommand is the hidden command wsl install runs in a
// distribution to record the Windows build that installed homeskillet there,
// so that homeskillet wsl run in the distribution can hand over to it (see
// handOff). It is not in commands, so help and errors never offer it.
const windowsBuildCommand = "__windows-build"

// windowsBuildFile is the data directory's record of the Windows build.
const windowsBuildFile = "windows-build.json"

// windowsBuild is the record windowsBuildCommand writes: the Windows build's
// program as Windows names it, to show, and as the distribution does, to run.
type windowsBuild struct {
	Windows string `json:"windows"`
	Path    string `json:"path"`
}

// valid reports whether b names a Windows program in full, both ways, with
// nothing in either name that could disguise what it shows.
func (b windowsBuild) valid() bool {
	exe := func(name string) bool { return len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") }
	return config.Printable(b.Windows) && config.Printable(b.Path) && exe(b.Windows) && exe(b.Path) && path.IsAbs(b.Path)
}

// parseWSL reads the arguments after wsl: a command (see wslCommands); then,
// except for install, a repository, a Windows clone to copy into the
// distribution or, written / or ~ first, a clone already there; and options.
// --distribution and --from are wsl's own. Every other option, and every
// argument after the repository, is passed on to homeskillet in the
// distribution, which checks them as it would its own, so they mean what
// they mean there: --home names a Linux directory. Options that take a value
// are passed on joined to it, so a value is never read as an option.
func parseWSL(args []string, output io.Writer) (invocation, error) {
	inv := invocation{command: "wsl", repo: ".", wsl: new(wslOptions)}
	inv.sub, args = word(args, "")
	if inv.sub == "" && slices.ContainsFunc(args, func(arg string) bool { return arg == "-h" || arg == "--help" }) {
		return invocation{}, help(output, newFlags(new(invocation)))
	}
	if !slices.Contains(wslCommands, inv.sub) {
		if inv.sub == "" {
			return invocation{}, fmt.Errorf("wsl needs a command: %s", strings.Join(wslCommands, ", "))
		}
		return invocation{}, fmt.Errorf("unknown command wsl %s; use wsl %s", inv.sub, strings.Join(wslCommands, ", wsl "))
	}
	known := newFlags(new(invocation))
	var operands []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			operands = append(operands, args[i:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			operands = append(operands, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "h" || name == "help" {
			return invocation{}, help(output, known)
		}
		f := known.Lookup(name)
		if f == nil {
			return invocation{}, fmt.Errorf("unknown option %s\n  run homeskillet --help to see the commands and options", arg)
		}
		boolean := isBool(f)
		if !boolean && !hasValue {
			if i+1 >= len(args) {
				return invocation{}, fmt.Errorf("option %s needs a value", arg)
			}
			i++
			value = args[i]
		}
		// As for every command (see parse): an empty value, as from an unset
		// variable, would otherwise mean the default.
		if !boolean && value == "" {
			return invocation{}, fmt.Errorf("--%s is empty, as from an unset variable; give a value, or leave the option out", name)
		}
		if name == "home" {
			value = inDistribution(value)
		}
		if err := inv.setWSLOption(name, value, boolean, hasValue); err != nil {
			return invocation{}, err
		}
		switch {
		case name == "distribution" || name == "from":
		// setup keeps nothing in the data directory, and refuses the option;
		// wsl reads it only to find the copy there.
		case name == "data-dir" && inv.sub == "setup":
		case boolean:
			inv.wsl.forward = append(inv.wsl.forward, arg)
		default:
			inv.wsl.forward = append(inv.wsl.forward, "--"+name+"="+value)
		}
	}
	return inv.wslOperands(operands)
}

// setWSLOption records the options wsl reads itself, among those it passes on.
func (inv *invocation) setWSLOption(name, value string, boolean, hasValue bool) error {
	on := !hasValue || value == "true"
	switch name {
	case "distribution":
		if !wsl.ValidName(value) {
			return errors.New("--distribution must name a distribution, as wsl --list shows it")
		}
		inv.wsl.distribution = value
	case "from":
		if inv.sub != "install" {
			return errors.New("--from applies only to wsl install")
		}
		inv.wsl.from = value
	case "repo":
		return fmt.Errorf("name the repository after the command: homeskillet wsl %s PATH", inv.sub)
	case "prefix":
		if inv.sub != "install" {
			return errors.New("--prefix applies only to wsl install")
		}
		inv.prefix = value
	case "home":
		inv.home = value
	case "data-dir":
		inv.data = value
	case "allow-root":
		inv.root = boolean && on
	case "dry-run":
		inv.dryRun = boolean && on
	case "quiet":
		inv.quiet = boolean && on
	}
	return nil
}

// wslOperands takes the repository from the first of operands, as parse does
// for the commands themselves, and passes the rest on.
func (inv invocation) wslOperands(operands []string) (invocation, error) {
	if inv.sub == "install" {
		if len(operands) > 0 {
			return invocation{}, fmt.Errorf("unexpected arguments: %s", strings.Join(operands, " "))
		}
		return inv, nil
	}
	// After setup, other words name steps, so there too the repository must
	// plainly be a path.
	if len(operands) > 0 && operands[0] != "--" && operands[0] != "" &&
		(inv.sub != "setup" || isPath(operands[0]) || linuxPath(operands[0])) {
		inv.repo, operands = operands[0], operands[1:]
		inv.repoGiven = true
	}
	if slices.Contains(operands, "") || inv.repo == "" {
		return invocation{}, errors.New("an argument is empty, as from an unset variable; name a path, or leave it out")
	}
	inv.wsl.forward = append(inv.wsl.forward, operands...)
	return inv, nil
}

// linuxPath reports whether a repository given to wsl is a path in the
// distribution, starting with / or ~, rather than a Windows clone to copy.
func linuxPath(p string) bool {
	return strings.HasPrefix(p, "/") || p == "~" || strings.HasPrefix(p, "~/")
}

// inDistribution returns p, a path in the distribution, as homeskillet there
// takes it when started in the user's home: ~ as . and ~/x as ./x, since no
// shell expands them.
func inDistribution(p string) string {
	if rest, ok := strings.CutPrefix(p, "~"); ok {
		return "." + rest
	}
	return p
}

// linuxCommand returns the homeskillet command in the distribution, relative
// to the user's home, where commands start, or below home when one is given.
func linuxCommand(home string) string {
	if home == "" {
		home = "."
	}
	return path.Join(inDistribution(home), ".local", "bin", install.Binary)
}

// mirrorName returns the name of the copy of the Windows clone repo in a
// distribution: the clone's own name, readable, and a hash of its whole
// path, so two clones with one name never share a copy.
func mirrorName(repo string) string {
	var name strings.Builder
	for _, r := range strings.ToLower(filepath.Base(repo)) {
		if 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '.' || r == '_' || r == '-' {
			name.WriteRune(r)
		} else {
			name.WriteByte('-')
		}
	}
	base := strings.TrimLeft(name.String(), ".-")
	if base == "" {
		base = "repository"
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.ToLower(repo))) // a hash.Hash never fails to write
	return fmt.Sprintf("%s-%08x", base, hash.Sum32())
}

// release reports whether v names a published release, such as v0.2.0, which
// has binaries to download, rather than a development build.
func release(v string) bool {
	return version.Valid(v) && !strings.ContainsAny(v, "-+")
}

// versionOf returns the version the output of homeskillet version names.
func versionOf(output string) string {
	first, _, _ := strings.Cut(output, "\n")
	v, _ := strings.CutPrefix(strings.TrimSpace(first), install.Binary+" ")
	return v
}

// installNotes returns what self install wrote in distribution, run there by
// wsl install, as wsl install shows it. wsl install goes on to choose which
// version runs (see selectInstalled), so self install's line about that would
// be out of date before anyone read it, and is left out. And self install
// checks the PATH wsl.exe gives it, which no shell startup file has set, so
// its note that a directory is missing from PATH says nothing about the
// user's shells, and becomes advice that claims nothing.
func installNotes(installed, distribution string) string {
	var notes strings.Builder
	for line := range strings.Lines(installed) {
		text := strings.TrimSuffix(line, "\n")
		bin, missing := missingFromPath(text)
		switch {
		case install.AboutRunning(text):
		case missing:
			_, _ = fmt.Fprintf(&notes, "note\tif a new shell in %s cannot run homeskillet, add %s to PATH in your shell startup file\n", distribution, bin) // a strings.Builder never fails to write
		default:
			notes.WriteString(line)
		}
	}
	return notes.String()
}

// missingFromPath returns the directory a line, without its newline, says is
// not on PATH, when it is the note notOnPathLine makes.
func missingFromPath(line string) (string, bool) {
	prefix, rest, _ := strings.Cut(notOnPathLine, "%s")
	middle, _, _ := strings.Cut(rest, "%s")
	dir, ok := strings.CutPrefix(line, prefix)
	if dir, _, found := strings.Cut(dir, middle); ok && found && dir != "" {
		return dir, true
	}
	return "", false
}

// parseMirror reads the arguments of mirrorCommand: a mode (see
// mirrorModes), the copy's name, for update the Windows clone it is a copy
// of, kept in inv.repo, and --home, --data-dir, and --allow-root, as wsl
// passes them on.
func parseMirror(args []string) (invocation, error) {
	inv := invocation{command: mirrorCommand}
	flags := newFlags(&inv)
	operands, err := parseFlags(flags, args)
	if err != nil {
		return invocation{}, err
	}
	var other error
	flags.Visit(func(f *flag.Flag) {
		if f.Name != "home" && f.Name != "data-dir" && f.Name != "allow-root" {
			other = fmt.Errorf("%s takes no --%s", mirrorCommand, f.Name)
		}
	})
	if other != nil {
		return invocation{}, other
	}
	if len(operands) < 2 || len(operands) > 3 || !slices.Contains(mirrorModes, operands[0]) || !mirror.ValidName(operands[1]) ||
		len(operands) == 3 && (operands[0] != "update" || !config.Printable(operands[2])) {
		return invocation{}, fmt.Errorf("usage: homeskillet %s %s NAME [SOURCE]", mirrorCommand, strings.Join(mirrorModes, "|"))
	}
	inv.sub, inv.operand = operands[0], operands[1]
	if len(operands) == 3 {
		inv.repo = operands[2]
	}
	return inv, nil
}

// copyRecord is what mirrorCommand records beside a copy it updates, as
// NAME.json in the data directory's mirrors: the Windows clone it is a copy
// of, and when, so that the Linux build can say what it is acting on (see
// describeCopy).
type copyRecord struct {
	Source  string    `json:"source"`
	Updated time.Time `json:"updated"`
}

// maxRecord bounds what is read of a record wsl keeps in the data directory.
const maxRecord = 64 << 10

// runMirror receives a copy of a repository on input, for mirrorCommand, and
// writes where it is, or for compare how it differs from the copy there is.
// Copies live in the data directory's mirrors, private like the rest of it,
// or for temporary in a new directory of their own.
func runMirror(inv invocation, input io.Reader, output io.Writer) error {
	if input == nil {
		return errors.New("no copy to receive on standard input")
	}
	parent, err := mirrorParent(inv)
	if err != nil {
		return err
	}
	if inv.sub == "compare" {
		copied := filepath.Join(parent, inv.operand)
		//nolint:gosec // G703: parseMirror took the name only if mirror.ValidName passed, so it stays a name in parent
		if _, err := os.Lstat(copied); errors.Is(err, fs.ErrNotExist) {
			// Read what is sent, so the sender finishes.
			if _, err := io.Copy(io.Discard, input); err != nil {
				return err
			}
			_, err := fmt.Fprintln(output, "none")
			return err
		}
		diff, err := mirror.Compare(input, copied)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "%d %d %d %s\n", diff.Added, diff.Changed, diff.Removed, copied)
		return err
	}
	updated, err := mirror.Update(input, parent, inv.operand)
	if err != nil {
		if inv.sub == "temporary" {
			err = errors.Join(err, os.RemoveAll(parent)) //nolint:gosec // G703: the temporary directory mirrorParent just made
		}
		return err
	}
	if inv.sub == "temporary" {
		_, err = fmt.Fprintln(output, updated.Path)
		return err
	}
	if inv.repo != "" {
		record := copyRecord{Source: inv.repo, Updated: time.Now().UTC().Truncate(time.Second)}
		err := safefile.Write(filepath.Join(parent, inv.operand+".json"), 0o600, func(w io.Writer) error {
			return json.NewEncoder(w).Encode(record)
		})
		if err != nil {
			return err
		}
	}
	if updated.Created {
		_, err = fmt.Fprintf(output, "new %s\n", updated.Path)
		return err
	}
	_, err = fmt.Fprintf(output, "%d %d %d %s\n", updated.Added, updated.Changed, updated.Removed, updated.Path)
	return err
}

// describeCopy returns, for the header of a command acting on repo, what to
// say when repo is a copy homeskillet wsl keeps of a Windows clone: whose
// copy it is, when it was last updated, and how to update it, so that a
// command run inside WSL never acts on an old copy unawares. It is empty for
// any other repository, or when the data directory cannot be found.
func describeCopy(inv invocation, home, repo string) string {
	data, err := dataDir(inv, home)
	if err != nil {
		return ""
	}
	mirrors := filepath.Join(data, mirrorsDir)
	if filepath.Dir(repo) != mirrors {
		return ""
	}
	text := "a Windows clone, kept by homeskillet wsl"
	var record copyRecord
	raw, err := safefile.Read(repo+".json", maxRecord)
	if err == nil && json.Unmarshal(raw, &record) == nil && config.Printable(record.Source) && !record.Updated.IsZero() {
		text = record.Source + ", updated " + record.Updated.Local().Format("2006-01-02 15:04")
	}
	return text + "; homeskillet wsl link updates it from Windows"
}

// mirrorParent returns the directory runMirror keeps the copy in, creating it
// private when it is missing.
func mirrorParent(inv invocation) (string, error) {
	if inv.sub == "temporary" {
		return os.MkdirTemp("", "homeskillet-mirror-")
	}
	return distributionData(inv, mirrorsDir)
}

// distributionData returns the directory dir in the data directory of the
// home wsl acts on in the distribution, creating both private when missing.
// Links lead into the copy there, and the record there names a program
// homeskillet runs, so whoever could change them could run code as the user.
func distributionData(inv invocation, dir string) (string, error) {
	home, err := distributionHome(inv)
	if err != nil {
		return "", err
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return "", err
	}
	if err := trust.CheckCreatable(data); err != nil {
		return "", err
	}
	parent := filepath.Join(data, dir)
	//nolint:gosec // G703: in the data directory, which dataDir and trust.CheckCreatable just checked
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return parent, trust.CheckDirectory(parent)
}

// distributionHome returns the home wsl acts on in the distribution: the
// user's own, or --home, relative to the user's own, as the commands wsl runs
// there take it, since they start there.
func distributionHome(inv invocation) (string, error) {
	homePath := inv.home
	if !filepath.IsAbs(homePath) {
		own, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		homePath = filepath.Join(own, homePath)
	}
	home, err := canonicalDirectory(homePath)
	if err != nil {
		return "", fmt.Errorf("home: %w", err)
	}
	if err := trust.CheckLocation(home); err != nil {
		return "", fmt.Errorf("home: %w", err)
	}
	return home, nil
}

// parseWindowsBuild reads the arguments of windowsBuildCommand: the Windows
// build as Windows names it, kept in inv.repo, and as the distribution does,
// kept in inv.operand, and --home, --data-dir, and --allow-root, as wsl
// install passes them on.
func parseWindowsBuild(args []string) (invocation, error) {
	inv := invocation{command: windowsBuildCommand}
	flags := newFlags(&inv)
	operands, err := parseFlags(flags, args)
	if err != nil {
		return invocation{}, err
	}
	var other error
	flags.Visit(func(f *flag.Flag) {
		if f.Name != "home" && f.Name != "data-dir" && f.Name != "allow-root" {
			other = fmt.Errorf("%s takes no --%s", windowsBuildCommand, f.Name)
		}
	})
	if other != nil {
		return invocation{}, other
	}
	if len(operands) != 2 || !(windowsBuild{Windows: operands[0], Path: operands[1]}).valid() {
		return invocation{}, fmt.Errorf("usage: homeskillet %s WINDOWS-PATH PATH", windowsBuildCommand)
	}
	inv.repo, inv.operand = operands[0], operands[1]
	return inv, nil
}

// runWindowsBuild records the Windows build, for windowsBuildCommand, in the
// data directory of the home wsl install installed homeskillet in.
func runWindowsBuild(inv invocation) error {
	data, err := distributionData(inv, ".")
	if err != nil {
		return err
	}
	record := windowsBuild{Windows: inv.repo, Path: inv.operand}
	return safefile.Write(filepath.Join(data, windowsBuildFile), 0o600, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(record)
	})
}

// readWindowsBuild returns the Windows build recorded in data, the data
// directory, or says how to record one.
func readWindowsBuild(data string) (windowsBuild, error) {
	var record windowsBuild
	raw, err := safefile.Read(filepath.Join(data, windowsBuildFile), maxRecord)
	if errors.Is(err, fs.ErrNotExist) {
		return record, errors.New("no Windows build is recorded here; run homeskillet wsl install from Windows once, and then homeskillet wsl works here too")
	}
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil || !record.valid() {
		return windowsBuild{}, errors.New("the record of the Windows build is damaged; run homeskillet wsl install from Windows again")
	}
	return record, nil
}
