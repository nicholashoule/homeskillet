//go:build linux || darwin

// Package link decides what homeskillet would change in a home directory and
// then makes those changes.
//
// Work happens in two phases. Prepare inspects every entry and fails the whole
// set if any entry is unsafe, without modifying anything. Create and Remove
// then act on the plans it returns. Putting every check ahead of every change
// means one bad entry never leaves a half-installed home directory.
//
// The phases are not a transaction. If the filesystem changes between them, or
// an I/O error stops Create or Remove midway, the result can be partial;
// rerunning the command after fixing the cause completes it.
//
// Every file in this package carries the linux || darwin build constraint, as
// do packages trust, setup, and cli. The ownership checks in package trust
// read Unix file metadata, and constraining whole packages means other systems
// skip them in `go build ./...` and `go test ./...` instead of failing to
// compile half a package.
package link

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// State describes a target as Prepare found it.
type State string

const (
	// Missing means nothing exists at the target, so Create will link it.
	Missing State = "missing"
	// Linked means the target is a symlink to its source, so Remove will
	// delete it.
	Linked State = "linked"
	// Conflict means something else exists at the target. It is never
	// replaced, even when its contents match the source, because it may be the
	// only copy of the user's data.
	Conflict State = "conflict"
	// NoSource means the source is absent from the repository, so there is
	// nothing to link to. Prepare reports it only when preparing to link.
	NoSource State = "no-source"
	// Orphaned means the target is a link homeskillet made from this
	// repository, as its record shows, for an entry the repository no longer
	// declares, such as a file since deleted from home/. Create and Remove
	// both delete it, so the home directory again matches the repository.
	Orphaned State = "orphaned"
)

// Plan is the prepared action for one entry.
type Plan struct {
	Source string // absolute path inside the repository
	Target string // absolute path inside the home directory
	State  State
}

// ErrBlocked is returned by Create, and by callers that stop for the same
// reason, when any plan is a Conflict or NoSource. "Missing" names a target
// not linked yet, which blocks nothing, so the message avoids the word.
var ErrBlocked = errors.New("blocked by conflicts or sources absent from the repository")

// Blocked reports whether any plan prevents linking.
func Blocked(plans []Plan) bool {
	for _, plan := range plans {
		if plan.State == Conflict || plan.State == NoSource {
			return true
		}
	}
	return false
}

// Prepare resolves entries against repo and home and reports each target's
// state. Entries come from config.Load, listed in the manifest or discovered
// under the repository's home directory; both get the same checks. data is
// homeskillet's data directory, which holds the record of links. repo, home,
// and data must be absolute with symlinks already resolved (see
// trust.Canonical), because containment and link ownership are decided by
// comparing path strings.
//
// After the plans for entries come Orphaned plans for links the repository
// made earlier but no longer declares (see Save and record.go).
//
// All three directories must pass trust.CheckLocation. When unlinking is false,
// Prepare requires the repository, home and every existing directory a link
// goes into, and the data directory to be private (see trust.CheckRepository,
// trust.CheckDirectory, trust.CheckExisting, and trust.CheckCreatable) and
// reports absent sources as NoSource. When unlinking is true it skips the
// privacy checks and ignores absent sources. Removing links that point into the repository cannot
// run anyone's code, and unlinking is how the user recovers from a deleted
// source or a repository whose permissions went wrong, so it must not be
// blocked by either.
func Prepare(entries []config.Entry, repo, home, data string, unlinking bool) ([]Plan, error) {
	return PrepareSelected(entries, repo, home, data, unlinking, nil)
}

// PrepareSelected is Prepare for only the entries and recorded links that
// selected accepts, given their absolute target and source; nil selects
// everything. The checks of the repository as a whole, and of how entries
// relate to each other, still cover every entry, but the checks of each
// source, target, and target directory cover only the selected ones, so a
// problem with one file never blocks acting on another. Every entry still
// counts as declared, so an unselected one is never reported as orphaned.
func PrepareSelected(entries []config.Entry, repo, home, data string, unlinking bool, selected func(target, source string) bool) ([]Plan, error) {
	// Even unlinking stays out of system directories: removing links there
	// is no more homeskillet's business than creating them.
	for _, dir := range []string{repo, home, data} {
		if err := trust.CheckLocation(dir); err != nil {
			return nil, err
		}
	}
	if !unlinking {
		if err := trust.CheckRepository(repo); err != nil {
			return nil, err
		}
		// Links live in home, so anyone who could write to it could
		// replace them. The data directory's check covers home only when
		// the data directory is inside it.
		if err := trust.CheckDirectory(home); err != nil {
			return nil, err
		}
	}
	// A data directory in the repository would put the record in version
	// control, where every machine's links would overwrite each other's.
	if trust.Within(repo, data) {
		return nil, fmt.Errorf("data directory %s is inside the repository", data)
	}
	if !unlinking {
		if err := trust.CheckCreatable(data); err != nil {
			return nil, err
		}
	}
	var plans []Plan
	declared := make([]string, 0, len(entries))
	for _, entry := range entries {
		source := filepath.Join(repo, entry.Source)
		target := filepath.Join(home, entry.Target)
		// homeskillet's own data, including the record and backups, must
		// never be replaced by a link, nor sit inside a target that link
		// --force could move aside.
		if trust.Within(data, target) || trust.Within(target, data) {
			return nil, fmt.Errorf("target %s overlaps homeskillet's data directory %s", target, data)
		}
		// When home is the repository or contains it, a target could replace
		// repository files, including other entries' sources; a target that
		// contains the repository, such as .config for a clone in
		// ~/.config/dotfiles, could take the whole repository with it.
		if trust.Within(repo, target) || trust.Within(target, repo) {
			return nil, fmt.Errorf("target %s overlaps the repository %s", target, repo)
		}
		// Overlapping targets such as "a" and "a/b" would make the second link
		// be created through the first.
		for _, previous := range declared {
			if trust.Within(previous, target) || trust.Within(target, previous) {
				return nil, fmt.Errorf("overlapping targets: %s and %s", previous, target)
			}
		}
		declared = append(declared, target)
		if selected != nil && !selected(target, source) {
			continue
		}
		missing, err := checkSource(repo, source, unlinking)
		if err != nil {
			return nil, err
		}
		if err := checkParents(home, target); err != nil {
			return nil, err
		}
		if !unlinking {
			if err := trust.CheckExisting(home, filepath.Dir(target)); err != nil {
				return nil, err
			}
		}
		state, err := targetState(source, target)
		if err != nil {
			return nil, err
		}
		if missing && !unlinking {
			state = NoSource
		}
		plans = append(plans, Plan{Source: source, Target: target, State: state})
	}
	found, err := orphans(repo, home, data, declared)
	if err != nil {
		return nil, err
	}
	for _, orphan := range found {
		if selected == nil || selected(orphan.Target, orphan.Source) {
			plans = append(plans, orphan)
		}
	}
	return plans, nil
}

// checkSource verifies that source is a regular file inside repo and, unless
// unlinking, that every directory and symlink on the way to it is private and
// inside repo (see trust.Resolve). It reports whether the source is absent
// instead of failing, so callers can report that per entry.
func checkSource(repo, source string, unlinking bool) (missing bool, err error) {
	var resolved string
	if unlinking {
		// Unlinking only needs to know the link is homeskillet's, not that
		// the repository is still safe (see Prepare).
		resolved, err = filepath.EvalSymlinks(source)
		if err == nil && !trust.Within(repo, resolved) {
			return false, fmt.Errorf("source %s resolves outside the repository", source)
		}
	} else {
		// A source that leads outside the repository at any hop, for
		// example through a symlink in /tmp that another user controls,
		// would make the link's content depend on files the repository
		// checks never examine.
		resolved, err = trust.Resolve(repo, source)
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("source %s: %w", source, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return false, err
	}
	// Only regular files are linked. A linked directory would let files appear
	// under home that the repository never declared and these checks never
	// examined.
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("source %s is not a regular file", source)
	}
	return false, nil
}

// checkParents rejects a target whose existing parent directories include a
// symlink or a non-directory. A symlinked parent such as ~/.config pointing at
// /tmp/x would place the link outside home, which the lexical containment
// checks cannot detect. Parents that do not exist yet are fine; Create makes
// them.
func checkParents(home, target string) error {
	relative, err := filepath.Rel(home, filepath.Dir(target))
	if err != nil {
		return err
	}
	parent := home
	for component := range strings.SplitSeq(relative, string(filepath.Separator)) {
		parent = filepath.Join(parent, component)
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("target parent %s is a symlink or not a directory", parent)
		}
	}
	return nil
}

// targetState classifies whatever currently exists at target.
func targetState(source, target string) (State, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return Missing, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return Conflict, nil
	}
	destination, err := os.Readlink(target)
	if err != nil {
		return "", err
	}
	// A link is ours if it names the source path. Comparing the name rather
	// than the resolved file means a dangling link to a deleted source is
	// still recognized and can be removed. Relative links, such as those GNU
	// Stow creates, are accepted too.
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(filepath.Dir(target), destination)
	}
	if filepath.Clean(destination) == source {
		return Linked, nil
	}
	return Conflict, nil
}
