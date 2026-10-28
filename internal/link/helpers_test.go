//go:build linux || darwin || windows

// What tests need that differs between systems: skips for symlinks, which
// Windows lets a process make only in Developer Mode or elevated, and for
// Unix permission bits, and a way to let other users write to a file.

package link

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/host"
)

// requireSymlinks skips a test that makes symlinks when this process may
// not, as on Windows outside Developer Mode.
func requireSymlinks(t *testing.T) {
	t.Helper()
	if err := host.CheckSymlinks(); err != nil {
		t.Skip(err)
	}
}

// requireModes skips a test of Unix permission bits on Windows, which has
// none; package trust tests its access control lists instead.
func requireModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits; trust_windows_test.go covers its access control lists")
	}
}

// makeShared lets other users write to path, so a test can check that
// homeskillet refuses it: by mode bits on Linux and macOS, and on Windows by
// granting BUILTIN\Users the right to change it, named by its SID so the
// test does not depend on the system's language.
func makeShared(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o777); err != nil {
			t.Fatal(err)
		}
		return
	}
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	if output, err := exec.CommandContext(t.Context(), icacls, path, "/grant", "*S-1-5-32-545:(M)").CombinedOutput(); err != nil { //nolint:gosec // G204: the test's own fixture
		t.Fatalf("icacls %s: %v\n%s", path, err, output)
	}
}
