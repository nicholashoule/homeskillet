//go:build linux || darwin

// Finding programs on Linux and macOS: the home's .local/bin, Homebrew's
// directories, and lookup of executable files.

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// userBin is where installers conventionally put a user's own programs,
// relative to the home directory: uv's and many other installers, pipx, and
// homeskillet's own install among them.
const userBin = ".local/bin"

// appended returns the directories searchPath adds to PATH: the home's
// .local/bin.
func appended(home string) []string {
	return []string{filepath.Join(home, userBin)}
}

// homebrewBins are Homebrew's standard bin directories: Apple silicon macOS,
// Intel macOS, and Linux. Homebrew installs programs there, but shells add
// them to PATH only once a startup file runs brew shellenv, usually at the
// next login, so right after a Homebrew install a Provides check on PATH alone
// would offer the step again.
//
// Provides checks therefore also look here. Steps do not get these
// directories on their PATH: Homebrew normally makes them writable by the
// admin group, so they fail the ownership rules searchPath applies. A step
// that needs brew right after installing it calls it by its full path.
var homebrewBins = []string{"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin"}

// lookPath finds the program a step names, as exec.LookPath does but
// searching path rather than the process's own PATH, which lacks what
// searchPath adds. A name containing a slash is returned unchanged; exec
// resolves a relative one against the repository, the step's directory.
//
// Relative PATH entries, including the empty entry that means ".", are
// skipped, as exec.LookPath has refused matches in the current directory
// since Go 1.19: a step runs in the repository, and a file there must not be
// able to stand in for a command.
func lookPath(name, path string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q not found on PATH", name)
}

// documents returns the Documents folder in home, where a step's creates
// path after config.DocumentsPrefix lies. macOS and the XDG user directories
// most Linux desktops use keep it there.
func documents(home string) (string, error) {
	return filepath.Join(home, "Documents"), nil
}
