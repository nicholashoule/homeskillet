//go:build linux || darwin

// link --force: moving conflicting files and symlinks, never directories, into
// the data directory's backups/ before linking, never overwriting a backup,
// and moving them back when linking fails.

package link

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// BackupDir is the directory in homeskillet's data directory that holds the
// files link --force moved aside.
const BackupDir = "backups"

// Backup moves whatever occupies each Conflict target into data/BackupDir, at
// its path relative to home with the current Unix time appended, such as
// backups/.bashrc.1761393600. It returns where each target went, keyed by
// target, and the plans with those targets now Missing, ready for Create. It
// writes one line per move to log, which may be nil. With no conflicts it
// changes nothing.
//
// This is how link --force replaces files: it moves them, never deletes them,
// so the user can restore anything it replaced, and it moves no directories
// (see CheckForce). Backups stay out of home, so a program that reads every
// file in a directory, as an Include of config.d/* does, never loads one. A target absent from the repository still blocks,
// because linking would fail anyway and nothing should move for nothing. If a
// move fails, the moves already made are undone, so home is never left
// without a file that neither a backup nor a link replaced. A move is a
// rename, so the data directory must be on the same file system as home.
func Backup(plans []Plan, home, data string, log io.Writer) (map[string]string, []Plan, error) {
	log = orDiscard(log)
	if slices.ContainsFunc(plans, func(plan Plan) bool { return plan.State == NoSource }) {
		return nil, nil, ErrBlocked
	}
	if err := CheckForce(plans); err != nil {
		return nil, nil, err
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	root := filepath.Join(data, BackupDir)
	updated := slices.Clone(plans)
	moved := map[string]string{}
	var order []string
	undo := func(cause error) error {
		errs := []error{cause}
		for _, target := range slices.Backward(order) {
			errs = append(errs, os.Rename(moved[target], target))
		}
		return errors.Join(errs...)
	}
	for i, plan := range updated {
		if plan.State != Conflict {
			continue
		}
		relative, err := filepath.Rel(home, plan.Target)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, nil, undo(fmt.Errorf("target %s is not inside home %s", plan.Target, home))
		}
		destination, err := freeName(filepath.Join(root, relative) + "." + stamp)
		if err != nil {
			return nil, nil, undo(err)
		}
		if err := os.Rename(plan.Target, destination); err != nil {
			return nil, nil, undo(fmt.Errorf("moving %s into the backup (the data directory must be on the same file system): %w", plan.Target, err))
		}
		moved[plan.Target] = destination
		order = append(order, plan.Target)
		updated[i].State = Missing
		if _, err := fmt.Fprintf(log, "moved\t%s -> %s\n", plan.Target, destination); err != nil {
			return nil, nil, undo(err)
		}
	}
	return moved, updated, nil
}

// Restore moves back each target in moved, a result of Backup, that is still
// absent, as when linking failed after Backup moved files aside. Targets that
// are now links, or that hold anything else, are left alone. So a forced link
// that stops partway never leaves a target with neither its original file nor
// its new link.
func Restore(moved map[string]string) error {
	var errs []error
	for target, backup := range moved {
		if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		errs = append(errs, os.Rename(backup, target))
	}
	return errors.Join(errs...)
}

// CheckForce returns an error naming the first conflicting target that
// Backup would not move: a directory. Only files and symlinks are moved. A
// directory where the repository has a file can hold anything, even the
// repository, the data directory, or other programs' files, so moving it is
// left to the user. Callers check before a dry run reports what --force would
// do, and Backup checks again before moving anything.
func CheckForce(plans []Plan) error {
	for _, plan := range plans {
		if plan.State != Conflict {
			continue
		}
		info, err := os.Lstat(plan.Target)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("--force moves only files and symlinks, and %s is a directory; move it aside yourself", plan.Target)
		}
	}
	return nil
}

// freeName creates the parent directories of name, private, and returns name,
// or name with -1, -2, and so on appended when a backup of the same file from
// the same second already exists. rename replaces an existing file silently,
// so without this a second backup would destroy the first. The backup
// directory is private, so nobody else can create a name between this check
// and the rename.
func freeName(name string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return "", err
	}
	candidate := name
	for n := 1; ; n++ {
		_, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = name + "-" + strconv.Itoa(n)
	}
}
