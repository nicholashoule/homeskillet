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
	"slices"
	"strconv"
	"strings"
	"time"

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
			err = addToPath(layout.Bin, home, inv.dryRun, output)
		}
	case "update":
		err = runUpdate(ctx, inv, layout, home, output, diagnostics)
	case "releases":
		err = runReleases(ctx, layout, output)
	case "use":
		if !terminal(output) {
			err = layout.Use(inv.operand, inv.dryRun, output)
			break
		}
		return useView(layout, inv.operand, inv.dryRun, output)
	case "remove":
		if !terminal(output) {
			err = layout.Remove(inv.names, inv.dryRun, output)
			break
		}
		return removeView(layout, inv.names, inv.dryRun, output)
	}
	// update and install with a version say a dry run changed nothing in
	// their own words for a person.
	said := terminal(output) && (inv.sub == "update" || inv.sub == "install" && inv.operand != "")
	if err == nil && inv.dryRun && !said {
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

// listVersions prints the installed versions: for a person at a terminal,
// grouped and aligned (see versionsView); for a script, one line per version,
// the version, its roles, and the build details install recorded for it.
func listVersions(layout install.Layout, output io.Writer) error {
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	if terminal(output) {
		return versionsView(layout, state, output)
	}
	if len(state.Versions) == 0 {
		_, err := fmt.Fprintf(output, "no versions installed in %s\n", layout.Data)
		return err
	}
	if state.Broken != "" {
		if _, err := fmt.Fprintf(output, "current\tbroken: points to %s, which is not installed; run homeskillet use latest\n", brokenTarget(state)); err != nil {
			return err
		}
	}
	for _, v := range state.Versions {
		// The record is a file like any other, so it must not be able to
		// rewrite the terminal either.
		build := "build details unavailable"
		if info, err := layout.Build(v); err == nil && config.Printable(info.Summary()) {
			build = info.Summary()
		}
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\n", v, cmp.Or(strings.Join(versionRoles(state, v), ", "), "-"), build); err != nil {
			return err
		}
	}
	return nil
}

// versionRoles returns what v is to the installation: latest, previous, and
// current, pinned or following latest.
func versionRoles(state install.State, v string) []string {
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
	return roles
}

// brokenTarget returns where a dangling current points. A pin to a version
// that is gone, as after rm -rf on its directory, leaves the command
// dangling; self use repairs it. The link's target is a file name like any
// other, so it is quoted if it could rewrite the terminal.
func brokenTarget(state install.State) string {
	if !config.Printable(state.Broken) {
		return strconv.Quote(state.Broken)
	}
	return state.Broken
}

// versionsView writes the installed versions for a person: newest first,
// releases apart from development builds, in columns that line up across
// both however long a pseudo-version is, then which version runs. Versions
// are shown whole, to be copied into use or self remove.
func versionsView(layout install.Layout, state install.State, w io.Writer) error {
	color := colorFor(w).on
	text := fmt.Sprintf("%s\n  versions in: %s\n\n", paint(color, styleHeading, "homeskillet list"), displayPath(layout.Data))
	if len(state.Versions) == 0 {
		_, err := io.WriteString(w, text+"No versions installed.\n")
		return err
	}
	if _, err := io.WriteString(w, text); err != nil {
		return err
	}
	var releases, builds [][]string
	for _, v := range slices.Backward(state.Versions) {
		row := append([]string{v, paintRoles(color, strings.Join(versionRoles(state, v), ", "))}, buildColumns(layout, v)...)
		if release(v) {
			releases = append(releases, row)
		} else {
			builds = append(builds, row)
		}
	}
	widths := columnWidths(releases, builds)
	for _, g := range []struct {
		heading string
		rows    [][]string
	}{{"Releases", releases}, {"Development builds", builds}} {
		if len(g.rows) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s\n", paint(color, styleHeading, fmt.Sprintf("%s (%d)", g.heading, len(g.rows)))); err != nil {
			return err
		}
		if err := writeTable(w, g.rows, widths); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	var closing string
	switch {
	case state.Broken != "":
		closing = fmt.Sprintf("%s %s, %s; homeskillet use latest repairs it.", paint(color, styleError, "The command points to"), brokenTarget(state),
			paint(color, styleError, "which is not installed"))
	case state.Current == "":
		closing = paint(color, styleAttention, "No version is chosen to run;") + " homeskillet use latest chooses the newest."
	case state.Following:
		closing = state.Current + " " + paint(color, styleGood, "runs") + ", following latest."
	default:
		closing = state.Current + " " + paint(color, styleGood, "runs") + ", pinned; homeskillet use latest follows the newest again."
	}
	_, err := fmt.Fprintln(w, closing)
	return err
}

// buildColumns returns the build details install recorded for v, for a
// person: the commit, abbreviated as Git does and marked when its tree was
// modified, the build time in local time, and the Go release.
func buildColumns(layout install.Layout, v string) []string {
	info, err := layout.Build(v)
	if err != nil || !config.Printable(info.Summary()) {
		return []string{"build details unavailable"}
	}
	commit := info.Revision
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if info.Modified {
		commit += " modified"
	}
	built := info.Built
	if t, err := time.Parse(time.RFC3339, info.Built); err == nil {
		built = t.Local().Format("2006-01-02 15:04")
	}
	return []string{commit, built, info.Go}
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

// useView chooses which version runs, as self use does, and says so for a
// person in one sentence, rather than the links it changed.
func useView(layout install.Layout, choice string, dryRun bool, w io.Writer) error {
	if err := layout.Use(choice, dryRun, io.Discard); err != nil {
		return err
	}
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	color := colorFor(w).on
	if dryRun {
		_, err := fmt.Fprintf(w, "%s nothing changed; homeskillet use %s would choose it.\n", dryRunLead(color), choice)
		return err
	}
	text := state.Current + " " + paint(color, styleGood, "runs now") + ", following latest."
	if !state.Following {
		text = state.Current + " " + paint(color, styleGood, "runs now") + ", pinned; homeskillet use latest follows the newest again."
	}
	_, err = fmt.Fprintln(w, text)
	return err
}

// removeView removes installed versions, as self remove does, and says so
// for a person in one sentence. What runs does not change, so it says only
// what was removed.
func removeView(layout install.Layout, versions []string, dryRun bool, w io.Writer) error {
	if err := layout.Remove(versions, dryRun, io.Discard); err != nil {
		return err
	}
	color := colorFor(w).on
	text := paint(color, styleGood, "Removed") + " " + joined(versions, "and") + "."
	if dryRun {
		text = dryRunLead(color) + " nothing changed; homeskillet remove would delete " + joined(versions, "and") + "."
	}
	_, err := fmt.Fprintln(w, text)
	return err
}
