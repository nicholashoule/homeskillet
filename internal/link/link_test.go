//go:build linux || darwin

package link

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// fixture creates a private repository holding each entry's source and a
// matching manifest, plus an empty home directory. The temporary root is
// canonicalized because macOS exposes its temporary directory through a
// symlink, and Prepare requires canonical paths.
func fixture(t *testing.T, entries ...config.Entry) (repo, home string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, home = filepath.Join(root, "repo"), filepath.Join(root, "home")
	for _, directory := range []string{repo, home} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range entries {
		path := filepath.Join(repo, entry.Source)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(config.Manifest{Files: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, home
}

func states(plans []Plan) []State {
	var result []State
	for _, plan := range plans {
		result = append(result, plan.State)
	}
	return result
}

func TestConflicts(t *testing.T) {
	entries := []config.Entry{{Source: "first", Target: "first"}, {Source: "second", Target: "second"}}
	for _, kind := range []string{"file", "directory", "foreign-link"} {
		t.Run(kind, func(t *testing.T) {
			repo, home := fixture(t, entries...)
			first, second := filepath.Join(home, "first"), filepath.Join(home, "second")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(second, []byte("keep"), 0o600)
			case "directory":
				err = os.Mkdir(second, 0o755)
			case "foreign-link":
				err = os.Symlink("missing-unrelated-file", second)
			}
			if err != nil {
				t.Fatal(err)
			}
			plans, err := Prepare(entries, repo, home, dataDir(home), false)
			if err != nil || !slices.Equal(states(plans), []State{Missing, Conflict}) {
				t.Fatalf("prepare: %v, %v", plans, err)
			}
			if err := Create(plans, io.Discard); !errors.Is(err, ErrBlocked) {
				t.Fatalf("create was not blocked: %v", err)
			}
			if _, err := os.Lstat(first); !os.IsNotExist(err) {
				t.Fatalf("partial link occurred: %v", err)
			}
			if err := os.Symlink(filepath.Join(repo, "first"), first); err != nil {
				t.Fatal(err)
			}
			plans, err = Prepare(entries, repo, home, dataDir(home), true)
			if err != nil {
				t.Fatal(err)
			}
			var log bytes.Buffer
			if err := Remove(plans, &log); err != nil || !strings.Contains(log.String(), "skipped\t"+second) {
				t.Fatalf("remove did not skip conflict: %s, %v", log.String(), err)
			}
			if _, err := os.Lstat(first); !os.IsNotExist(err) {
				t.Fatalf("owned link kept: %v", err)
			}
			if _, err := os.Lstat(second); err != nil {
				t.Fatal("conflicting file was removed", err)
			}
		})
	}
}

func TestMissingSource(t *testing.T) {
	entries := []config.Entry{{Source: "one", Target: "one"}, {Source: "two", Target: "two"}}
	repo, home := fixture(t, entries...)
	two := filepath.Join(home, "two")
	if err := os.Symlink(filepath.Join(repo, "two"), two); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "two")); err != nil {
		t.Fatal(err)
	}
	plans, err := Prepare(entries, repo, home, dataDir(home), false)
	if err != nil || !slices.Equal(states(plans), []State{Missing, NoSource}) {
		t.Fatalf("prepare: %v, %v", plans, err)
	}
	if err := Create(plans, io.Discard); !errors.Is(err, ErrBlocked) {
		t.Fatalf("create was not blocked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "one")); !os.IsNotExist(err) {
		t.Fatalf("partial link occurred: %v", err)
	}
	plans, err = Prepare(entries, repo, home, dataDir(home), true)
	if err != nil || !slices.Equal(states(plans), []State{Missing, Linked}) {
		t.Fatalf("prepare to unlink: %v, %v", plans, err)
	}
	if err := Remove(plans, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(two); !os.IsNotExist(err) {
		t.Fatalf("dangling link kept: %v", err)
	}
}

func TestOverlappingTargets(t *testing.T) {
	cases := map[string][]config.Entry{
		"duplicate": {{Source: "one", Target: "same"}, {Source: "two", Target: "same"}},
		"nested":    {{Source: "one", Target: "nested"}, {Source: "two", Target: "nested/file"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			repo, home := fixture(t, entries...)
			if _, err := Prepare(entries, repo, home, dataDir(home), false); err == nil {
				t.Fatal("overlapping targets accepted")
			}
		})
	}
}

func TestRepositoryTargetRejected(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: "config"}}
	repo, _ := fixture(t, entries...)
	if _, err := Prepare(entries, repo, repo, dataDir(repo), false); err == nil {
		t.Fatal("allowed modifying repository through target")
	}
}

func TestSymlinkEscapes(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: ".config/config"}}
	for _, side := range []string{"source", "target-parent"} {
		t.Run(side, func(t *testing.T) {
			repo, home := fixture(t, entries...)
			outside := t.TempDir()
			if side == "source" {
				if err := os.Remove(filepath.Join(repo, "config")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outside, "config"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "config"), filepath.Join(repo, "config")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(outside, filepath.Join(home, ".config")); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(entries, repo, home, dataDir(home), false); err == nil {
				t.Fatal("symlink escape accepted")
			}
		})
	}
}

func TestRelativeOwnedLink(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: "config"}}
	repo, home := fixture(t, entries...)
	relative, err := filepath.Rel(home, filepath.Join(repo, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, filepath.Join(home, "config")); err != nil {
		t.Fatal(err)
	}
	if plans, err := Prepare(entries, repo, home, dataDir(home), false); err != nil || !slices.Equal(states(plans), []State{Linked}) {
		t.Fatalf("relative link not recognized: %v, %v", plans, err)
	}
}

func TestNewParentsArePrivate(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: ".ssh/config"}}
	repo, home := fixture(t, entries...)
	plans, err := Prepare(entries, repo, home, dataDir(home), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := Create(plans, io.Discard); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(home, ".ssh")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("parent not private: %v, %v", info, err)
	}
}

func TestRepositoryMustBePrivate(t *testing.T) {
	entries := []config.Entry{{Source: "home/config", Target: "config"}, {Source: "links/alias", Target: "alias"}}
	// Paths are relative to the fixture root, the repository's parent.
	cases := map[string]struct {
		path string
		mode os.FileMode
	}{
		"ancestor":            {"", 0o777},
		"repository":          {"repo", 0o775},
		"manifest":            {"repo/dotfiles.json", 0o606},
		"source directory":    {"repo/home", 0o757},
		"source":              {"repo/home/config", 0o620},
		"symlink directory":   {"repo/links", 0o777},
		"symlink destination": {"repo/shared/real", 0o666},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			repo, home := fixture(t, entries...)
			// links/alias -> ../shared/real resolves into a directory that the
			// path as written never passes through, so both walks are needed.
			if err := os.Mkdir(filepath.Join(repo, "shared"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "shared", "real"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(repo, "links", "alias")
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../shared/real", alias); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(entries, repo, home, dataDir(home), false); err != nil {
				t.Fatalf("private fixture rejected: %v", err)
			}
			if err := os.Chmod(filepath.Join(filepath.Dir(repo), test.path), test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(entries, repo, home, dataDir(home), false); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
				t.Fatalf("accepted shared %s: %v", name, err)
			}
			if _, err := Prepare(entries, repo, home, dataDir(home), true); err != nil {
				t.Fatalf("unlinking blocked by shared %s: %v", name, err)
			}
		})
	}
}

func TestTargetDirectoriesMustBePrivate(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: ".config/app/config"}}
	// Paths are relative to home. .config/app does not exist yet, and link
	// creates it private, so only directories that already exist matter.
	for name, path := range map[string]string{"home": "", "existing parent": ".config"} {
		t.Run(name, func(t *testing.T) {
			repo, home := fixture(t, entries...)
			// The data directory is outside home, so its own checks cannot
			// be what catches a shared home.
			data := filepath.Join(filepath.Dir(home), "data")
			if err := os.Mkdir(filepath.Join(home, ".config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(entries, repo, home, data, false); err != nil {
				t.Fatalf("private home rejected: %v", err)
			}
			// Anyone who can write here can replace the link with their own
			// file, which then runs as the user.
			if err := os.Chmod(filepath.Join(home, path), 0o777); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(entries, repo, home, data, false); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
				t.Fatalf("accepted shared %s: %v", name, err)
			}
			if _, err := Prepare(entries, repo, home, data, true); err != nil {
				t.Fatalf("unlinking blocked by shared %s: %v", name, err)
			}
		})
	}
}

func TestStickyAncestorAllowed(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: "config"}}
	repo, home := fixture(t, entries...)
	if err := os.Chmod(filepath.Dir(repo), os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(entries, repo, home, dataDir(home), false); err != nil {
		t.Fatalf("sticky ancestor rejected: %v", err)
	}
}

func TestSystemLocationsRefused(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: "config"}}
	repo, home := fixture(t, entries...)
	for _, unlinking := range []bool{false, true} {
		for _, dirs := range [][2]string{{repo, "/"}, {repo, "/etc"}, {"/usr", home}, {repo, ""}, {"relative", home}} {
			if _, err := Prepare(entries, dirs[0], dirs[1], dataDir(dirs[1]), unlinking); err == nil {
				t.Errorf("Prepare(%q, %q, unlinking %v) succeeded", dirs[0], dirs[1], unlinking)
			}
		}
	}
}

func TestNilLog(t *testing.T) {
	entries := []config.Entry{{Source: "config", Target: "config"}}
	repo, home := fixture(t, entries...)
	plans, err := Prepare(entries, repo, home, dataDir(home), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := Create(plans, nil); err != nil {
		t.Fatal(err)
	}
	if plans, err = Prepare(entries, repo, home, dataDir(home), true); err != nil {
		t.Fatal(err)
	}
	if err := Remove(plans, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEverySymlinkHopIsChecked(t *testing.T) {
	entries := []config.Entry{{Source: "home/entry", Target: "entry"}}
	for name, hop := range map[string]func(t *testing.T, repo string) string{
		// entry -> outside/hop -> repo/real: the final destination is
		// inside, but whoever controls outside can retarget hop later.
		"bounce outside": func(t *testing.T, repo string) string {
			dir := filepath.Join(filepath.Dir(repo), "outside")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "hop")
		},
		// entry -> repo/shared/hop -> repo/real: shared is on neither the
		// path as written nor the resolved one.
		"shared directory inside": func(t *testing.T, repo string) string {
			dir := filepath.Join(repo, "shared")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "hop")
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo, home := fixture(t, config.Entry{Source: "real", Target: "real"})
			hop := hop(t, repo)
			if err := os.Symlink(filepath.Join(repo, "real"), hop); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repo, "home"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(hop, filepath.Join(repo, "home", "entry")); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(entries, repo, home, dataDir(home), false); err == nil {
				t.Fatal("source reached through an unchecked hop was accepted")
			}
		})
	}
}

func TestTargetsMayNotContainRepositoryOrData(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	// The repository is cloned inside home, as ~/.config/dotfiles.
	repo := filepath.Join(home, ".config", "dotfiles")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for target, data := range map[string]string{
		".config": filepath.Join(root, "data"),           // contains the repository
		".local":  filepath.Join(home, ".local", "data"), // contains the data directory
	} {
		entries := []config.Entry{{Source: "config", Target: target}}
		for _, unlinking := range []bool{false, true} {
			if _, err := Prepare(entries, repo, home, data, unlinking); err == nil || !strings.Contains(err.Error(), "overlaps") {
				t.Errorf("target %s accepted (unlinking %v): %v", target, unlinking, err)
			}
		}
	}
}

func TestForceMovesNoDirectories(t *testing.T) {
	entries := []config.Entry{{Source: "a", Target: "a"}, {Source: "b", Target: "b"}}
	repo, home := fixture(t, entries...)
	if err := os.WriteFile(filepath.Join(home, "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "b", "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	plans, err := Prepare(entries, repo, home, dataDir(home), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckForce(plans); err == nil {
		t.Fatal("CheckForce accepted a directory")
	}
	// Nothing moves, not even the file before the directory.
	if _, _, err := Backup(plans, home, dataDir(home), nil); err == nil {
		t.Fatal("Backup moved a directory")
	}
	if data, err := os.ReadFile(filepath.Join(home, "a")); err != nil || string(data) != "a" {
		t.Fatalf("file moved although Backup failed: %q, %v", data, err)
	}
}
