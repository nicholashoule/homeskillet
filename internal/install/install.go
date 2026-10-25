//go:build linux || darwin

// Package install keeps several versions of the homeskillet binary side by
// side and uses symlinks to choose which one runs, so an update never
// rewrites a binary in place and a bad update is undone by choosing another
// version.
//
// Under a prefix such as ~/.local, the layout is:
//
//	share/homeskillet/versions/v0.0.1/homeskillet
//	share/homeskillet/versions/v0.0.2/homeskillet
//	share/homeskillet/versions/v0.0.3/homeskillet
//	share/homeskillet/latest   -> versions/v0.0.3
//	share/homeskillet/previous -> versions/v0.0.2
//	share/homeskillet/current  -> latest
//	bin/homeskillet            -> <prefix>/share/homeskillet/current/homeskillet
//
// latest and previous always name the highest and second-highest installed
// versions by semantic version precedence, a clean build ranking above a
// +dirty build of the same version, and every change recomputes them.
// current chooses what bin/homeskillet runs. By default it follows latest, so
// installing a newer version updates the command. Pinning current to a
// version (see Layout.Use) keeps that version until current is changed again.
//
// The command stays usable throughout a change. A new binary is written under
// a temporary name and renamed into place, and each link is replaced by
// renaming a new link over it, so bin/homeskillet always resolves to a
// complete binary. Installed versions are never rewritten: installing a
// version again with different contents is refused, except for builds marked
// +dirty, which by definition do not name one exact build.
package install

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
)

const (
	// Binary is the program's file name, in Bin and in each version directory.
	Binary = "homeskillet"
	// Latest names the link to the highest installed version.
	Latest = "latest"
	// Previous names the link to the second-highest installed version.
	Previous = "previous"

	current  = "current"
	versions = "versions"
	// tempPrefix starts the names of binaries still being written. Remove
	// deletes leftovers from an interrupted install along with the binary.
	tempPrefix = "." + Binary + "-"
)

// Layout locates an installation. Both paths are absolute, with symlinks
// resolved in the parts that exist.
type Layout struct {
	// Bin is the directory, normally on PATH, that holds the homeskillet
	// command.
	Bin string
	// Data holds the installed versions and the latest, previous, and
	// current links.
	Data string
}

// New returns the layout under prefix, such as ~/.local. prefix must be
// absolute. Symlinks are resolved where the paths exist, because the
// ownership checks and the bin link compare and store path strings.
func New(prefix string) (Layout, error) {
	if !filepath.IsAbs(prefix) {
		return Layout{}, fmt.Errorf("prefix %s is not absolute", prefix)
	}
	bin, err := resolve(filepath.Join(prefix, "bin"))
	if err != nil {
		return Layout{}, err
	}
	data, err := resolve(filepath.Join(prefix, "share", Binary))
	if err != nil {
		return Layout{}, err
	}
	return Layout{Bin: bin, Data: data}, nil
}

// Command returns the path of the homeskillet command in Bin.
func (l Layout) Command() string {
	return filepath.Join(l.Bin, Binary)
}

// Program returns the path of installed version v's binary.
func (l Layout) Program(v string) string {
	return filepath.Join(l.Data, versions, v, Binary)
}

// commandTarget is what the homeskillet command in Bin links to. It goes
// through current, so choosing a version never touches Bin.
func (l Layout) commandTarget() string {
	return filepath.Join(l.Data, current, Binary)
}

// State is an installation as Inspect found it.
type State struct {
	// Versions lists the installed versions, lowest precedence first.
	Versions []string
	// Current is the version that runs, or "" if none is chosen yet.
	Current string
	// Following reports whether current follows latest rather than being
	// pinned to one version.
	Following bool
}

// Latest returns the highest installed version, or "" if there is none.
func (s State) Latest() string {
	return s.fromTop(1)
}

// Previous returns the second-highest installed version, or "" if there is
// none.
func (s State) Previous() string {
	return s.fromTop(2)
}

func (s State) fromTop(n int) string {
	if len(s.Versions) < n {
		return ""
	}
	return s.Versions[len(s.Versions)-n]
}

// Inspect reports the installed versions and the version current chooses.
// It changes nothing. Entries under versions that homeskillet did not create,
// such as a name that is not a version or a version without a binary, are
// ignored.
func (l Layout) Inspect() (State, error) {
	var state State
	entries, err := os.ReadDir(filepath.Join(l.Data, versions))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return State{}, err
	}
	for _, entry := range entries {
		v := entry.Name()
		// DirEntry types come from lstat, so a symlinked version directory
		// is skipped rather than followed.
		if !entry.IsDir() || !version.Valid(v) {
			continue
		}
		info, err := os.Lstat(l.Program(v))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return State{}, err
		}
		if info.Mode().IsRegular() {
			state.Versions = append(state.Versions, v)
		}
	}
	slices.SortFunc(state.Versions, order)
	link := filepath.Join(l.Data, current)
	target, err := os.Readlink(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return State{}, fmt.Errorf("%s must be a symlink: %w", link, err)
	case target == Latest:
		state.Following, state.Current = true, state.Latest()
	default:
		v, ok := strings.CutPrefix(target, versions+string(filepath.Separator))
		if !ok || !slices.Contains(state.Versions, v) {
			return State{}, fmt.Errorf("%s points to %q, not an installed version; choose one with homeskillet self use", link, target)
		}
		state.Current = v
	}
	return state, nil
}

// order sorts versions by precedence. Versions of equal precedence differ
// only in build metadata. A dirty build, such as v0.0.1+dirty made from the
// v0.0.1 tag with uncommitted changes, ranks below the clean build, so latest
// prefers the build a version actually names. Other ties fall back to byte
// order, so the result never depends on directory order.
func order(a, b string) int {
	if c := version.Compare(a, b); c != 0 {
		return c
	}
	if da, db := version.Dirty(a), version.Dirty(b); da != db {
		if da {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// Install copies the program at executable into the installation as version
// v and recomputes latest and previous. It creates current, following
// latest, and the homeskillet command in Bin if they do not exist yet.
//
// It writes one line per change to log. With dryRun it writes the same lines
// and changes nothing. Every check runs before the first change: the program
// must pass trust.CheckProgram, the directories it writes into must pass
// trust.CheckDirectory, and an existing file at the command's path that is
// not homeskillet's link is a conflict, never replaced.
func (l Layout) Install(executable, v string, dryRun bool, log io.Writer) error {
	if !version.Valid(v) {
		// make install always rebuilds, so the remedy names it rather than
		// make build. Tagging is preferred over stamping in a checkout: a
		// stamped v0.0.1 differs from the later tagged build of v0.0.1, and
		// installed versions are never rewritten.
		return fmt.Errorf("cannot install version %q: the build records no version; commit and tag the checkout (git tag v0.0.1), or where there is no Git metadata, run make install VERSION=v0.0.1", v)
	}
	source, err := os.Open(executable) //nolint:gosec // G304: copying the running program is the purpose
	if err != nil {
		return err
	}
	err = l.install(source, v, dryRun, log)
	return errors.Join(err, source.Close())
}

func (l Layout) install(source *os.File, v string, dryRun bool, log io.Writer) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if err := trust.CheckProgram(source.Name(), info); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(l.Program(v)), l.Bin} {
		existing, err := existingAncestor(dir)
		if err != nil {
			return err
		}
		if err := trust.CheckDirectory(existing); err != nil {
			return err
		}
	}
	state, err := l.Inspect()
	if err != nil {
		return err
	}
	action, err := l.programAction(source, v)
	if err != nil {
		return err
	}
	createCommand, err := l.commandMissing()
	if err != nil {
		return err
	}
	after := state.Versions
	if !slices.Contains(after, v) {
		after = append(slices.Clone(after), v)
		slices.SortFunc(after, order)
	}
	links, err := l.linkChanges(after)
	if err != nil {
		return err
	}

	// Every check has passed; from here on, only changes.
	if err := say(log, "%s\t%s\t%s\n", action, v, l.Program(v)); err != nil {
		return err
	}
	if action != "unchanged" && !dryRun {
		if err := copyProgram(source, l.Program(v)); err != nil {
			return err
		}
	}
	if err := applyLinks(links, dryRun, log); err != nil {
		return err
	}
	if state.Current == "" && !state.Following {
		if err := say(log, "current\t%s\n", Latest); err != nil {
			return err
		}
		if !dryRun {
			if err := replaceLink(Latest, filepath.Join(l.Data, current)); err != nil {
				return err
			}
		}
	}
	// Say when the version just installed is not the one that will run, so
	// an install never looks like an update that did not happen.
	switch latest := (State{Versions: after}).Latest(); {
	case state.Current != "" && !state.Following:
		if err := say(log, "pinned\t%s\trun homeskillet self use latest to follow the newest version\n", state.Current); err != nil {
			return err
		}
	case latest != v:
		if err := say(log, "note\t%s still runs because it is latest; homeskillet self use %s runs %s\n", latest, v, v); err != nil {
			return err
		}
	}
	if createCommand {
		if err := say(log, "linked\t%s -> %s\n", l.Command(), l.commandTarget()); err != nil {
			return err
		}
		if !dryRun {
			if err := os.MkdirAll(l.Bin, 0o700); err != nil {
				return err
			}
			// os.Symlink never replaces an existing file, so a command
			// created since the check makes this fail rather than be lost.
			return os.Symlink(l.commandTarget(), l.Command())
		}
	}
	return nil
}

// programAction decides what Install does with version v's binary:
// "installed" when it is new, "unchanged" when the same bytes are already
// installed, or "replaced" for a +dirty build with new contents. Rewriting
// any other installed version is refused, because a version that silently
// changed would no longer identify what is running.
func (l Layout) programAction(source *os.File, v string) (string, error) {
	path := l.Program(v)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "installed", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	same, err := sameContents(source, path)
	switch {
	case err != nil:
		return "", err
	case same:
		return "unchanged", nil
	case version.Dirty(v):
		return "replaced", nil
	default:
		return "", fmt.Errorf("%s is already installed with different contents; installed versions are never rewritten, so remove it first with homeskillet self remove %s", v, v)
	}
}

// commandMissing reports whether the homeskillet command in Bin still has to
// be created. Anything already there other than homeskillet's own link, such
// as a binary from go install or a link to a repository build, is a conflict:
// it is reported, never replaced.
func (l Layout) commandMissing() (bool, error) {
	path := l.Command()
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(path); err == nil && target == l.commandTarget() {
			return false, nil
		}
	}
	return false, fmt.Errorf("%s already exists and is not homeskillet's link to %s; move it aside, then install again", path, l.commandTarget())
}

// Use chooses what the homeskillet command runs. Latest follows the highest
// installed version from then on. Previous, or a version number, pins that
// version until Use is called again. Previous is resolved when Use runs, so
// installing another version later does not change what runs.
//
// It writes the choice to log. With dryRun it writes the same line and
// changes nothing.
func (l Layout) Use(choice string, dryRun bool, log io.Writer) error {
	if err := l.checkData(); err != nil {
		return err
	}
	state, err := l.Inspect()
	if err != nil {
		return err
	}
	pinned := choice
	switch {
	case choice == Latest:
		if state.Latest() == "" {
			return errors.New("no versions are installed")
		}
		pinned = ""
	case choice == Previous:
		if pinned = state.Previous(); pinned == "" {
			return errors.New("no previous version is installed")
		}
	case !version.Valid(choice):
		return fmt.Errorf("%q is not %s, %s, or a version such as v0.0.1", choice, Latest, Previous)
	case !slices.Contains(state.Versions, choice):
		return fmt.Errorf("%s is not installed; homeskillet self list shows what is", choice)
	}
	target, line := Latest, Latest+" ("+state.Latest()+")"
	if pinned != "" {
		target, line = filepath.Join(versions, pinned), pinned+" (pinned)"
	}
	if err := say(log, "current\t%s\n", line); err != nil || dryRun {
		return err
	}
	return replaceLink(target, filepath.Join(l.Data, current))
}

// Remove deletes installed version v and recomputes latest and previous. It
// refuses to remove the version that runs, which must first be changed with
// Use. It deletes only what Install creates, so a version directory holding
// anything else is left alone and reported.
//
// It writes one line per change to log. With dryRun it writes the same lines
// and changes nothing.
func (l Layout) Remove(v string, dryRun bool, log io.Writer) error {
	if !version.Valid(v) {
		return fmt.Errorf("%q is not a version such as v0.0.1", v)
	}
	if err := l.checkData(); err != nil {
		return err
	}
	state, err := l.Inspect()
	if err != nil {
		return err
	}
	if !slices.Contains(state.Versions, v) {
		return fmt.Errorf("%s is not installed; homeskillet self list shows what is", v)
	}
	if v == state.Current {
		return fmt.Errorf("%s is the version that runs; choose another first with homeskillet self use", v)
	}
	dir := filepath.Dir(l.Program(v))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if name := entry.Name(); name != Binary && !strings.HasPrefix(name, tempPrefix) {
			return fmt.Errorf("%s contains %q, which homeskillet did not create; remove the directory by hand", dir, name)
		}
	}
	links, err := l.linkChanges(slices.DeleteFunc(slices.Clone(state.Versions), func(s string) bool { return s == v }))
	if err != nil {
		return err
	}

	// Links move first, so latest and previous never name a missing version.
	if err := applyLinks(links, dryRun, log); err != nil {
		return err
	}
	if err := say(log, "removed\t%s\t%s\n", v, dir); err != nil || dryRun {
		return err
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(dir)
}

// checkData requires an existing, private data directory before Use or
// Remove changes anything in it.
func (l Layout) checkData() error {
	if _, err := os.Lstat(l.Data); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("nothing is installed in %s", l.Data)
	}
	return trust.CheckDirectory(l.Data)
}

// linkChange points one of the latest and previous links at a version, or
// removes it when there is no version to name.
type linkChange struct {
	name, version string
	path, target  string
}

// linkChanges lists the changes that make latest and previous name the
// highest and second-highest of installed, which is sorted by order. An
// existing file at either path that is not a symlink is an error.
func (l Layout) linkChanges(installed []string) ([]linkChange, error) {
	state := State{Versions: installed}
	var changes []linkChange
	for name, v := range map[string]string{Latest: state.Latest(), Previous: state.Previous()} {
		change := linkChange{name: name, version: v, path: filepath.Join(l.Data, name)}
		if v != "" {
			change.target = filepath.Join(versions, v)
		}
		target, err := os.Readlink(change.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s must be a symlink: %w", change.path, err)
		}
		if target != change.target {
			changes = append(changes, change)
		}
	}
	// Map order is random; report latest before previous every time.
	slices.SortFunc(changes, func(a, b linkChange) int { return strings.Compare(a.name, b.name) })
	return changes, nil
}

func applyLinks(changes []linkChange, dryRun bool, log io.Writer) error {
	for _, change := range changes {
		shown := cmp.Or(change.version, "(none)")
		if err := say(log, "%s\t%s\n", change.name, shown); err != nil {
			return err
		}
		if dryRun {
			continue
		}
		var err error
		if change.target == "" {
			err = os.Remove(change.path)
		} else {
			err = replaceLink(change.target, change.path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// replaceLink points the symlink name at target atomically. It creates the
// new link under a temporary name in the same directory and renames it over
// name, and rename(2) replaces a link in one step, so anything resolving name
// sees the old target or the new one, never neither.
func replaceLink(target, name string) error {
	temporary := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+".new")
	// A leftover from an interrupted run. The directory passed
	// trust.CheckDirectory, so nobody else can have put it there.
	if err := os.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, name); err != nil {
		return errors.Join(err, os.Remove(temporary))
	}
	return nil
}

// copyProgram writes source to path under a temporary name and renames it
// into place, so path never holds a partial binary, even if the copy fails.
func copyProgram(source *os.File, path string) error {
	dir := filepath.Dir(path)
	// New directories are private, as in package link.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	_, err = io.Copy(temporary, source)
	// Only the owner may run it, like everything else homeskillet creates.
	err = errors.Join(err, temporary.Chmod(0o700), temporary.Sync(), temporary.Close())
	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(temporary.Name()))
	}
	return nil
}

// sameContents reports whether source holds the same bytes as the file at
// path.
func sameContents(source *os.File, path string) (bool, error) {
	a, err := digest(source)
	if err != nil {
		return false, err
	}
	other, err := os.Open(path) //nolint:gosec // G304: path is inside the checked installation
	if err != nil {
		return false, err
	}
	b, err := digest(other)
	err = errors.Join(err, other.Close())
	return err == nil && bytes.Equal(a, b), err
}

func digest(file *os.File) ([]byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

// resolve returns path with symlinks resolved in its longest existing prefix.
// The rest does not exist yet and is appended unchanged.
func resolve(path string) (string, error) {
	existing, err := existingAncestor(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	rest, err := filepath.Rel(existing, path)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, rest), nil
}

// existingAncestor returns path, or its nearest ancestor that exists.
func existingAncestor(path string) (string, error) {
	for {
		_, err := os.Lstat(path)
		if err == nil {
			return path, nil
		}
		parent := filepath.Dir(path)
		if !errors.Is(err, fs.ErrNotExist) || parent == path {
			return "", err
		}
		path = parent
	}
}

// say writes one line of output, so every write's error is checked the same
// way.
func say(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}
