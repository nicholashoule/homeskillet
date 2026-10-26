// Command githooks installs this repository's pre-commit hook, for make
// hooks. It copies tools/githooks/pre-commit into the hooks directory Git
// reports, which linked worktrees share.
//
// It never replaces a hook it did not install: one written by hand or by
// another tool would silently stop running its checks. Nor does it install
// where core.hooksPath points, since that directory may hold the hooks of
// every repository on the machine.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/safefile"
)

// marker identifies a hook this command installed, so that it may replace the
// hook later. It is a line of tools/githooks/pre-commit itself.
const marker = "# Installed by make hooks from tools/githooks/pre-commit."

// maxHook bounds the read of an existing hook, which may be anything.
const maxHook = 1 << 20

func main() {
	path, status, err := run(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "githooks: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pre-commit hook %s: %s\n", status, path)
}

// run installs the hook into the repository holding the current directory,
// and returns where it went and what install did.
func run(ctx context.Context) (string, string, error) {
	root, err := git(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	if path, _ := git(ctx, root, "config", "--get", "core.hooksPath"); path != "" {
		return "", "", fmt.Errorf("core.hooksPath is set to %s, which may serve other repositories; copy tools/githooks/pre-commit there yourself", path)
	}
	dir, err := git(ctx, root, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	hook, err := safefile.Read(filepath.Join(root, "tools", "githooks", "pre-commit"), maxHook)
	if err != nil {
		return "", "", err
	}
	status, err := install(hook, dir)
	return filepath.Join(dir, "pre-commit"), status, err
}

// install writes hook to dir/pre-commit, unless a hook this command did not
// install is already there. It reports "installed", "updated", or
// "unchanged".
//
// It looks at the entry itself before reading through it: reading follows a
// symlink, and a dangling one reads as absent, which would let the write
// replace it. This command only ever writes a regular file, so a symlink or
// anything else in its place is someone else's.
func install(hook []byte, dir string) (string, error) {
	if !bytes.Contains(hook, []byte(marker)) {
		return "", fmt.Errorf("the hook lacks the line %q, so it could never be updated", marker)
	}
	path := filepath.Join(dir, "pre-commit")
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", err
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file, so make hooks did not install it; move it aside, then run make hooks again", path)
	}
	existing, err := safefile.Read(path, maxHook)
	status := "installed"
	switch {
	case errors.Is(err, fs.ErrNotExist) && info == nil:
	case err != nil:
		return "", err
	case bytes.Equal(existing, hook) && (info.Mode().Perm() == 0o700 || runtime.GOOS == "windows"):
		// Windows has no Unix permission bits to restore.
		return "unchanged", nil
	case bytes.Equal(existing, hook):
		// The same hook, but Git runs only an executable one, so a copy made
		// by hand with the wrong mode is rewritten to restore it.
		status = "updated"
	case !bytes.Contains(existing, []byte(marker)):
		return "", fmt.Errorf("%s exists and was not installed by make hooks; move it aside, then run make hooks again", path)
	default:
		status = "updated"
	}
	err = safefile.Write(path, 0o700, func(w io.Writer) error {
		_, err := w.Write(hook)
		return err
	})
	if err != nil {
		return "", err
	}
	return status, nil
}

// git runs git with args in dir and returns its output without the trailing
// newline.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: every caller passes fixed arguments
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
