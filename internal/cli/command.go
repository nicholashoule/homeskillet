//go:build linux || darwin || windows

// How to name homeskillet in advice about what to run next: as the person
// ran it, when that is not the installed command.

package cli

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/install"
)

// selfName returns how to run this program again in advice such as "run
// homeskillet prepare again": homeskillet when that command on PATH is this
// program, and otherwise the path it was run by, as before it is installed,
// when the first run is the download, such as homeskillet-windows-amd64.exe.
// Tests replace it, since a test binary is neither.
var selfName = func() string {
	executable, err := os.Executable()
	if err != nil {
		return install.Binary
	}
	onPath, _ := exec.LookPath(install.Binary)
	return nameFor(os.Args[0], executable, onPath)
}

// nameFor decides selfName from the first argument the program was run
// with, the program itself, and the homeskillet found on PATH, "" for none.
// Symlinks are resolved, since the installed command is a link to the
// version it runs.
func nameFor(arg0, executable, onPath string) string {
	if onPath != "" && samePath(onPath, executable) {
		return install.Binary
	}
	if arg0 == "" || !config.Printable(arg0) {
		return install.Binary
	}
	return arg0
}

// samePath reports whether a and b name the same file once symlinks are
// resolved.
func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}
