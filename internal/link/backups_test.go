//go:build linux || darwin || windows

// Tests of listing and purging backups: only what link --force names as one
// counts, a purge deletes only what it was given, never follows a symlink, and
// removes only the directories it emptied.

package link

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBackupName(t *testing.T) {
	for name, want := range map[string]string{
		".bashrc.1761393600":                                 ".bashrc",
		".bashrc.1761393600-2":                               ".bashrc",
		filepath.Join(".config", "git", "config.1761393600"): filepath.Join(".config", "git", "config"),
		"notes.txt.1761393600":                               "notes.txt",
	} {
		original, moved, ok := backupName(name)
		if !ok || original != want || !moved.Equal(time.Unix(1761393600, 0)) {
			t.Errorf("backupName(%q) = %q, %v, %v; want %q", name, original, moved, ok, want)
		}
	}
	for _, name := range []string{".bashrc", "notes.txt", ".1761393600", "x.0", "x.-5", "x.017", "x.1761393600-0", "x.1761393600-a", "x.1761393600-01", filepath.Join("dir", ".1761393600")} {
		if _, _, ok := backupName(name); ok {
			t.Errorf("backupName(%q) accepted", name)
		}
	}
}

// backupsFixture makes a data directory whose backups hold two backups of one
// file, one of another, a file that is not named as a backup, and, where the
// system allows, a backup that is a symlink leading out of the data
// directory.
func backupsFixture(t *testing.T) (data, outside string) {
	t.Helper()
	data, outside = t.TempDir(), t.TempDir()
	root := filepath.Join(data, BackupDir)
	for name, contents := range map[string]string{
		".bashrc.1700000000":                                 "old",
		".bashrc.1700000000-1":                               "same second",
		filepath.Join(".config", "git", "config.1800000000"): "newer",
		"README": "not a backup",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "target"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Join(outside, "target"), filepath.Join(root, ".profile.1750000000")) // skipped where symlinks are unavailable
	return data, outside
}

func TestBackupsAndPurge(t *testing.T) {
	data, outside := backupsFixture(t)
	kept, err := Backups(data)
	if err != nil {
		t.Fatal(err)
	}
	var originals []string
	for _, backup := range kept {
		originals = append(originals, filepath.ToSlash(backup.Original))
	}
	want := []string{".bashrc", ".bashrc", ".config/git/config"}
	if _, err := os.Lstat(filepath.Join(data, BackupDir, ".profile.1750000000")); err == nil {
		want = []string{".bashrc", ".bashrc", ".profile", ".config/git/config"}
	}
	if !slices.Equal(originals, want) {
		t.Fatalf("listed %v, want %v, oldest first", originals, want)
	}

	var log bytes.Buffer
	if err := Purge(kept, data, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Count(log.String(), "deleted\t") != len(want) {
		t.Errorf("log: %q", log.String())
	}
	if left, err := Backups(data); err != nil || len(left) != 0 {
		t.Fatalf("left after the purge: %v, %v", left, err)
	}
	// What is not a backup stays, as does whatever a symlink led to, and the
	// directories the purge emptied go.
	if data, err := os.ReadFile(filepath.Join(data, BackupDir, "README")); err != nil || string(data) != "not a backup" {
		t.Errorf("a file not named as a backup: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "target")); err != nil || string(data) != "keep" {
		t.Errorf("a backup symlink's target: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(data, BackupDir, ".config")); err == nil {
		t.Error("an emptied directory was left")
	}
}

func TestPurgeRefusesWhatIsNoBackup(t *testing.T) {
	data, _ := backupsFixture(t)
	root := filepath.Join(data, BackupDir)
	for name, backup := range map[string]Kept{
		"outside the backups": {Path: filepath.Join(data, "links.json.1700000000")},
		"not named as one":    {Path: filepath.Join(root, "README")},
		"escaping with ..":    {Path: filepath.Join(root, "..", "x.1700000000")},
	} {
		if err := Purge([]Kept{backup}, data, nil); err == nil {
			t.Errorf("%s: purged", name)
		}
	}
	// A directory that took a backup's place since it was listed stays.
	kept, err := Backups(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(kept[0].Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(kept[0].Path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Purge(kept, data, nil); err == nil || !strings.Contains(err.Error(), "no longer a file") {
		t.Fatalf("got %v", err)
	}
	if info, err := os.Lstat(kept[0].Path); err != nil || !info.IsDir() {
		t.Fatalf("the directory went: %v", err)
	}
	// A data directory without backups lists none.
	if kept, err := Backups(t.TempDir()); err != nil || len(kept) != 0 {
		t.Fatalf("no backups: %v, %v", kept, err)
	}
}
