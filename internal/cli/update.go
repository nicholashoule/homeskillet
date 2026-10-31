//go:build linux || darwin || windows

// self update and self releases: installing a published release of
// homeskillet, fetched from the project's GitHub releases and checked against
// its SHA256SUMS, and listing the releases there are.

package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// runUpdate installs the release inv.operand names, or the newest published
// one, as self install would install a build; self install VERSION runs it
// too. Without a version, update also leaves the newest release running,
// following latest again when a version was pinned (see followLatest). A
// version that is not a release, such as v0.3.0-rc.1 or v0.3, is refused
// before anything else happens (see release). The release's SHA256SUMS and
// the binary for this machine's platform are downloaded (see
// downloadRelease) into a private directory under the data directory (see
// stagingDir), the binary is checked against the listed SHA-256, and, unless
// this is a dry run, checked to report that version and then run to install
// itself with self install, with the same --home, --prefix, --data-dir, and
// --allow-root, so the version it records is its own. The version that was
// running stays installed, and the closing note says how to run it again
// when what runs has changed. A version already installed is left as it is:
// installed versions are never rewritten.
//
// A dry run stops after the download and the check: it runs nothing it
// downloaded, so what it changes does not depend on the download honoring
// --dry-run.
func runUpdate(ctx context.Context, inv invocation, layout install.Layout, home string, output, diagnostics io.Writer) error {
	return update(ctx, inv, layout, home, output, diagnostics, terminal(output))
}

// update is runUpdate, saying what happened for a person at a terminal, in
// a sentence or two, or for a script, in tab-separated lines.
func update(ctx context.Context, inv invocation, layout install.Layout, home string, output, diagnostics io.Writer, person bool) error {
	target := inv.operand
	if target != "" && !release(target) {
		return fmt.Errorf("%q is not a published version such as v0.2.0; only releases are downloaded", target)
	}
	from, err := newDownloader()
	if err != nil {
		return err
	}
	if target == "" {
		if target, err = latestVersion(ctx, from); err != nil {
			return err
		}
	}
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	// update without a version leaves the newest release running: a pinned
	// version is let go, as homeskillet use latest would, since asking for
	// the newest is the point. install VERSION and update VERSION install
	// and leave what runs alone.
	follow := inv.operand == "" && inv.sub == "update" && state.Current != "" && !state.Following
	if slices.Contains(state.Versions, target) {
		if follow {
			return followLatest(layout, state.Current, inv.dryRun, output, person)
		}
		if person {
			_, err := fmt.Fprintln(output, upToDate(state, target, inv.operand == "", colorFor(output).on))
			return err
		}
		_, err := fmt.Fprintf(output, "unchanged\t%s\talready installed%s\n", target, chooseHint(state, target))
		return err
	}
	// The build for the hardware, not for the translation layer the
	// running build may be under (see host.Detect).
	platform, _ := host.Detect()
	name := releaseFile(platform.OS, platform.Arch)
	dir, err := stagingDir(layout, inv.dryRun)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory; failing to remove it changes nothing
	sum, err := downloadRelease(ctx, from, target, name, dir)
	var missing downloadError
	if errors.As(err, &missing) && missing.notFound && inv.operand != "" {
		return fmt.Errorf("%s is not published on GitHub\n  homeskillet list-remote lists the versions that are. Nothing was changed", target)
	}
	if err != nil {
		return err
	}
	file := filepath.Join(dir, name)
	if err := verifyDownload(ctx, file, sum); err != nil {
		return err
	}
	if err := reportDownload(output, target, name, state.Current, inv.dryRun, follow, person); err != nil || inv.dryRun {
		return err
	}
	// For a person, the install's notes, such as a directory to add to
	// PATH, come after what happened.
	var notes strings.Builder
	installOutput := output
	if person {
		installOutput = &notes
	}
	if err := installDownload(ctx, file, target, inv, home, follow || person, installOutput, diagnostics, person); err != nil {
		return err
	}
	if !person {
		if follow {
			return followLatest(layout, state.Current, false, output, person)
		}
		return goBackNote(layout, state.Current, output)
	}
	if _, err := fmt.Fprintf(output, "%s %s from GitHub, checked against its SHA256SUMS.\n", paint(colorFor(output).on, styleGood, "Installed"), target); err != nil {
		return err
	}
	if follow {
		err = followLatest(layout, state.Current, false, output, person)
	} else {
		err = runsNow(layout, state.Current, target, output)
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, personNotes(notes.String(), colorFor(output).on))
	return err
}

// personNotes returns the install's note lines, tab-separated, as sentences
// for a person, each after a Note: in the standard's color for what still
// needs doing.
func personNotes(lines string, color bool) string {
	var text strings.Builder
	for line := range strings.Lines(lines) {
		note := strings.TrimSuffix(strings.TrimPrefix(line, "note\t"), "\n")
		text.WriteString(paint(color, styleAttention, "Note:") + " " + strings.ReplaceAll(note, "\t", " ") + "\n")
	}
	return text.String()
}

// reportDownload says that target was downloaded and checked, for a script,
// and for a dry run what would follow: for a person in a sentence, which is
// all a dry run says to one, for a script in lines. pinned is the version
// that runs, which update without a version lets go of when follow is set.
func reportDownload(output io.Writer, target, name, pinned string, dryRun, follow, person bool) error {
	if person {
		if !dryRun {
			return nil
		}
		text := fmt.Sprintf("%s downloaded %s and checked it against its SHA256SUMS; nothing was installed.\n", dryRunLead(colorFor(output).on), target)
		if follow {
			text += fmt.Sprintf("It would then run in place of the pinned %s.\n", pinned)
		}
		_, err := io.WriteString(output, text)
		return err
	}
	if _, err := fmt.Fprintf(output, "downloaded\t%s\t%s, checked against its SHA256SUMS\n", target, name); err != nil || !dryRun {
		return err
	}
	if _, err := fmt.Fprintf(output, "install\t%s\twould be installed as a version of its own\n", target); err != nil || !follow {
		return err
	}
	_, err := fmt.Fprintf(output, "current\tlatest\twould be followed again instead of the pinned %s\n", pinned)
	return err
}

// upToDate says, for a person, that version v, already installed, needs no
// update: what runs, and if v is not it, how to run v. newest says v is the
// newest release, as update without a version finds it.
func upToDate(state install.State, v string, newest, color bool) string {
	switch {
	case state.Current == v && newest && state.Following:
		return paint(color, styleGood, "homeskillet is up to date:") + " " + v + " runs, following latest."
	case state.Current == v && newest:
		return paint(color, styleGood, "homeskillet is up to date:") + " " + v + " runs."
	case state.Current == v:
		return v + " " + paint(color, styleGood, "is installed, and it runs") + "."
	}
	return v + " " + paint(color, styleGood, "is already installed") + "; homeskillet use " + v + " runs it."
}

// runsNow says, for a person, after installing target, which version runs
// and, when that changed, how to go back to before, the version that ran.
func runsNow(layout install.Layout, before, target string, output io.Writer) error {
	state, err := layout.Inspect()
	if err != nil {
		_, err = fmt.Fprintf(output, "%s %v\n", paint(colorFor(output).on, styleError, "The installation could not be read back:"), err)
		return err
	}
	color := colorFor(output).on
	var text string
	switch {
	case state.Current == target && state.Following:
		text = target + " " + paint(color, styleGood, "runs now") + ", following latest."
	case state.Current == target:
		text = target + " " + paint(color, styleGood, "runs now") + "."
	case !state.Following && state.Current != "":
		text = state.Current + " " + paint(color, styleGood, "still runs") + ", pinned; homeskillet use " + target + " runs the new one."
	default:
		text = state.Current + " " + paint(color, styleGood, "still runs") + ", as the latest; homeskillet use " + target + " runs the new one."
	}
	if before != "" && state.Current != before {
		text += " To go back: homeskillet use " + before
	}
	_, err = fmt.Fprintln(output, text)
	return err
}

// installDownload runs the release binary file, just downloaded and checked,
// to install itself as version target: it must run here and report that
// version, and then its self install acts on the same installation as this
// command (see selfOptions). When update goes on to follow latest, the
// install's line about what still runs would be out of date before anyone
// read it, so it is left out, as wsl install leaves it out.
//
// For a person, only the install's notes are kept, such as a directory to
// add to PATH, written as sentences; update says the rest.
func installDownload(ctx context.Context, file, target string, inv invocation, home string, follow bool, output, diagnostics io.Writer, person bool) error {
	name := filepath.Base(file)
	if err := os.Chmod(file, 0o700); err != nil { //nolint:gosec // G302: a program must be executable, and only by its owner
		return err
	}
	reported, err := exec.CommandContext(ctx, file, "version").Output() //nolint:gosec // G702: the release binary just downloaded into the private staging directory and checked against SHA256SUMS
	if err != nil {
		return fmt.Errorf("the downloaded %s does not run here: %w", name, err)
	}
	if have := versionOf(string(reported)); have != target {
		return fmt.Errorf("the downloaded %s reports version %s, not %s; nothing was installed", name, have, target)
	}
	cmd := exec.CommandContext(ctx, file, append([]string{"self", "install"}, selfOptions(inv, home)...)...) //nolint:gosec // G204: the release binary just downloaded and checked, installing itself
	var installed strings.Builder
	cmd.Stdout = output
	if follow {
		cmd.Stdout = &installed
	}
	cmd.Stderr = diagnostics
	runErr := cmd.Run()
	for line := range strings.Lines(installed.String()) {
		text := strings.TrimSuffix(line, "\n")
		if install.AboutRunning(text) {
			continue
		}
		// A person gets only the notes, which update words for them once
		// it has said what happened (see personNotes).
		if person && !strings.HasPrefix(text, "note\t") {
			continue
		}
		if _, err := io.WriteString(output, line); err != nil {
			return err
		}
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			// Its message is already on diagnostics.
			return fmt.Errorf("installing %s failed", target)
		}
		return runErr
	}
	return nil
}

// followLatest makes the command follow latest again, as update does when a
// version was pinned, then says how to go back to before, the version that
// ran.
func followLatest(layout install.Layout, before string, dryRun bool, output io.Writer, person bool) error {
	if !person {
		if err := layout.Use(install.Latest, dryRun, output); err != nil || dryRun {
			return err
		}
		return goBackNote(layout, before, output)
	}
	if err := layout.Use(install.Latest, dryRun, io.Discard); err != nil {
		return err
	}
	if dryRun {
		_, err := fmt.Fprintf(output, "%s the newest would run in place of the pinned %s; nothing changed.\n", dryRunLead(colorFor(output).on), before)
		return err
	}
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	return runsNow(layout, before, state.Current, output)
}

// stagingDir returns a new private directory to download into: under the
// data directory, which passes the ownership rules and whose ancestors
// nobody else can rename, so the file checked is the file run, and which is
// on the filesystem versions are installed to, where /tmp mounted noexec is
// no obstacle. The data directory is created if needed, as self install
// would. A dry run runs nothing it downloads, so when the data directory
// does not exist it uses the system's temporary directory rather than
// create one.
func stagingDir(layout install.Layout, dryRun bool) (string, error) {
	if err := trust.CheckCreatable(layout.Data); err != nil {
		return "", err
	}
	if _, err := os.Lstat(layout.Data); dryRun && errors.Is(err, fs.ErrNotExist) {
		return os.MkdirTemp("", "homeskillet-update-")
	}
	if err := os.MkdirAll(layout.Data, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(layout.Data, ".update-")
}

// goBackNote writes, after an update, how to run the version that ran
// before it, before, when the update changed what runs; a pinned version
// keeps running, and so does a higher one, and then there is nothing to go
// back to. An installation that cannot be inspected afterward is reported as
// a note, since the install itself succeeded.
func goBackNote(layout install.Layout, before string, output io.Writer) error {
	state, err := layout.Inspect()
	switch {
	case err != nil:
		_, err = fmt.Fprintf(output, "note\tinstalled, but the installation could not be read back: %v\n", err)
	case before != "" && state.Current != before:
		_, err = fmt.Fprintf(output, "note\tto go back, run homeskillet use %s\n", before)
	}
	return err
}

// chooseHint says how to run version v when it is installed but is not what
// the command runs, or "" when it is.
func chooseHint(state install.State, v string) string {
	if state.Current == v {
		return ""
	}
	return "; homeskillet use " + v + " runs it"
}

// selfOptions returns the options to pass to the downloaded build's self
// install so that it acts on the same installation as this command: the
// home, resolved, and --prefix, --data-dir, --allow-root, --dry-run, and
// --quiet as given. Values are joined with = so a value starting with - is
// never read as an option.
func selfOptions(inv invocation, home string) []string {
	options := []string{"--home=" + home}
	if inv.prefix != "" {
		options = append(options, "--prefix="+inv.prefix)
	}
	if inv.data != "" {
		options = append(options, "--data-dir="+inv.data)
	}
	for _, flag := range []struct {
		set  bool
		name string
	}{{inv.root, "--allow-root"}, {inv.dryRun, "--dry-run"}, {inv.quiet, "--quiet"}} {
		if flag.set {
			options = append(options, flag.name)
		}
	}
	return options
}

// runReleases lists the published releases, newest first, and for each
// whether it is installed here and whether it is what the command runs, so
// that self update VERSION and self use VERSION can be chosen from it.
func runReleases(ctx context.Context, layout install.Layout, output io.Writer) error {
	from, err := newDownloader()
	if err != nil {
		return err
	}
	published, err := listReleases(ctx, from)
	if err != nil {
		return err
	}
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	if len(published) == 0 {
		_, err := fmt.Fprintln(output, "no releases published")
		return err
	}
	rows := make([][]string, 0, len(published))
	newest := ""
	for _, r := range published {
		var roles []string
		// The newest release is the first that GitHub does not flag as a
		// pre-release, which is the one the latest release page leads to,
		// and so the one update installs when no version is named.
		if newest == "" && !r.Prerelease {
			roles = append(roles, "newest")
			newest = r.Version
		}
		if r.Prerelease {
			roles = append(roles, "pre-release")
		}
		if slices.Contains(state.Versions, r.Version) {
			roles = append(roles, "installed")
		}
		if r.Version == state.Current {
			roles = append(roles, "current")
		}
		rows = append(rows, []string{r.Version, strings.Join(roles, ", ")})
	}
	if terminal(output) {
		return releasesView(rows, newest, state, output)
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(output, "%s\t%s\n", row[0], cmp.Or(row[1], "-")); err != nil {
			return err
		}
	}
	return nil
}

// releasesView writes the published releases for a person: aligned, newest
// first, then what the newest is to this installation.
func releasesView(rows [][]string, newest string, state install.State, w io.Writer) error {
	color := colorFor(w).on
	if _, err := fmt.Fprintf(w, "%s\n  from: %s\n\n%s\n", paint(color, styleHeading, "homeskillet list-remote"), project+"/releases",
		paint(color, styleHeading, fmt.Sprintf("Published (%d)", len(rows)))); err != nil {
		return err
	}
	painted := make([][]string, len(rows))
	for i, row := range rows {
		painted[i] = []string{row[0], paintRoles(color, row[1])}
	}
	if err := writeTable(w, painted, nil); err != nil {
		return err
	}
	var closing string
	switch {
	case newest == "":
		closing = "No release is published yet, other than pre-releases."
	case newest == state.Current:
		closing = newest + " is the newest, and " + paint(color, styleGood, "it runs here") + "."
	case slices.Contains(state.Versions, newest):
		closing = newest + " is the newest, and installed; " + paint(color, styleAttention, "homeskillet use latest runs it") + "."
	default:
		closing = newest + " is the newest; " + paint(color, styleAttention, "homeskillet update installs it") + "."
	}
	_, err := fmt.Fprintf(w, "\n%s\n", closing)
	return err
}
