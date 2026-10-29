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
			return followLatest(layout, state.Current, inv.dryRun, output)
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
	if _, err := fmt.Fprintf(output, "downloaded\t%s\t%s, checked against its SHA256SUMS\n", target, name); err != nil {
		return err
	}
	if inv.dryRun {
		if _, err := fmt.Fprintf(output, "install\t%s\twould be installed as a version of its own\n", target); err != nil || !follow {
			return err
		}
		_, err := fmt.Fprintf(output, "current\tlatest\twould be followed again instead of the pinned %s\n", state.Current)
		return err
	}
	if err := installDownload(ctx, file, target, inv, home, follow, output, diagnostics); err != nil {
		return err
	}
	if follow {
		return followLatest(layout, state.Current, false, output)
	}
	return goBackNote(layout, state.Current, output)
}

// installDownload runs the release binary file, just downloaded and checked,
// to install itself as version target: it must run here and report that
// version, and then its self install acts on the same installation as this
// command (see selfOptions). When update goes on to follow latest, the
// install's line about what still runs would be out of date before anyone
// read it, so it is left out, as wsl install leaves it out.
func installDownload(ctx context.Context, file, target string, inv invocation, home string, follow bool, output, diagnostics io.Writer) error {
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
		if !install.AboutRunning(strings.TrimSuffix(line, "\n")) {
			if _, err := io.WriteString(output, line); err != nil {
				return err
			}
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
func followLatest(layout install.Layout, before string, dryRun bool, output io.Writer) error {
	if err := layout.Use(install.Latest, dryRun, output); err != nil || dryRun {
		return err
	}
	return goBackNote(layout, before, output)
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
	newest := true
	for _, r := range published {
		var roles []string
		// The newest release is the first that GitHub does not flag as a
		// pre-release, which is the one the latest release page leads to,
		// and so the one update installs when no version is named.
		if newest && !r.Prerelease {
			roles = append(roles, "newest")
			newest = false
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
		if _, err := fmt.Fprintf(output, "%s\t%s\n", r.Version, cmp.Or(strings.Join(roles, ", "), "-")); err != nil {
			return err
		}
	}
	return nil
}
