//go:build linux || darwin

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/trust"
)

// userBin is where installers conventionally put a user's own programs,
// relative to the home directory: uv's and many other installers, pipx, and
// homeskillet's own install among them.
const userBin = ".local/bin"

// searchPath returns the PATH that steps run with, and that command lookups
// and Provides checks search: the inherited PATH, with home's .local/bin
// appended when it exists, is not listed yet, and passes
// trust.CheckDirectory.
//
// Shells usually add ~/.local/bin only at the next login, and only if it
// already existed, so on a fresh machine a step that runs a program an earlier
// step just installed there would fail, and a Provides check would never see
// it. Appending rather than prepending means nothing installed there can
// shadow a system command such as bash or curl that a later step relies on,
// and the ownership check means a directory that others can write never
// joins PATH. It is recomputed before each step, so a directory an earlier
// step created counts.
func searchPath(home string) string {
	inherited := os.Getenv("PATH")
	bin := filepath.Join(home, userBin)
	if slices.Contains(filepath.SplitList(inherited), bin) {
		return inherited
	}
	if info, err := os.Stat(bin); err != nil || !info.IsDir() || trust.CheckDirectory(bin) != nil {
		return inherited
	}
	if inherited == "" {
		return bin
	}
	return inherited + string(os.PathListSeparator) + bin
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

// provided reports whether the program a step's Provides names is installed:
// on the step's search path (see searchPath) or in Homebrew's bin
// directories. Nothing is run; this only looks for an executable file.
//
// A Homebrew directory and the program found there count only when owned by
// the current user or root (see trust.CheckOwner). /home/linuxbrew is an
// ordinary home directory, so on a shared machine a user named linuxbrew could
// otherwise plant programs there that make other users' steps look done.
func provided(name, home string) bool {
	if _, err := lookPath(name, searchPath(home)); err == nil {
		return true
	}
	for _, dir := range homebrewBins {
		if trust.CheckOwner(dir) != nil {
			continue
		}
		if program, err := lookPath(name, dir); err == nil && trust.CheckOwner(program) == nil {
			return true
		}
	}
	return false
}

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
