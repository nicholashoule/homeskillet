//go:build linux || darwin || windows

// Finding linked commands that a shell would run instead of another of the
// same name on PATH, such as a ~/.local/bin/sudo, which status and link point
// out.

package cli

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// shadowed returns, for each plan that is or will be linked where a shell
// finds it before a command of the same name later on PATH, and after none
// earlier, that later command: a linked ~/.local/bin/sudo, say, runs instead of /usr/bin/sudo.
// Linking one is allowed, as a newer tool or a wrapper in ~/.local/bin is
// common, but it is pointed out, because a repository from someone else could
// use one to capture what is typed into it, a sudo password included. With
// force, conflicts count too, since they are about to be linked.
//
// home's userBins count as first on PATH even when they are not there yet.
// Relative PATH entries are never searched, as for setup steps. Aliases and
// functions in a linked startup file can stand in for commands too, which no
// check of file names can see; that is why only a trusted repository should
// be linked at all.
func shadowed(plans []link.Plan, home string, force bool) map[string]string {
	var dirs []string
	for _, dir := range userBins {
		dirs = append(dirs, filepath.Join(home, dir))
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		// Targets are canonical, so PATH entries must be too to match them.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		if !slices.ContainsFunc(dirs, func(listed string) bool { return trust.SamePath(listed, dir) }) {
			dirs = append(dirs, dir)
		}
	}
	found := map[string]string{}
	for _, plan := range plans {
		if plan.State != link.Linked && plan.State != link.Missing && (!force || plan.State != link.Conflict) {
			continue
		}
		at := slices.IndexFunc(dirs, func(dir string) bool { return trust.SamePath(dir, filepath.Dir(plan.Target)) })
		source, err := os.Stat(plan.Source)
		if at < 0 || err != nil || !executable(plan.Target, source) {
			continue
		}
		// A command of the same name earlier on PATH runs instead of the
		// link, so the link takes the place of nothing.
		name := commandName(plan.Target)
		if command(dirs[:at], name, source) != "" {
			continue
		}
		if other := command(dirs[at+1:], name, source); other != "" {
			found[plan.Target] = other
		}
	}
	return found
}

// command returns the first command named name in dirs, as a shell's search
// would find it, leaving out source itself, or "" if there is none.
func command(dirs []string, name string, source os.FileInfo) string {
	for _, dir := range dirs {
		for _, path := range candidates(dir, name) {
			if info, err := os.Stat(path); err == nil && executable(path, info) && !os.SameFile(info, source) {
				return path
			}
		}
	}
	return ""
}
