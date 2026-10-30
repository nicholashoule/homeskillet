// Tests of installing homeskillet's own versions on Windows: the command
// links straight to the chosen version's program, current is a file, and
// choosing a version renames a new link over the command.

package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// fixture returns a layout under an empty prefix and a directory for fake
// programs, skipping when this process may not make symlinks, as outside
// Developer Mode.
func fixture(t *testing.T) (Layout, string) {
	t.Helper()
	if err := host.CheckSymlinks(); err != nil {
		t.Skip(err)
	}
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
	path := filepath.Join(builds, contents+".exe")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func install(t *testing.T, layout Layout, path, v string) string {
	t.Helper()
	var log bytes.Buffer
	info := version.Info{Version: v, Revision: "0123456789ab", Go: "go1.24.0", Platform: "windows/amd64"}
	if err := layout.Install(path, info, false, &log); err != nil {
		t.Fatalf("install %s: %v", v, err)
	}
	return log.String()
}

// runs returns the contents of the program the command links to, which is
// what Windows would run.
func runs(t *testing.T, layout Layout) string {
	t.Helper()
	data, err := os.ReadFile(layout.Command())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLifecycleWindows(t *testing.T) {
	layout, builds := fixture(t)
	if !strings.HasSuffix(layout.Command(), `\homeskillet.exe`) || !strings.HasSuffix(layout.Program("v1.0.0"), `\v1.0.0\homeskillet.exe`) {
		t.Fatalf("command %s and program %s lack .exe", layout.Command(), layout.Program("v1.0.0"))
	}
	output := install(t, layout, program(t, builds, "one"), "v1.0.0")
	for _, want := range []string{"installed\tv1.0.0", "current\tlatest", "linked\t" + layout.Command() + " -> " + layout.Program("v1.0.0")} {
		if !strings.Contains(output, want) {
			t.Errorf("first install output lacks %q:\n%s", want, output)
		}
	}
	// No latest or previous links: Windows could not replace them.
	for _, name := range []string{Latest, Previous} {
		if _, err := os.Lstat(filepath.Join(layout.Data, name)); err == nil {
			t.Errorf("%s exists", name)
		}
	}
	if current, err := os.ReadFile(filepath.Join(layout.Data, "current")); err != nil || string(current) != "latest\n" {
		t.Fatalf("current holds %q, %v", current, err)
	}
	if got := runs(t, layout); got != "one" {
		t.Fatalf("command runs %q", got)
	}
	// A newer version is followed: the command now links to it.
	output = install(t, layout, program(t, builds, "two"), "v1.1.0")
	if !strings.Contains(output, "linked\t"+layout.Command()+" -> "+layout.Program("v1.1.0")) || runs(t, layout) != "two" {
		t.Fatalf("install of a newer version not followed:\n%s", output)
	}
	state, err := layout.Inspect()
	if err != nil || state.Latest() != "v1.1.0" || state.Previous() != "v1.0.0" || !state.Following {
		t.Fatalf("Inspect = %+v, %v", state, err)
	}
	// Pinning the previous version links to it, and a later install keeps
	// the pin.
	var log bytes.Buffer
	if err := layout.Use(Previous, false, &log); err != nil || runs(t, layout) != "one" {
		t.Fatalf("use previous: %v; runs %q\n%s", err, runs(t, layout), log.String())
	}
	if current, err := os.ReadFile(filepath.Join(layout.Data, "current")); err != nil || string(current) != `versions\v1.0.0`+"\n" {
		t.Fatalf("pinned current holds %q, %v", current, err)
	}
	install(t, layout, program(t, builds, "three"), "v1.2.0")
	if got := runs(t, layout); got != "one" {
		t.Fatalf("install changed a pinned command to %q", got)
	}
	if err := layout.Remove([]string{"v1.0.0"}, false, nil); err == nil {
		t.Fatal("removed the pinned version")
	}
	if err := layout.Use(Latest, false, nil); err != nil || runs(t, layout) != "three" {
		t.Fatalf("use latest: %v", err)
	}
	if err := layout.Remove([]string{"v1.0.0"}, false, nil); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(layout.Program("v1.0.0"))); err == nil {
		t.Fatal("removed version's directory remains")
	}
}

func TestCommandConflictWindows(t *testing.T) {
	layout, builds := fixture(t)
	if err := os.MkdirAll(layout.Bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Command(), []byte("someone else's"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := layout.Install(program(t, builds, "one"), version.Info{Version: "v1.0.0"}, false, nil); err == nil {
		t.Fatal("replaced a command homeskillet did not make")
	}
	if data, err := os.ReadFile(layout.Command()); err != nil || string(data) != "someone else's" {
		t.Fatalf("command changed: %q, %v", data, err)
	}
}

func TestDryRunWindows(t *testing.T) {
	layout, builds := fixture(t)
	var log bytes.Buffer
	if err := layout.Install(program(t, builds, "one"), version.Info{Version: "v1.0.0"}, true, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "linked\t") {
		t.Fatalf("dry run did not say what it would link:\n%s", log.String())
	}
	if _, err := os.Lstat(filepath.Dir(layout.Bin)); err == nil {
		t.Fatal("dry run created the prefix")
	}
}
