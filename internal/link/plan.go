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
)

// Plan is the prepared action for one entry.
type Plan struct {
	Source string // absolute path inside the repository
	Target string // absolute path inside the home directory
	State  State
}

// ErrBlocked is returned by Create, and by callers that stop for the same
// reason, when any plan is a Conflict or NoSource.
var ErrBlocked = errors.New("conflicts or missing sources found; no files changed")

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
// under the repository's home directory; both get the same checks. repo and
// home must be absolute with symlinks already resolved, because containment
// and link ownership are decided by comparing path strings.
//
// When unlinking is false, Prepare requires the repository to be private (see
// trust.CheckRepository) and reports absent sources as NoSource. When unlinking is
// true it skips the privacy checks and ignores absent sources. Removing links
// that point into the repository cannot run anyone's code, and unlinking is how
// the user recovers from a deleted source or a repository whose permissions
// went wrong, so it must not be blocked by either.
func Prepare(entries []config.Entry, repo, home string, unlinking bool) ([]Plan, error) {
	if !unlinking {
		if err := trust.CheckRepository(repo); err != nil {
			return nil, err
		}
	}
	var plans []Plan
	for _, entry := range entries {
		source := filepath.Join(repo, entry.Source)
		target := filepath.Join(home, entry.Target)
		// When home is the repository or contains it, a target could replace
		// repository files, including other entries' sources.
		if trust.Within(repo, target) {
			return nil, fmt.Errorf("target %s would modify the repository", target)
		}
		// Overlapping targets such as "a" and "a/b" would make the second link
		// be created through the first.
		for _, previous := range plans {
			if trust.Within(previous.Target, target) || trust.Within(target, previous.Target) {
				return nil, fmt.Errorf("overlapping targets: %s and %s", previous.Target, target)
			}
		}
		missing, err := checkSource(repo, source, unlinking)
		if err != nil {
			return nil, err
		}
		if err := checkParents(home, target); err != nil {
			return nil, err
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
	return plans, nil
}

// checkSource verifies that source is a regular file that resolves inside repo
// and, unless unlinking, that nobody else can modify it. It reports whether the
// source is absent instead of failing, so callers can report that per entry.
func checkSource(repo, source string, unlinking bool) (missing bool, err error) {
	resolved, err := filepath.EvalSymlinks(source)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("source %s: %w", source, err)
	}
	// A source that is a symlink leaving the repository, for example to a
	// file in /tmp that another user controls, would make the link's content
	// depend on a file that the repository checks never examine.
	if !trust.Within(repo, resolved) {
		return false, fmt.Errorf("source %s resolves outside the repository", source)
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
	if !unlinking {
		// Check the path as written and as resolved. The written path covers
		// directories that hold symlinks (whoever can write to them can swap
		// the symlink), and the resolved path covers the real file.
		if err := trust.CheckPath(repo, source); err != nil {
			return false, err
		}
		if resolved != source {
			if err := trust.CheckPath(repo, resolved); err != nil {
				return false, err
			}
		}
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
