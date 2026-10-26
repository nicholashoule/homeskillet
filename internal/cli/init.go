//go:build linux || darwin

// The init command, which writes a starter dotfiles.json for a repository that
// wants platforms or setup steps. A repository with only a home/ directory
// needs no manifest.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// runInit writes a starter manifest into the repository, for init. A
// repository needs no manifest: files under home/ are found without one. A
// manifest adds what only it can say, a default platform and setup steps, so
// the starter holds those two: this machine's operating system, or
// --platform, and an empty list of steps. It leaves out "files", so files are
// still found under home/, which it creates when missing so the repository
// works at once.
//
// It never replaces a manifest, and refuses a directory that another user
// could write to, as the manifest decides what runs. With --dry-run it prints
// the manifest instead of writing it.
func runInit(inv invocation, output io.Writer) error {
	repo, err := canonicalDirectory(inv.repo)
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckLocation(repo); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	path := filepath.Join(repo, config.FileName)
	exists := fmt.Errorf("%s already exists; init never replaces it", path)
	// Checked first for a clear error before anything is created; the write
	// itself refuses a manifest that appears after this (see safefile.Create).
	if _, err := os.Lstat(path); err == nil {
		return exists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := trust.CheckRepository(repo); err != nil {
		return err
	}
	createHome, err := homeToCreate(repo)
	if err != nil {
		return err
	}
	detected, _ := host.Detect()
	platform := detected.OS
	if inv.platform != "" {
		chosen, err := config.ParsePlatform(inv.platform)
		if err != nil {
			return fmt.Errorf("--platform: %w", err)
		}
		platform = chosen.String()
	}
	quoted, err := json.Marshal(platform)
	if err != nil {
		return err
	}
	manifest := fmt.Sprintf("{\n  \"platforms\": [%s],\n  \"setup\": []\n}\n", quoted)
	home := filepath.Join(repo, config.HomeDir)

	if inv.dryRun {
		text := ""
		if createHome {
			text += fmt.Sprintf("would create\t%s\n", home)
		}
		_, err := fmt.Fprintf(output, "%swould write\t%s\n%sdry run: nothing changed\n", text, path, manifest)
		return err
	}
	if createHome {
		// Private, as every directory homeskillet creates is.
		if err := os.Mkdir(home, 0o700); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "created\t%s\n", home); err != nil {
			return err
		}
	}
	err = safefile.Create(path, 0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, manifest)
		return err
	})
	if errors.Is(err, fs.ErrExist) {
		err = exists
	}
	if err != nil {
		// Leave the repository as it was: home/ was empty when made here.
		if createHome {
			err = errors.Join(err, os.Remove(home))
		}
		return err
	}
	_, err = fmt.Fprintf(output, "created\t%s\nnote\tfiles under %s/ are found without a list; add setup steps under \"setup\"\n", path, config.HomeDir)
	return err
}

// homeToCreate reports whether repo has no home directory, which init then
// creates. An existing one must be what config.Load accepts: a directory, or
// a symlink to one inside the repository. Anything else, such as a file, a
// dangling symlink, or one leading out of the repository, is refused before
// init writes anything, rather than leaving a manifest Load rejects. The
// checks go through an os.Root, which resolves symlinks as Load does.
func homeToCreate(repo string) (bool, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return false, err
	}
	create, err := checkHome(root)
	return create, errors.Join(err, root.Close())
}

func checkHome(root *os.Root) (bool, error) {
	if _, err := root.Lstat(config.HomeDir); errors.Is(err, fs.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	info, err := root.Stat(config.HomeDir)
	if err != nil {
		return false, fmt.Errorf("%s in %s: %w", config.HomeDir, root.Name(), err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s in %s is not a directory", config.HomeDir, root.Name())
	}
	return false, nil
}
