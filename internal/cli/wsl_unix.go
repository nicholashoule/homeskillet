//go:build linux || darwin

// The wsl command on Linux and macOS. Inside WSL, the Linux build hands it to
// the Windows build, which reaches into WSL from Windows; elsewhere there is
// no WSL to reach into.

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
	"strings"
	"syscall"

	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/wsl"
)

// wslpath is WSL's own tool for naming a path as Windows does, where WSL puts
// it in every distribution.
const wslpath = "/usr/bin/wslpath"

// runWSL hands wsl over to the Windows build inside WSL, which names the
// distribution in WSL_DISTRO_NAME, and refuses elsewhere.
func runWSL(ctx context.Context, inv invocation, input io.Reader, output, diagnostics io.Writer) error {
	distribution := os.Getenv("WSL_DISTRO_NAME")
	if distribution == "" {
		return errors.New("wsl runs on Windows, and in a WSL distribution, where it reaches into WSL")
	}
	if !wsl.ValidName(distribution) {
		return fmt.Errorf("WSL_DISTRO_NAME is %q, which names no distribution", distribution)
	}
	return handOff(ctx, inv, distribution, input, output, diagnostics)
}

// handOff runs wsl as homeskillet.exe wsl would, on this distribution, with
// the Windows build wsl install recorded here: that program only, never one
// found on PATH, where WSL adds Windows's directories in an order the user
// does not see. The record names a program to run, so the data directory
// holding it must pass the ownership rules. The program itself is on a
// Windows drive, where the distribution sees no owner or permissions to
// check; Windows's own access rules guard it, as when it runs from Windows.
func handOff(ctx context.Context, inv invocation, distribution string, input io.Reader, output, diagnostics io.Writer) error {
	home, err := distributionHome(inv)
	if err != nil {
		return err
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return err
	}
	if err := trust.CheckDirectory(data); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	build, err := readWindowsBuild(data)
	if err != nil {
		return err
	}
	args, err := handOffArgs(inv, distribution, func(p string) (string, error) { return windowsPath(ctx, p) })
	if err != nil {
		return err
	}
	if !inv.quiet {
		if _, err := fmt.Fprintf(diagnostics, "homeskillet: running the Windows build, %s\n", build.Windows); err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, build.Path, args...) //nolint:gosec // G204: the program wsl install recorded in the checked data directory, its arguments passed as they are
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostics
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		// It ran, and has said why it failed.
		return reportedError{err}
	case errors.Is(err, syscall.ENOEXEC):
		return fmt.Errorf("WSL does not run Windows programs here, as its interop with Windows is off; run homeskillet wsl %s from Windows", inv.sub)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the Windows build wsl install recorded, %s, is gone; run homeskillet wsl install from Windows again", build.Windows)
	}
	return err
}

// handOffArgs returns the arguments that run wsl as inv asks with the Windows
// build, on distribution unless --distribution names another. A repository
// or --from given here is a path here, so it goes in full: as Windows names
// it, by windows (see windowsPath), or for a repository in the
// distribution's own file system, as it is, a clone there.
func handOffArgs(inv invocation, distribution string, windows func(string) (string, error)) ([]string, error) {
	args := []string{"wsl", inv.sub, "--distribution=" + cmp.Or(inv.wsl.distribution, distribution)}
	if inv.wsl.from != "" {
		from, err := fullPath(inv.wsl.from)
		if err != nil {
			return nil, fmt.Errorf("--from: %w", err)
		}
		named, err := windows(from)
		if err != nil {
			return nil, fmt.Errorf("--from: %w", err)
		}
		if named == "" {
			return nil, fmt.Errorf("--from: %s is not on a Windows drive, where homeskillet.exe reads it", displayPath(from))
		}
		args = append(args, "--from="+named)
	}
	if inv.repoGiven {
		repo, err := fullPath(inv.repo)
		if err != nil {
			return nil, fmt.Errorf("repository: %w", err)
		}
		named, err := windows(repo)
		if err != nil {
			return nil, fmt.Errorf("repository: %w", err)
		}
		args = append(args, cmp.Or(named, repo))
	}
	return append(args, inv.wsl.forward...), nil
}

// fullPath returns p, a path given here, absolute, with a leading ~ as the
// home, as a shell would have expanded it.
func fullPath(p string) (string, error) {
	expanded, err := expandHome(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

// windowsPath returns how Windows names path, an absolute path here, when it
// is on a Windows drive, or "" when it is in the distribution's own file
// system. It asks WSL's own wslpath, since wsl.conf can move the drives.
func windowsPath(ctx context.Context, path string) (string, error) {
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, wslpath, "-w", path) //nolint:gosec // G204: WSL's own tool, where WSL puts it, given a path as one argument
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("%s: %s", displayPath(path), message)
		}
		return "", fmt.Errorf("%s: %w", displayPath(path), err)
	}
	named := strings.TrimSuffix(string(out), "\n")
	// Windows reaches the distribution's own files as \\wsl.localhost\...
	if strings.HasPrefix(named, `\\`) {
		return "", nil
	}
	return named, nil
}
