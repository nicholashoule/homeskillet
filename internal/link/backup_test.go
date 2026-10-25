//go:build linux || darwin

package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestBackupUndoesMovesOnFailure(t *testing.T) {
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "b", Target: "sub/b"}}
	repo, home := fixture(t, entries...)
	data := dataDir(home)
	for _, target := range []string{"a", "sub/b"} {
		path := filepath.Join(home, target)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(target), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := Prepare(entries, repo, home, data, false)
	if err != nil {
		t.Fatal(err)
	}
	// A file where the backup needs the directory sub makes the second move
	// fail after the first succeeded.
	if err := os.MkdirAll(filepath.Join(data, BackupDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, BackupDir, "sub"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Backup(plans, home, data, nil); err == nil {
		t.Fatal("Backup succeeded despite a blocked move")
	}
	for _, target := range []string{"a", "sub/b"} {
		if data, err := os.ReadFile(filepath.Join(home, target)); err != nil || string(data) != target {
			t.Fatalf("%s not restored: %q, %v", target, data, err)
		}
	}
}

func TestBackupRefusesAbsentSources(t *testing.T) {
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "gone", Target: "b"}}
	repo, home := fixture(t, entries...)
	if err := os.Remove(filepath.Join(repo, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := Prepare(entries, repo, home, dataDir(home), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Backup(plans, home, dataDir(home), nil); err == nil {
		t.Fatal("Backup moved files although linking would fail")
	}
	if info, err := os.Lstat(filepath.Join(home, "a")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("conflict moved: %v", err)
	}
}
