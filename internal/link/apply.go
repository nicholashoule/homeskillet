//go:build linux || darwin

// Carrying out plans that Prepare has checked: making the missing symlinks for
// link, and removing for unlink only the symlinks that point where homeskillet
// pointed them.

package link

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Create removes every Orphaned link and links every Missing target to its
// source, writing one line per change to log, which may be nil to discard the
// lines. It refuses to start if any plan is blocked, so a caller cannot apply
// part of a set of entries that failed its checks. Callers then record the
// result with Save.
//
// A failed write to log stops Create with that error. The link it describes
// already exists, and rerunning finishes the rest, but a run that cannot report
// what it changed should not claim success.
func Create(plans []Plan, log io.Writer) error {
	log = orDiscard(log)
	if Blocked(plans) {
		return ErrBlocked
	}
	if err := removeOrphans(plans, log); err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.State != Missing {
			continue
		}
		// New parent directories are private. GnuPG warns unless ~/.gnupg is
		// 0700, ~/.ssh is expected to be, and no dotfile directory needs to be
		// listable by other users. Existing directories are left as they are.
		if err := os.MkdirAll(filepath.Dir(plan.Target), 0o700); err != nil {
			return err
		}
		// os.Symlink never replaces an existing file, so a target created after
		// Prepare ran makes this fail rather than be overwritten. The link
		// stores the absolute source path, so it stays valid wherever the
		// homeskillet binary is moved or rebuilt.
		if err := os.Symlink(plan.Source, plan.Target); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(log, "linked\t%s -> %s\n", plan.Target, plan.Source); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes every Linked and Orphaned target and reports each Conflict as
// skipped, writing one line per target to log, which may be nil. Conflicts do
// not block it: it only deletes symlinks that point at their own source, so
// whatever else occupies a target is left alone while the other entries are
// still removed. A failed write to log stops it, as in Create. Callers then
// record the result with Save.
func Remove(plans []Plan, log io.Writer) error {
	log = orDiscard(log)
	for _, plan := range plans {
		var line string
		switch plan.State {
		case Linked, Orphaned:
			if err := unlink(plan.Target); err != nil {
				return err
			}
			line = "unlinked\t" + plan.Target
		case Conflict:
			line = "skipped\t" + plan.Target
		default:
			continue
		}
		if _, err := fmt.Fprintln(log, line); err != nil {
			return err
		}
	}
	return nil
}

// removeOrphans deletes every Orphaned link. Prepare found each to be a
// symlink naming its recorded source, and unlink deletes the link itself,
// never what it points to.
func removeOrphans(plans []Plan, log io.Writer) error {
	for _, plan := range plans {
		if plan.State != Orphaned {
			continue
		}
		if err := unlink(plan.Target); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(log, "unlinked\t%s\n", plan.Target); err != nil {
			return err
		}
	}
	return nil
}

// unlink removes the symlink at path. Unlike os.Remove, it never removes a
// directory, so a directory that replaced the link after Prepare looked is
// left alone, with an error, instead of deleted.
func unlink(path string) error {
	if err := syscall.Unlink(path); err != nil {
		return &os.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}

// orDiscard returns w, or io.Discard when w is nil, so a caller that wants no
// output cannot make a write panic.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
