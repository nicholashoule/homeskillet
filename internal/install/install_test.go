//go:build linux || darwin

package install

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// fixture returns a layout under an empty prefix and a directory for fake
// programs. The temporary root is canonicalized because macOS exposes its
// temporary directory through a symlink.
func fixture(t *testing.T) (Layout, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	builds := filepath.Join(root, "builds")
	if err := os.Mkdir(builds, 0o700); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "prefix")
	layout, err := New(filepath.Join(prefix, "bin"), filepath.Join(prefix, "share", "homeskillet"))
	if err != nil {
		t.Fatal(err)
	}
	return layout, builds
}

// program writes a fake build with the given contents and returns its path.
func program(t *testing.T, builds, contents string) string {
	t.Helper()
	path := filepath.Join(builds, contents)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// build describes a fake build of version v.
func build(v string) version.Info {
	return version.Info{Version: v, Revision: "0123456789ab", Go: "go1.24.0", Platform: "linux/amd64"}
}

func install(t *testing.T, layout Layout, path, v string) string {
	t.Helper()
	var log bytes.Buffer
	if err := layout.Install(path, build(v), false, &log); err != nil {
		t.Fatalf("install %s: %v", v, err)
	}
	return log.String()
}

// runs returns the contents of the binary the command resolves to, which is
// what a shell would execute.
func runs(t *testing.T, layout Layout) string {
	t.Helper()
	data, err := os.ReadFile(layout.Command())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func links(t *testing.T, layout Layout) (latest, previous string) {
	t.Helper()
	read := func(name string) string {
		target, err := os.Readlink(filepath.Join(layout.Data, name))
		if errors.Is(err, fs.ErrNotExist) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	return read(Latest), read(Previous)
}

func TestLifecycle(t *testing.T) {
	layout, builds := fixture(t)
	output := install(t, layout, program(t, builds, "one"), "v1.0.0")
	for _, want := range []string{"installed\tv1.0.0", "latest\tv1.0.0", "current\tlatest", "linked\t" + layout.Command()} {
		if !strings.Contains(output, want) {
			t.Errorf("first install output lacks %q:\n%s", want, output)
		}
	}
	if latest, previous := links(t, layout); latest != "versions/v1.0.0" || previous != "" {
		t.Fatalf("links after first install: %q, %q", latest, previous)
	}
	if info, err := os.Stat(layout.Program("v1.0.0")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("installed binary: %v, %v", info, err)
	}
	if info, err := os.Stat(layout.Data); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("new directories must be private: %v, %v", info, err)
	}

	// Following latest, a newer version takes over and the old one becomes
	// previous.
	install(t, layout, program(t, builds, "two"), "v1.1.0")
	if got := runs(t, layout); got != "two" {
		t.Fatalf("command runs %q after update", got)
	}
	if latest, previous := links(t, layout); latest != "versions/v1.1.0" || previous != "versions/v1.0.0" {
		t.Fatalf("links after update: %q, %q", latest, previous)
	}

	// An older version is installed beside them without becoming latest.
	install(t, layout, program(t, builds, "old"), "v1.0.5")
	if latest, previous := links(t, layout); latest != "versions/v1.1.0" || previous != "versions/v1.0.5" {
		t.Fatalf("links after older install: %q, %q", latest, previous)
	}

	// Rolling back to previous pins that version, so a later install leaves
	// what runs alone.
	if err := layout.Use(Previous, false, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if got := runs(t, layout); got != "old" {
		t.Fatalf("rollback runs %q", got)
	}
	if output := install(t, layout, program(t, builds, "three"), "v1.2.0"); !strings.Contains(output, "pinned\tv1.0.5") {
		t.Fatalf("install did not report the pin:\n%s", output)
	}
	if got := runs(t, layout); got != "old" {
		t.Fatalf("install changed a pinned version to %q", got)
	}
	if err := layout.Use(Latest, false, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if got := runs(t, layout); got != "three" {
		t.Fatalf("use latest runs %q", got)
	}

	state, err := layout.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"v1.0.0", "v1.0.5", "v1.1.0", "v1.2.0"}; !slices.Equal(state.Versions, want) || state.Current != "v1.2.0" || !state.Following {
		t.Fatalf("state %+v", state)
	}

	// The running version cannot be removed; removing latest's predecessor
	// moves previous.
	if err := layout.Remove("v1.2.0", false, new(bytes.Buffer)); err == nil {
		t.Fatal("removed the running version")
	}
	if err := layout.Remove("v1.1.0", false, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(layout.Program("v1.1.0"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("version directory remains: %v", err)
	}
	if latest, previous := links(t, layout); latest != "versions/v1.2.0" || previous != "versions/v1.0.5" {
		t.Fatalf("links after remove: %q, %q", latest, previous)
	}
}

func TestInstalledVersionsAreImmutable(t *testing.T) {
	layout, builds := fixture(t)
	first := program(t, builds, "first")
	install(t, layout, first, "v1.0.0")
	if output := install(t, layout, first, "v1.0.0"); !strings.Contains(output, "unchanged\tv1.0.0") {
		t.Fatalf("reinstall: %s", output)
	}
	// A rebuild of the same commit differs only in its build time: it counts
	// as installed, and the installed binary stays.
	if output := install(t, layout, program(t, builds, "rebuilt"), "v1.0.0"); !strings.Contains(output, "unchanged\tv1.0.0") {
		t.Fatalf("rebuild of the same source: %s", output)
	}
	other := build("v1.0.0")
	other.Revision = "ba9876543210"
	if err := layout.Install(program(t, builds, "tampered"), other, false, new(bytes.Buffer)); err == nil {
		t.Fatal("rewrote an installed version")
	}
	if got := runs(t, layout); got != "first" {
		t.Fatalf("command runs %q", got)
	}
	// A dirty build of the same version installs beside it, but latest
	// keeps naming the clean build.
	if output := install(t, layout, program(t, builds, "dirty"), "v1.0.0+dirty"); !strings.Contains(output, "note\tv1.0.0 still runs") {
		t.Fatalf("dirty install of the latest version: %s", output)
	}
	if latest, previous := links(t, layout); latest != "versions/v1.0.0" || previous != "versions/v1.0.0+dirty" {
		t.Fatalf("links after dirty install: %q, %q", latest, previous)
	}
	// A +dirty build names no exact build, so a rebuild replaces it.
	install(t, layout, program(t, builds, "dirty1"), "v1.1.0+dirty")
	if output := install(t, layout, program(t, builds, "dirty2"), "v1.1.0+dirty"); !strings.Contains(output, "replaced\tv1.1.0+dirty") {
		t.Fatalf("dirty rebuild: %s", output)
	}
	if got := runs(t, layout); got != "dirty2" {
		t.Fatalf("command runs %q", got)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	layout, builds := fixture(t)
	path := program(t, builds, "one")
	var log bytes.Buffer
	if err := layout.Install(path, build("v1.0.0"), true, &log); err != nil || !strings.Contains(log.String(), "installed\tv1.0.0") {
		t.Fatalf("dry run: %s, %v", log.String(), err)
	}
	prefix := filepath.Dir(layout.Bin)
	if _, err := os.Lstat(prefix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dry run created %s: %v", prefix, err)
	}
	install(t, layout, path, "v1.0.0")
	install(t, layout, program(t, builds, "two"), "v1.1.0")
	before, err := layout.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Use(Previous, true, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if err := layout.Remove("v1.0.0", true, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if err := layout.Install(program(t, builds, "three"), build("v1.2.0"), true, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	after, err := layout.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before.Versions, after.Versions) || before.Current != after.Current || runs(t, layout) != "two" {
		t.Fatalf("dry runs changed %+v to %+v", before, after)
	}
}

func TestCommandConflicts(t *testing.T) {
	for name, create := range map[string]func(path string) error{
		"file":          func(path string) error { return os.WriteFile(path, []byte("go install"), 0o700) },
		"foreign link":  func(path string) error { return os.Symlink("/usr/bin/true", path) },
		"dangling link": func(path string) error { return os.Symlink("missing", path) },
	} {
		t.Run(name, func(t *testing.T) {
			layout, builds := fixture(t)
			if err := os.MkdirAll(layout.Bin, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := create(layout.Command()); err != nil {
				t.Fatal(err)
			}
			if err := layout.Install(program(t, builds, "one"), build("v1.0.0"), false, new(bytes.Buffer)); err == nil {
				t.Fatal("replaced an existing command")
			}
			// The conflict is found before anything is written.
			if _, err := os.Lstat(layout.Data); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("install wrote %s before failing: %v", layout.Data, err)
			}
		})
	}
}

func TestInstallChecksOwnership(t *testing.T) {
	cases := map[string]func(t *testing.T, layout Layout, path string){
		"shared program": func(t *testing.T, _ Layout, path string) {
			chmod(t, path, 0o722)
		},
		"shared bin": func(t *testing.T, layout Layout, _ string) {
			mkdir(t, layout.Bin)
			chmod(t, layout.Bin, 0o777)
		},
		"shared data": func(t *testing.T, layout Layout, _ string) {
			mkdir(t, layout.Data)
			chmod(t, layout.Data, 0o775)
		},
		"shared ancestor": func(t *testing.T, layout Layout, _ string) {
			prefix := filepath.Dir(layout.Bin)
			mkdir(t, layout.Bin)
			chmod(t, prefix, 0o777)
		},
	}
	for name, share := range cases {
		t.Run(name, func(t *testing.T) {
			layout, builds := fixture(t)
			path := program(t, builds, "one")
			share(t, layout, path)
			err := layout.Install(path, build("v1.0.0"), false, new(bytes.Buffer))
			if err == nil || !strings.Contains(err.Error(), "writable by group or others") {
				t.Fatalf("accepted %s: %v", name, err)
			}
		})
	}
}

func TestInvalidVersions(t *testing.T) {
	layout, builds := fixture(t)
	path := program(t, builds, "one")
	for _, v := range []string{"(devel)", "1.0.0", "v1.0.0/../../x", "v1/../2.3.4", "latest", ""} {
		if err := layout.Install(path, build(v), false, new(bytes.Buffer)); err == nil {
			t.Errorf("installed %q", v)
		}
	}
	if _, err := os.Lstat(layout.Data); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("invalid installs wrote %s: %v", layout.Data, err)
	}
	if err := layout.Use(Latest, false, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "nothing is installed") {
		t.Fatalf("use with nothing installed: %v", err)
	}
	install(t, layout, path, "v1.0.0")
	for _, choice := range []string{Previous, "v2.0.0", "current", "../v1.0.0"} {
		if err := layout.Use(choice, false, new(bytes.Buffer)); err == nil {
			t.Errorf("used %q", choice)
		}
	}
	for _, v := range []string{"v2.0.0", "../v1.0.0"} {
		if err := layout.Remove(v, false, new(bytes.Buffer)); err == nil {
			t.Errorf("removed %q", v)
		}
	}
}

func TestRemoveKeepsForeignFiles(t *testing.T) {
	layout, builds := fixture(t)
	install(t, layout, program(t, builds, "one"), "v1.0.0")
	install(t, layout, program(t, builds, "two"), "v1.1.0")
	notes := filepath.Join(filepath.Dir(layout.Program("v1.0.0")), "notes.txt")
	if err := os.WriteFile(notes, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := layout.Remove("v1.0.0", false, new(bytes.Buffer)); err == nil {
		t.Fatal("removed a directory holding a foreign file")
	}
	if _, err := os.Stat(notes); err != nil {
		t.Fatal("foreign file lost:", err)
	}
	if _, previous := links(t, layout); previous != "versions/v1.0.0" {
		t.Fatalf("failed remove moved previous to %q", previous)
	}
}

func TestInspectIgnoresForeignEntries(t *testing.T) {
	layout, builds := fixture(t)
	install(t, layout, program(t, builds, "one"), "v1.0.0")
	root := filepath.Join(layout.Data, "versions")
	mkdir(t, filepath.Join(root, "not-a-version"))
	mkdir(t, filepath.Join(root, "v9.0.0"))
	if err := os.Symlink("v1.0.0", filepath.Join(root, "v8.0.0")); err != nil {
		t.Fatal(err)
	}
	state, err := layout.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.Versions, []string{"v1.0.0"}) {
		t.Fatalf("versions %v", state.Versions)
	}
}

func TestInterruptedRunsRecover(t *testing.T) {
	layout, builds := fixture(t)
	install(t, layout, program(t, builds, "one"), "v1.0.0")
	// Leftovers from a run that stopped between creating a temporary link or
	// binary and renaming it.
	if err := os.Symlink("versions/v0.0.1", filepath.Join(layout.Data, ".latest.new")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(layout.Program("v1.0.0"))
	if err := os.WriteFile(filepath.Join(dir, safefile.TempPrefix+"123"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	install(t, layout, program(t, builds, "two"), "v1.1.0")
	if err := layout.Remove("v1.0.0", false, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("version directory remains: %v", err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRecords(t *testing.T) {
	layout, builds := fixture(t)
	install(t, layout, program(t, builds, "one"), "v1.0.0")
	got, err := layout.Build("v1.0.0")
	if err != nil || got != build("v1.0.0") {
		t.Fatalf("Build = %+v, %v", got, err)
	}
	// A missing record is restored by reinstalling the same bytes.
	record := filepath.Join(filepath.Dir(layout.Program("v1.0.0")), buildFile)
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.Build("v1.0.0"); err == nil {
		t.Fatal("read a missing record")
	}
	install(t, layout, filepath.Join(builds, "one"), "v1.0.0")
	if _, err := layout.Build("v1.0.0"); err != nil {
		t.Fatalf("record not restored: %v", err)
	}
	// An oversized record is refused rather than read into memory.
	if err := os.WriteFile(record, bytes.Repeat([]byte(" "), maxBuildFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.Build("v1.0.0"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized record: %v", err)
	}
	for _, v := range []string{"../v1.0.0", ""} {
		if _, err := layout.Build(v); err == nil {
			t.Errorf("Build(%q) succeeded", v)
		}
	}
}

func TestEqualFiles(t *testing.T) {
	dir := t.TempDir()
	block := bytes.Repeat([]byte("x"), 64<<10)
	contents := map[string][]byte{
		"empty":     nil,
		"small":     []byte("homeskillet"),
		"other":     []byte("homeskillex"),
		"large":     append(slices.Clone(block), block...),
		"lastByte":  append(append(slices.Clone(block), block[:len(block)-1]...), 'y'),
		"plusOne":   append(append(slices.Clone(block), block...), 'x'),
		"largeCopy": append(slices.Clone(block), block...),
	}
	open := func(name string) *os.File {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, contents[name], 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return file
	}
	for _, test := range []struct {
		a, b string
		want bool
	}{
		{"empty", "empty", true},
		{"small", "small", true},
		{"small", "other", false},
		{"large", "largeCopy", true},
		{"large", "lastByte", false},
		{"large", "plusOne", false},
	} {
		got, err := equalFiles(open(test.a), open(test.b))
		if err != nil || got != test.want {
			t.Errorf("equalFiles(%s, %s) = %v, %v; want %v", test.a, test.b, got, err, test.want)
		}
	}
}

func TestLayoutsOutsideAllowedLocations(t *testing.T) {
	for _, prefix := range []string{"/", "/usr", "/usr/local", "/etc", "relative"} {
		if _, err := New(filepath.Join(prefix, "bin"), filepath.Join(prefix, "share", "homeskillet")); err == nil {
			t.Errorf("New under %q succeeded", prefix)
		}
	}
	// The zero Layout's empty paths would mean the current directory.
	var zero Layout
	_, builds := fixture(t)
	path := program(t, builds, "one")
	checks := map[string]error{
		"Install": zero.Install(path, build("v1.0.0"), false, nil),
		"Use":     zero.Use(Latest, false, nil),
		"Remove":  zero.Remove("v1.0.0", false, nil),
	}
	_, checks["Inspect"] = zero.Inspect()
	_, checks["Build"] = zero.Build("v1.0.0")
	for name, err := range checks {
		if err == nil {
			t.Errorf("zero Layout %s succeeded", name)
		}
	}
}

func TestNilLog(t *testing.T) {
	layout, builds := fixture(t)
	for _, v := range []string{"v1.0.0", "v1.1.0"} {
		if err := layout.Install(program(t, builds, v), build(v), false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := layout.Use(Previous, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := layout.Use(Latest, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := layout.Remove("v1.0.0", false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveRefusesDirectories(t *testing.T) {
	layout, builds := fixture(t)
	install(t, layout, program(t, builds, "one"), "v1.0.0")
	install(t, layout, program(t, builds, "two"), "v1.1.0")
	// Even named like a leftover of Install's, a directory is not one.
	dir := filepath.Join(filepath.Dir(layout.Program("v1.0.0")), safefile.TempPrefix+"dir")
	if err := os.MkdirAll(filepath.Join(dir, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	latest, previous := links(t, layout)
	if err := layout.Remove("v1.0.0", false, nil); err == nil {
		t.Fatal("removed a version directory holding a directory")
	}
	if l, p := links(t, layout); l != latest || p != previous {
		t.Fatalf("links changed before Remove failed: %q, %q", l, p)
	}
}

func TestReplaceLinkLeavesLeftovers(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "latest")
	// A leftover from an interrupted run of a process with this PID.
	leftover := filepath.Join(dir, safefile.TempPrefix+"latest."+strconv.Itoa(os.Getpid())+".0")
	if err := os.Symlink("stale", leftover); err != nil {
		t.Fatal(err)
	}
	if err := replaceLink("versions/v1.0.0", name); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(name); err != nil || target != "versions/v1.0.0" {
		t.Fatalf("link %q, %v", target, err)
	}
	if target, err := os.Readlink(leftover); err != nil || target != "stale" {
		t.Fatalf("leftover reused: %q, %v", target, err)
	}
}
