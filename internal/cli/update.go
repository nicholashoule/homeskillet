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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
)

// runUpdate installs the release inv.operand names, or the newest published
// one, as self install would install a build: the binary for this machine's
// platform and the release's SHA256SUMS are downloaded (see downloadRelease)
// into a private temporary directory, the binary is checked against the
// listed SHA-256 and to report that version, and then it installs itself
// with self install, with the same --home, --prefix, --data-dir, and
// --allow-root, so the version it records is its own. The version that was
// running stays installed, as previous unless an older one was, and self
// use previous or self use VERSION returns to it. A version already
// installed is left as it is: installed versions are never rewritten.
func runUpdate(ctx context.Context, inv invocation, layout install.Layout, home string, output, diagnostics io.Writer) error {
	from, err := newDownloader()
	if err != nil {
		return err
	}
	target := inv.operand
	if target == "" {
		if target, err = latestVersion(ctx, from); err != nil {
			return err
		}
	} else if !release(target) {
		return fmt.Errorf("%q is not a published version such as v0.2.0; self update installs releases only", target)
	}
	state, err := layout.Inspect()
	if err != nil {
		return err
	}
	if slices.Contains(state.Versions, target) {
		_, err := fmt.Fprintf(output, "unchanged\t%s\talready installed%s\n", target, chooseHint(state, target))
		return err
	}
	// The build for the hardware, not for the translation layer the
	// running build may be under (see host.Detect).
	platform, _ := host.Detect()
	name := releaseFile(platform.OS, platform.Arch)
	dir, err := os.MkdirTemp("", "homeskillet-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory; failing to remove it changes nothing
	sum, err := downloadRelease(ctx, from, target, name, dir)
	if err != nil {
		return err
	}
	file := filepath.Join(dir, name)
	if err := verifyDownload(ctx, file, sum); err != nil {
		return err
	}
	if err := os.Chmod(file, 0o700); err != nil { //nolint:gosec // G302: a program must be executable, and only by its owner
		return err
	}
	if _, err := fmt.Fprintf(output, "downloaded\t%s\t%s, checked against its SHA256SUMS\n", target, name); err != nil {
		return err
	}
	reported, err := exec.CommandContext(ctx, file, "version").Output() //nolint:gosec // G204: the release binary just downloaded and checked
	if err != nil {
		return fmt.Errorf("the downloaded %s does not run here: %w", name, err)
	}
	if have := versionOf(string(reported)); have != target {
		return fmt.Errorf("the downloaded %s reports version %s, not %s; nothing was installed", name, have, target)
	}
	cmd := exec.CommandContext(ctx, file, append([]string{"self", "install"}, selfOptions(inv, home)...)...) //nolint:gosec // G204: the release binary just downloaded and checked, installing itself
	cmd.Stdout = output
	cmd.Stderr = diagnostics
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// Its message is already on diagnostics.
			return fmt.Errorf("installing %s failed", target)
		}
		return err
	}
	if inv.dryRun {
		return nil
	}
	if state, err = layout.Inspect(); err == nil && state.Previous() != "" {
		_, err = fmt.Fprintf(output, "note\tto go back, run homeskillet self use %s\n", state.Previous())
	}
	return err
}

// chooseHint says how to run version v when it is installed but is not what
// the command runs, or "" when it is.
func chooseHint(state install.State, v string) string {
	if state.Current == v {
		return ""
	}
	return "; homeskillet self use " + v + " runs it"
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
	for i, r := range published {
		var roles []string
		if i == 0 {
			roles = append(roles, "newest")
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
