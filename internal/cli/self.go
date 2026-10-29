//go:build linux || darwin || windows

// The self command: installing, listing, choosing, and removing versions of
// homeskillet itself, through package install; self update and self releases
// are in update.go.

package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// runSelf manages installed versions of homeskillet under the prefix,
// .local in the home directory unless --prefix says otherwise.
func runSelf(ctx context.Context, inv invocation, home string, output, diagnostics io.Writer) error {
	layout, err := selfLayout(inv, home)
	if err != nil {
		return err
	}
	switch inv.sub {
	case "list":
		return listVersions(layout, output)
	case "install":
		// A version names a release, downloaded and checked as update does.
		if inv.operand != "" {
			err = runUpdate(ctx, inv, layout, home, output, diagnostics)
			break
		}
		if err := firstInstallOnly(layout); err != nil {
			return err
		}
		// The running program installs itself, so the version recorded in
		// it and the bytes copied cannot disagree.
		var executable string
		if executable, err = os.Executable(); err != nil {
			return err
		}
		// An installed build should match the hardware, not the
		// translation layer it happens to run under.
		if detected, translated := host.Detect(); translated {
			if _, err := fmt.Fprintf(output, "note\tinstalling a %s build on %s, where it runs translated; build for %s\n", host.Build(), detected, detected); err != nil {
				return err
			}
		}
		err = layout.Install(executable, version.Current(), inv.dryRun, output)
		if err == nil && !onPath(layout.Bin) {
			_, err = fmt.Fprintf(output, notOnPathLine, layout.Bin, pathAdvice)
		}
	case "update":
		err = runUpdate(ctx, inv, layout, home, output, diagnostics)
	case "releases":
		err = runReleases(ctx, layout, output)
	case "use":
		err = layout.Use(inv.operand, inv.dryRun, output)
	case "remove":
		err = layout.Remove(inv.operand, inv.dryRun, output)
	}
	if err == nil && inv.dryRun {
		_, err = fmt.Fprintln(output, "dry run: nothing changed")
	}
	return err
}

// downloadedBuild reports whether the running program is a build Go made from
// a module it downloaded, as go run module@version does (see
// version.Info.Downloaded). Tests replace it.
var downloadedBuild = func() bool { return version.Current().Downloaded }

// firstInstallOnly refuses to install a build that Go made from a downloaded
// module once any version is installed in layout. Such a build is a way onto a
// new machine; after that, update and install VERSION install the binaries
// releases publish, so that each installed version is its release's build.
func firstInstallOnly(layout install.Layout) error {
	if !downloadedBuild() {
		return nil
	}
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	if len(state.Versions) > 0 {
		return errors.New("homeskillet is already installed here, and a build from go run or go install is for a first install only;\n" +
			"  homeskillet update installs the newest release, and homeskillet install VERSION a particular one; nothing has changed")
	}
	return nil
}

// selfLayout returns where the self commands keep installed versions: under
// the prefix, .local in the home directory unless --prefix says otherwise,
// and the data directory (see dataDir).
func selfLayout(inv invocation, home string) (install.Layout, error) {
	prefix := inv.prefix
	if prefix == "" {
		prefix = filepath.Join(home, ".local")
	}
	prefix, err := filepath.Abs(underHome(prefix, home))
	if err != nil {
		return install.Layout{}, err
	}
	inv.command = "self"
	data, err := dataDir(inv, home)
	if err != nil {
		return install.Layout{}, err
	}
	return install.New(filepath.Join(prefix, "bin"), data)
}

// listVersions prints one line per installed version: the version, its role
// (latest, previous, current), and the build details install recorded for it.
func listVersions(layout install.Layout, output io.Writer) error {
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	if len(state.Versions) == 0 {
		_, err := fmt.Fprintf(output, "no versions installed in %s\n", layout.Data)
		return err
	}
	// A pin to a version that is gone, as after rm -rf on its directory,
	// leaves the command dangling; self use repairs it. The link's target is
	// a file name like any other, so it is quoted if it could rewrite the
	// terminal.
	if state.Broken != "" {
		target := state.Broken
		if !config.Printable(target) {
			target = strconv.Quote(target)
		}
		if _, err := fmt.Fprintf(output, "current\tbroken: points to %s, which is not installed; run homeskillet self use latest\n", target); err != nil {
			return err
		}
	}
	for _, v := range state.Versions {
		var roles []string
		if v == state.Latest() {
			roles = append(roles, install.Latest)
		}
		if v == state.Previous() {
			roles = append(roles, install.Previous)
		}
		switch {
		case v == state.Current && state.Following:
			roles = append(roles, "current")
		case v == state.Current:
			roles = append(roles, "current (pinned)")
		}
		// The record is a file like any other, so it must not be able to
		// rewrite the terminal either.
		build := "build details unavailable"
		if info, err := layout.Build(v); err == nil && config.Printable(info.Summary()) {
			build = info.Summary()
		}
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\n", v, cmp.Or(strings.Join(roles, ", "), "-"), build); err != nil {
			return err
		}
	}
	return nil
}

// notOnPathLine is the note self install writes when the directory it
// installs into is not on PATH, with pathAdvice. wsl install rewrites it
// (see installNotes).
const notOnPathLine = "note\t%s is not on PATH; %s\n"

// onPath reports whether dir is on PATH. Entries are compared after
// resolving symlinks, so one that reaches dir through a symlink counts.
func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(entry); err == nil && trust.SamePath(resolved, dir) {
			return true
		}
	}
	return false
}
