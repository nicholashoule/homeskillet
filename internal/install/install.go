//go:build linux || darwin

// Package install keeps several versions of the homeskillet binary side by
// side and uses symlinks to choose which one runs, so an update never
// rewrites a binary in place and a bad update is undone by choosing another
// version.
//
// With the default data directory, ~/.local/share/homeskillet, the layout
// is:
//
//	~/.local/share/homeskillet/versions/v0.0.1/homeskillet
//	~/.local/share/homeskillet/versions/v0.0.2/homeskillet
//	~/.local/share/homeskillet/versions/v0.0.3/homeskillet
//	~/.local/share/homeskillet/latest   -> versions/v0.0.3
//	~/.local/share/homeskillet/previous -> versions/v0.0.2
//	~/.local/share/homeskillet/current  -> latest
//	~/.local/bin/homeskillet            -> ~/.local/share/homeskillet/current/homeskillet
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/safefile"
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
	// buildFile records, beside each binary, the version.Info of the build
	// that installed it, which is the build being installed: Install copies
	// the running program. Reading it costs nothing, where parsing build
	// details out of the binary would need debug/buildinfo and the ELF,
	// Mach-O, and PE readers it links in, about a sixth of the binary.
	buildFile = "build.json"
	// maxBuildFile bounds what Build reads; the file is a few hundred bytes.
	maxBuildFile = 64 << 10
)

// Layout locates an installation. Both paths are absolute, with symlinks
// resolved in the parts that exist; New returns such a layout. Every method
// refuses a Layout whose paths fail trust.CheckLocation, including the zero
// Layout, whose empty paths would otherwise mean the current directory.
type Layout struct {
	// Bin is the directory, normally on PATH, that holds the homeskillet
	// command.
	Bin string
	// Data holds the installed versions and the latest, previous, and
	// current links.
	Data string
}

// New returns the layout with the homeskillet command in bin, normally
// ~/.local/bin, and versions in data, homeskillet's data directory. Both must
// be absolute. Symlinks are resolved where the paths exist (see
// trust.Canonical), because the ownership checks and the bin link compare and
// store path strings.
func New(bin, data string) (Layout, error) {
	bin, err := trust.Canonical(bin)
	if err != nil {
		return Layout{}, err
	}
	data, err = trust.Canonical(data)
	if err != nil {
		return Layout{}, err
	}
	layout := Layout{Bin: bin, Data: data}
	return layout, layout.check()
}

// check refuses a layout outside the places homeskillet may act, such as
// --prefix /usr/local, whose bin and share belong to the system.
func (l Layout) check() error {
	for _, dir := range []string{l.Bin, l.Data} {
		if err := trust.CheckLocation(dir); err != nil {
			return err
		}
	}
	return nil
}

// Command returns the path of the homeskillet command in Bin.
func (l Layout) Command() string {
	return filepath.Join(l.Bin, Binary)
}

// Program returns the path of installed version v's binary.
func (l Layout) Program(v string) string {
	return filepath.Join(l.Data, versions, v, Binary)
}

func (l Layout) buildPath(v string) string {
	return filepath.Join(l.Data, versions, v, buildFile)
}

// Build returns the build details recorded when version v was installed. A
// version installed before homeskillet recorded them has none, which is
// reported as an error.
func (l Layout) Build(v string) (version.Info, error) {
	if err := l.check(); err != nil {
		return version.Info{}, err
	}
	if !version.Valid(v) {
		return version.Info{}, fmt.Errorf("%q is not a version such as v0.0.1", v)
	}
	data, err := safefile.Read(l.buildPath(v), maxBuildFile)
	if err != nil {
		return version.Info{}, err
	}
	var info version.Info
	if err := json.Unmarshal(data, &info); err != nil {
		return version.Info{}, fmt.Errorf("%s: %w", l.buildPath(v), err)
	}
	return info, nil
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
	// Broken is what current points at when that is not an installed
	// version, as after its directory was deleted by hand; Current is then
	// "". Use repairs it, so the command works again.
	Broken string
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
	if err := l.check(); err != nil {
		return State{}, err
	}
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
			state.Broken = target
			break
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

// Install copies the program at executable into the installation as the
// version build describes, records build beside it (see Build), and
// recomputes latest and previous. It creates current, following latest, and
// the homeskillet command in Bin if they do not exist yet. build must
// describe executable; the CLI passes the running program and
// version.Current.
//
// It writes one line per change to log, which may be nil. With dryRun it
// writes the same lines and changes nothing. Every check runs before the first change: the program
// must pass trust.CheckProgram, the directories it writes into must pass
// trust.CheckDirectory, and an existing file at the command's path that is
// not homeskillet's link is a conflict, never replaced.
func (l Layout) Install(executable string, build version.Info, dryRun bool, log io.Writer) error {
	v := build.Version
	if err := l.check(); err != nil {
		return err
	}
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
	err = l.install(source, build, dryRun, log)
	return errors.Join(err, source.Close())
}

func (l Layout) install(source *os.File, build version.Info, dryRun bool, log io.Writer) error {
	v := build.Version
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if err := trust.CheckProgram(source.Name(), info); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(l.Program(v)), l.Bin} {
		if err := trust.CheckCreatable(dir); err != nil {
			return err
		}
	}
	state, err := l.Inspect()
	if err != nil {
		return err
	}
	action, err := l.programAction(source, build)
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
	if !dryRun {
		if action != "unchanged" {
			if err := copyProgram(source, l.Program(v)); err != nil {
				return err
			}
		}
		// The record describes the installed bytes. An unchanged install
		// keeps the record it has, which a rebuild from the same source
		// would otherwise overwrite with its own build time; a missing
		// record is restored.
		if _, err := l.Build(v); action != "unchanged" || err != nil {
			if err := writeBuild(l.buildPath(v), build); err != nil {
				return err
			}
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
	if err := reportRunning(log, state, after, v); err != nil {
		return err
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
			if err := os.Symlink(l.commandTarget(), l.Command()); err != nil {
				return err
			}
			return safefile.SyncDir(l.Bin)
		}
	}
	return nil
}

// reportRunning says when the version just installed is not the one that
// will run, so an install never looks like an update that did not happen.
// state is the installation before the install and after its versions
// afterward.
func reportRunning(log io.Writer, state State, after []string, v string) error {
	switch latest := (State{Versions: after}).Latest(); {
	case state.Current != "" && !state.Following:
		return say(log, "pinned\t%s\trun homeskillet self use latest to follow the newest version\n", state.Current)
	case latest != v:
		return say(log, "note\t%s still runs because it is latest; homeskillet self use %s runs %s\n", latest, v, v)
	}
	return nil
}

// programAction decides what Install does with build's binary: "installed"
// when its version is new, "unchanged" when the same bytes, or a build of the
// same source (see version.Info.SameSource), are already installed, or
// "replaced" for a +dirty build with new contents. Rewriting any other
// installed version is refused, because a version that silently changed
// would no longer identify what is running.
//
// A build of the same source counts as unchanged because builds are stamped
// with their build time, so rebuilding a release never reproduces its bytes;
// the installed binary is kept, so the version still names what it did.
func (l Layout) programAction(source *os.File, build version.Info) (string, error) {
	v := build.Version
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
	}
	if installed, err := l.Build(v); err == nil && installed.SameSource(build) {
		return "unchanged", nil
	}
	return "", fmt.Errorf("%s is already installed with different contents; installed versions are never rewritten, so remove it first with homeskillet self remove %s", v, v)
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
// It writes the choice to log, which may be nil. With dryRun it writes the
// same line and changes nothing.
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
// It writes one line per change to log, which may be nil. With dryRun it
// writes the same lines and changes nothing.
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
		// Leftovers from an interrupted write are Install's too. Install
		// creates no directories here, and removing one would fail only
		// after latest and previous had moved, so any directory stops
		// Remove before it changes anything.
		if name := entry.Name(); entry.IsDir() || name != Binary && name != buildFile && !strings.HasPrefix(name, safefile.TempPrefix) {
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
	if err := l.check(); err != nil {
		return err
	}
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
	dir, base := filepath.Dir(name), filepath.Base(name)
	// A temporary name of this process's own, so concurrent self commands
	// never remove or rename each other's half-made link. symlink fails on
	// an existing name, such as a leftover from an interrupted run, so the
	// next number is tried. The directory passed trust.CheckDirectory, so
	// nobody else can create names in it.
	var temporary string
	for n := 0; ; n++ {
		temporary = filepath.Join(dir, fmt.Sprintf("%s%s.%d.%d", safefile.TempPrefix, base, os.Getpid(), n))
		err := os.Symlink(target, temporary)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	if err := os.Rename(temporary, name); err != nil {
		return errors.Join(err, os.Remove(temporary))
	}
	return safefile.SyncDir(dir)
}

// copyProgram writes source to path atomically (see safefile.Write) as an
// executable only its owner may run, like everything else homeskillet
// creates.
func copyProgram(source *os.File, path string) error {
	return safefile.Write(path, 0o700, func(w io.Writer) error {
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return err
		}
		_, err := io.Copy(w, source)
		return err
	})
}

// writeBuild records build at path.
func writeBuild(path string, build version.Info) error {
	return safefile.Write(path, 0o600, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(build)
	})
}

// sameContents reports whether source holds the same bytes as the file at
// path. It compares the bytes themselves, which is exact and needs no hash.
func sameContents(source *os.File, path string) (bool, error) {
	other, err := os.Open(path) //nolint:gosec // G304: path is inside the checked installation
	if err != nil {
		return false, err
	}
	same, err := equalFiles(source, other)
	if err := errors.Join(err, other.Close()); err != nil {
		return false, err
	}
	return same, nil
}

// equalFiles compares a and b from their beginnings, first by size, then
// block by block.
func equalFiles(a, b *os.File) (bool, error) {
	infoA, err := a.Stat()
	if err != nil {
		return false, err
	}
	infoB, err := b.Stat()
	if err != nil {
		return false, err
	}
	if infoA.Size() != infoB.Size() {
		return false, nil
	}
	for _, file := range []*os.File{a, b} {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
	}
	blockA, blockB := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		nA, errA := io.ReadFull(a, blockA)
		nB, errB := io.ReadFull(b, blockB)
		if !bytes.Equal(blockA[:nA], blockB[:nB]) {
			return false, nil
		}
		endA, endB := ended(errA), ended(errB)
		if errA != nil && !endA {
			return false, errA
		}
		if errB != nil && !endB {
			return false, errB
		}
		if endA || endB {
			return endA && endB, nil
		}
	}
}

// ended reports whether err from io.ReadFull means the file ran out, rather
// than that reading failed.
func ended(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// say writes one line of output, so every write's error is checked the same
// way. A nil writer discards it.
func say(w io.Writer, format string, args ...any) error {
	if w == nil {
		return nil
	}
	_, err := fmt.Fprintf(w, format, args...)
	return err
}
