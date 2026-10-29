//go:build linux || darwin || windows

// Finding programs for setup steps: the PATH they run with, the inherited one
// with the directories installers put programs in appended, command lookup on
// it, and the provides check, which also looks in Homebrew's directories.

package setup

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/nicholashoule/homeskillet/internal/trust"
)

// searchPath returns the PATH that steps run with, and that command lookups
// and Provides checks search: the inherited PATH, with each of the
// directories installers put programs in (see appended) added at the end
// when it exists, is not listed yet, and passes trust.CheckDirectory.
//
// Shells usually add ~/.local/bin only at the next login, and only if it
// already existed, and Windows installers record the directories they add in
// the registry, which only processes started afterward read. So on a fresh
// machine a step that runs a program an earlier step just installed would
// fail, and a Provides check would never see it. Appending rather than
// prepending means nothing installed there can shadow a system command such
// as bash or curl that a later step relies on, and the ownership check means
// a directory that others can write never joins PATH. It is recomputed before
// each step, so a directory an earlier step created counts.
func searchPath(home string) string {
	path := os.Getenv("PATH")
	listed := filepath.SplitList(path)
	for _, dir := range appended(home) {
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) || slices.ContainsFunc(listed, func(entry string) bool {
			return entry != "" && trust.SamePath(filepath.Clean(entry), dir)
		}) {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() || trust.CheckDirectory(dir) != nil {
			continue
		}
		listed = append(listed, dir)
		if path == "" {
			path = dir
		} else {
			path += string(os.PathListSeparator) + dir
		}
	}
	return path
}

// Provided returns where the program name is installed for steps run with
// HOME set to home, or "": the lookup a step's Provides gets (see provided),
// for checks such as homeskillet prepare's.
func Provided(name, home string) string {
	return provided(name, home)
}

// provided returns where the program a step's Provides names is installed,
// or "" if it is not: on the step's search path (see searchPath) or in
// Homebrew's bin directories. Nothing is run; this only looks for a program.
//
// A Homebrew directory and the program found there count only when owned by
// the current user or root (see trust.CheckOwner). /home/linuxbrew is an
// ordinary home directory, so on a shared machine a user named linuxbrew could
// otherwise plant programs there that make other users' steps look done.
func provided(name, home string) string {
	if program, err := lookPath(name, searchPath(home)); err == nil {
		return program
	}
	for _, dir := range homebrewBins {
		if trust.CheckOwner(dir) != nil {
			continue
		}
		if program, err := lookPath(name, dir); err == nil && trust.CheckOwner(program) == nil {
			return program
		}
	}
	return ""
}
