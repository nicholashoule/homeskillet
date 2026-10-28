//go:build linux || darwin || windows

// What tests need that differs between systems: skips for symlinks, which
// Windows lets a process make only in Developer Mode or elevated, for Unix
// permission bits and paths such as /etc, a setup step every system can run,
// and a way to let other users write to a file.

package cli

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

// requireUnix skips a test of what only Linux and macOS have, such as
// permission bits or /etc; cli_windows_test.go and package trust cover their
// Windows counterparts.
func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("tests Linux and macOS behavior; see the Windows tests")
	}
}

// touchCommand returns a command that creates the file name in $HOME: with
// sh on Linux and macOS, and on Windows with Windows PowerShell, which every
// Windows has.
func touchCommand(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
			"New-Item -ItemType File -Path (Join-Path $env:HOME '" + name + "') | Out-Null"}
	}
	return []string{"sh", "-c", `: > "$HOME/$1"`, "sh", name}
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
