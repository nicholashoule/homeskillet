//go:build linux || darwin || windows

// Tests of the record of links: orphans, keeping repositories and homes apart,
// and refusing a record that is invalid or that someone else could have
// planted.

package link

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// apply links or unlinks entries and records the result, as the CLI does.
func apply(t *testing.T, entries []config.Entry, repo, home string, unlinking bool) []Plan {
	t.Helper()
	plans, err := Prepare(entries, repo, home, dataDir(home), unlinking)
	if err != nil {
		t.Fatal(err)
	}
	if unlinking {
		err = Remove(plans, nil)
	} else {
		err = Create(plans, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	save(t, repo, home, dataDir(home), plans)
	return plans
}

// save records plans after they were applied, as the CLI does.
func save(t *testing.T, repo, home, data string, plans []Plan) {
	t.Helper()
	refreshed, err := Refresh(plans)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(repo, home, data, refreshed, false); err != nil {
		t.Fatal(err)
	}
}

// dataDir returns the default data directory in home, as the CLI uses it.
func dataDir(home string) string {
	return filepath.Join(home, ".local", "share", "homeskillet")
}

// recordedTargets returns each repository's recorded targets in home.
func recordedTargets(t *testing.T, home string) map[string][]string {
	t.Helper()
	rec, err := loadRecord(dataDir(home))
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string][]string{}
	for repo, links := range rec.Homes[home] {
		for _, link := range links {
			targets[repo] = append(targets[repo], link.Target)
		}
	}
	return targets
}

func TestOrphans(t *testing.T) {
	requireSymlinks(t)
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "b", Target: ".config/b"}}
	repo, home := fixture(t, entries...)
	apply(t, entries, repo, home, false)
	if got := recordedTargets(t, home)[repo]; strings.Join(got, ",") != "a,"+filepath.Join(".config", "b") {
		t.Fatalf("recorded %v", got)
	}

	// The repository stops declaring b, as when its file is deleted.
	if err := os.Remove(filepath.Join(repo, "b")); err != nil {
		t.Fatal(err)
	}
	remaining := entries[:1]
	plans, err := Prepare(remaining, repo, home, dataDir(home), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := states(plans); len(got) != 2 || got[0] != Linked || got[1] != Orphaned {
		t.Fatalf("states %v", got)
	}
	if Blocked(plans) {
		t.Fatal("an orphan blocked linking")
	}
	apply(t, remaining, repo, home, false)
	if _, err := os.Lstat(filepath.Join(home, ".config", "b")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("orphan remains: %v", err)
	}
	if got := recordedTargets(t, home)[repo]; strings.Join(got, ",") != "a" {
		t.Fatalf("recorded %v after removing the orphan", got)
	}

	// Unlinking removes the rest, and the record goes with the last links.
	apply(t, remaining, repo, home, true)
	if _, err := os.Lstat(RecordPath(dataDir(home))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty record remains: %v", err)
	}
}

func TestUnlinkRemovesOrphans(t *testing.T) {
	requireSymlinks(t)
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "b", Target: "b"}}
	repo, home := fixture(t, entries...)
	apply(t, entries, repo, home, false)
	apply(t, entries[:1], repo, home, true)
	for _, name := range []string{"a", "b"} {
		if _, err := os.Lstat(filepath.Join(home, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s remains after unlink: %v", name, err)
		}
	}
}

func TestOrphansOnlyIncludeOwnLinks(t *testing.T) {
	requireSymlinks(t)
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "b", Target: "b"}, {Source: "c", Target: "c"}}
	repo, home := fixture(t, entries...)
	apply(t, entries, repo, home, false)
	// The user replaced b with a file, and pointed c somewhere else. Neither
	// is homeskillet's any more.
	b, c := filepath.Join(home, "b"), filepath.Join(home, "c")
	for _, path := range []string{b, c} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(b, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "a"), c); err != nil {
		t.Fatal(err)
	}
	plans, err := Prepare(entries[:1], repo, home, dataDir(home), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := states(plans); len(got) != 1 {
		t.Fatalf("states %v: changed links counted as orphans", got)
	}
	apply(t, entries[:1], repo, home, false)
	if data, err := os.ReadFile(b); err != nil || string(data) != "mine" {
		t.Fatalf("user file changed: %q, %v", data, err)
	}
	if target, err := os.Readlink(c); err != nil || target != filepath.Join(repo, "a") {
		t.Fatalf("user link changed: %q, %v", target, err)
	}
}

func TestRecordKeepsOtherRepositories(t *testing.T) {
	requireSymlinks(t)
	entries := []config.Entry{{Source: "a", Target: "a"}}
	repo, home := fixture(t, entries...)
	other := filepath.Join(filepath.Dir(repo), "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	otherEntries := []config.Entry{{Source: "x", Target: "x"}}
	apply(t, entries, repo, home, false)
	apply(t, otherEntries, other, home, false)
	apply(t, entries, repo, home, true)
	got := recordedTargets(t, home)
	if _, ok := got[repo]; ok || strings.Join(got[other], ",") != "x" {
		t.Fatalf("record %v", got)
	}
}

func TestInvalidRecordsRefused(t *testing.T) {
	entries := []config.Entry{{Source: "a", Target: "a"}}
	repo, home := fixture(t, entries...)
	for name, contents := range map[string]string{
		"not json":        `{`,
		"unknown field":   `{"version": 1, "homes": {}, "extra": true}`,
		"other version":   `{"version": 2, "homes": {}}`,
		"escaping target": `{"version": 1, "homes": {"HOME": {"REPO": [{"target": "../outside", "source": "REPO/a"}]}}}`,
		"foreign source":  `{"version": 1, "homes": {"HOME": {"REPO": [{"target": "a", "source": "/etc/passwd"}]}}}`,
		"relative repo":   `{"version": 1, "homes": {"HOME": {"repo": [{"target": "a", "source": "repo/a"}]}}}`,
		"relative home":   `{"version": 1, "homes": {"home": {"REPO": [{"target": "a", "source": "REPO/a"}]}}}`,
		"escape code":     `{"version": 1, "homes": {"HOME": {"REPO": [{"target": "a\u001b[2K", "source": "REPO/a"}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := RecordPath(dataDir(home))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			contents = strings.ReplaceAll(strings.ReplaceAll(contents, "REPO", repo), "HOME", home)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, unlinking := range []bool{false, true} {
				if _, err := Prepare(entries, repo, home, dataDir(home), unlinking); err == nil || !strings.Contains(err.Error(), "is invalid") {
					t.Errorf("unlinking %v: %v", unlinking, err)
				}
			}
		})
	}
}

func TestSharedRecordDirectoryRefused(t *testing.T) {
	requireModes(t)
	entries := []config.Entry{{Source: "a", Target: "a"}}
	repo, home := fixture(t, entries...)
	apply(t, entries, repo, home, false)
	if err := os.Chmod(dataDir(home), 0o777); err != nil {
		t.Fatal(err)
	}
	// Another user could have planted the record, so linking refuses it, and
	// so does unlinking, which removes the orphaned links the record lists
	// and could not save the record afterward.
	for _, unlinking := range []bool{false, true} {
		if _, err := Prepare(entries, repo, home, dataDir(home), unlinking); err == nil || !strings.Contains(err.Error(), "permissions too open") {
			t.Fatalf("unlinking %v: shared record directory accepted: %v", unlinking, err)
		}
	}
}

func TestDataDirectoryIsNotATarget(t *testing.T) {
	entries := []config.Entry{{Source: "a", Target: ".local/share/homeskillet/links.json"}}
	repo, home := fixture(t, entries...)
	if _, err := Prepare(entries, repo, home, dataDir(home), false); err == nil || !strings.Contains(err.Error(), "data directory") {
		t.Fatalf("record could be replaced by a link: %v", err)
	}
	// Nor may the data directory be in the repository, where the record
	// would be committed.
	if _, err := Prepare(entries[:0], repo, home, filepath.Join(repo, "data"), false); err == nil || !strings.Contains(err.Error(), "inside the repository") {
		t.Fatalf("data directory inside the repository: %v", err)
	}
}

func TestSharedDataDirectoryKeepsHomesApart(t *testing.T) {
	requireSymlinks(t)
	// One absolute data directory serves the real home and a scratch home,
	// as with HOMESKILLET_DATA_DIR set during a --home trial run.
	entries := []config.Entry{{Source: "a", Target: "a"}}
	repo, home := fixture(t, entries...)
	scratch := filepath.Join(filepath.Dir(home), "scratch")
	if err := os.Mkdir(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(filepath.Dir(home), "data")
	link := func(h string, unlinking bool) {
		t.Helper()
		plans, err := Prepare(entries, repo, h, data, unlinking)
		if err != nil {
			t.Fatal(err)
		}
		if unlinking {
			err = Remove(plans, nil)
		} else {
			err = Create(plans, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		save(t, repo, h, data, plans)
	}
	link(home, false)
	link(scratch, false)
	// Unlinking the scratch home leaves the real home's record alone.
	link(scratch, true)
	rec, err := loadRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Homes) != 1 || len(rec.Homes[home][repo]) != 1 {
		t.Fatalf("record %+v", rec.Homes)
	}
	// A home that is not there now, as on an unmounted volume, keeps its
	// record: it may come back, and its links are still homeskillet's.
	link(scratch, false)
	away := scratch + ".unmounted"
	if err := os.Rename(scratch, away); err != nil {
		t.Fatal(err)
	}
	link(home, false)
	if rec, err = loadRecord(data); err != nil || len(rec.Homes) != 2 {
		t.Fatalf("absent home dropped: %+v, %v", rec.Homes, err)
	}
}

func TestScopedSaveKeepsOtherLinks(t *testing.T) {
	requireSymlinks(t)
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "b", Target: "b"}}
	repo, home := fixture(t, entries...)
	apply(t, entries, repo, home, false)
	// Unlink only a, recording only what that run covered.
	plans, err := PrepareSelected(entries, repo, home, dataDir(home), true, func(target, _ string) bool {
		return target == filepath.Join(home, "a")
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("selected plans %v, %v", plans, err)
	}
	if err := Remove(plans, nil); err != nil {
		t.Fatal(err)
	}
	refreshed, err := Refresh(plans)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(repo, home, dataDir(home), refreshed, true); err != nil {
		t.Fatal(err)
	}
	if got := recordedTargets(t, home)[repo]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("recorded %v, want only b", got)
	}
}

func TestRecordRejectsTrailingData(t *testing.T) {
	requireSymlinks(t)
	entries := []config.Entry{{Source: "a", Target: "a"}}
	repo, home := fixture(t, entries...)
	apply(t, entries, repo, home, false)
	// A valid record followed by a second value.
	if err := os.WriteFile(RecordPath(dataDir(home)), []byte(`{"version": 1, "homes": {}} {"version": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecord(dataDir(home)); err == nil {
		t.Fatal("record with trailing data accepted")
	}
}
