//go:build linux || darwin || windows

// Tests of how advice names homeskillet: the installed command, or the path
// a download was run by.

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNameFor(t *testing.T) {
	requireSymlinks(t)
	dir := t.TempDir()
	version := filepath.Join(dir, "versions", "v0.4.0", "homeskillet")
	if err := os.MkdirAll(filepath.Dir(version), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(version, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "homeskillet")
	if err := os.Symlink(version, command); err != nil {
		t.Fatal(err)
	}
	download := filepath.Join(dir, "homeskillet-windows-amd64.exe")
	if err := os.WriteFile(download, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, arg0, executable, onPath, want string }{
		{"the installed command", "homeskillet", version, command, "homeskillet"},
		{"the installed command run by its path", command, version, command, "homeskillet"},
		// As each system's shell takes it: a backslash needs quoting outside Windows.
		{"a download, before any install", `.\homeskillet-windows-amd64.exe`, download, "", commandWord(`.\homeskillet-windows-amd64.exe`)},
		{"a download, with an older version installed", "./homeskillet-linux-amd64", download, command, "./homeskillet-linux-amd64"},
		{"an unprintable path", "./bad\x1b[2J", download, "", "homeskillet"},
		// Windows passes the full path, which may hold a space.
		{"a download in a folder with a space", filepath.Join(dir, "John Smith", "homeskillet-windows-amd64.exe"), download, "",
			commandWord(filepath.Join(dir, "John Smith", "homeskillet-windows-amd64.exe"))},
	} {
		if got := nameFor(test.arg0, test.executable, test.onPath); got != test.want {
			t.Errorf("%s: %q, want %q", test.name, got, test.want)
		}
	}
}
