//go:build linux || darwin || windows

// The backups link --force made: listing them, and purging them, the one way
// homeskillet deletes a file it did not create, and only when asked.

package link

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kept is one backup: a file or symlink link --force moved into the data
// directory's BackupDir.
type Kept struct {
	// Path is where the backup is.
	Path string
	// Original is where it was, relative to the home it came from.
	Original string
	// Moved is when link --force moved it, from its name.
	Moved time.Time
	// Size is its size in bytes; a symlink's is that of the link itself.
	Size int64
}

// Backups lists the backups in data's BackupDir, oldest first. Only a file or
// symlink named as Backup names one, its path in home, a dot, the Unix time,
// and perhaps -1, -2, and so on, counts; anything else there is left out, so
// it is never purged. Symlinks are listed, never followed. data must be
// absolute, canonical, and checked by trust.CheckLocation and the ownership
// rules (see trust.CheckCreatable), as for the record of links.
func Backups(data string) ([]Kept, error) {
	root := filepath.Join(data, BackupDir)
	var kept []Kept
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		original, moved, ok := backupName(relative)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		kept = append(kept, Kept{Path: path, Original: original, Moved: moved, Size: info.Size()})
		return nil
	})
	slices.SortStableFunc(kept, func(a, b Kept) int {
		if c := a.Moved.Compare(b.Moved); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return kept, err
}

// backupName splits a backup's path relative to BackupDir into the original
// path and the time Backup moved it, as Backup names it: the original, a dot,
// the Unix time, and perhaps a dash and a number.
func backupName(relative string) (original string, moved time.Time, ok bool) {
	dot := strings.LastIndexByte(relative, '.')
	if dot <= 0 || os.IsPathSeparator(relative[dot-1]) {
		return "", time.Time{}, false
	}
	stamp, extra, hasExtra := strings.Cut(relative[dot+1:], "-")
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || seconds <= 0 || stamp != strconv.FormatInt(seconds, 10) {
		return "", time.Time{}, false
	}
	if n, err := strconv.Atoi(extra); hasExtra && (err != nil || n <= 0 || extra != strconv.Itoa(n)) {
		return "", time.Time{}, false
	}
	return relative[:dot], time.Unix(seconds, 0), true
}

// Purge deletes each backup in kept, a result of Backups, and then each
// directory in data's BackupDir that it left empty, writing a line per backup
// to log, which may be nil. Each is looked at again just before it goes, and
// only a file or symlink still named as a backup, inside BackupDir, is
// deleted; a symlink is deleted itself, never what it leads to. This is the
// one way homeskillet deletes files it did not create, so callers show every
// backup and ask first.
func Purge(kept []Kept, data string, log io.Writer) error {
	log = orDiscard(log)
	root := filepath.Join(data, BackupDir)
	emptied := map[string]bool{}
	for _, backup := range kept {
		relative, err := filepath.Rel(root, backup.Path)
		if err != nil || !filepath.IsLocal(relative) {
			return fmt.Errorf("%s is not in %s", backup.Path, root)
		}
		if _, _, ok := backupName(relative); !ok {
			return fmt.Errorf("%s is not named as a backup", backup.Path)
		}
		info, err := os.Lstat(backup.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("%s is no longer a file or symlink; nothing more was deleted", backup.Path)
		}
		if err := os.Remove(backup.Path); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(log, "deleted\t%s\n", backup.Path); err != nil {
			return err
		}
		for dir := filepath.Dir(backup.Path); inside(root, dir); dir = filepath.Dir(dir) {
			emptied[dir] = true
		}
	}
	// A directory sorts before those inside it, so in reverse each is tried
	// only after them. One that still holds anything stays.
	dirs := slices.Sorted(maps.Keys(emptied))
	slices.Reverse(dirs)
	for _, dir := range dirs {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
	}
	return nil
}

// inside reports whether dir is strictly inside root, lexically.
func inside(root, dir string) bool {
	relative, err := filepath.Rel(root, dir)
	return err == nil && relative != "." && filepath.IsLocal(relative)
}
