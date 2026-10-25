//go:build linux || darwin

package link

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Create links every Missing target to its source, writing one line per link to
// log. It refuses to start if any plan is blocked, so a caller cannot apply
// part of a set of entries that failed its checks.
//
// A failed write to log stops Create with that error. The link it describes
// already exists, and rerunning finishes the rest, but a run that cannot report
// what it changed should not claim success.
func Create(plans []Plan, log io.Writer) error {
	if Blocked(plans) {
		return ErrBlocked
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

// Remove deletes every Linked target and reports each Conflict as skipped,
// writing one line per target to log. Conflicts do not block it: it only
// deletes symlinks that point at their own source, so whatever else occupies a
// target is left alone while the other entries are still removed. A failed
// write to log stops it, as in Create.
func Remove(plans []Plan, log io.Writer) error {
	for _, plan := range plans {
		var line string
		switch plan.State {
		case Linked:
			if err := os.Remove(plan.Target); err != nil {
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
