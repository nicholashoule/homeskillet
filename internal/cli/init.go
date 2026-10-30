//go:build linux || darwin || windows

// The init command, which writes a starter bootstrap.json for a repository
// that wants platforms or setup steps. A repository with only a home/
// directory needs no manifest.

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
	"github.com/nicholashoule/homeskillet/internal/setup"
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
// It writes nothing into a repository that has a manifest under either name
// (see config.FileNames), so it never replaces one or adds to one, refuses a
// directory that another user could write to, as the manifest decides what
// runs, and refuses the home directory itself, which is no place for a
// repository. It shows what it would create and creates it only once
// approved, with no as the default answer, or under --yes; with --dry-run it
// only shows it.
func runInit(inv invocation, input io.Reader, output io.Writer) error {
	repo, err := canonicalDirectory(inv.repo)
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckLocation(repo); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	// Run from the home directory, init would turn it into a repository,
	// with a manifest and a home/ inside it; that is never what is meant.
	if home, err := os.UserHomeDir(); err == nil {
		if home, err := canonicalDirectory(home); err == nil && trust.SamePath(home, repo) {
			return fmt.Errorf("%s is your home directory, which is no place for a repository\n  name a directory for it, as in homeskillet init ~/dotfiles; nothing was created", repo)
		}
	}
	path := filepath.Join(repo, config.FileName)
	exists := fmt.Errorf("%s already exists; init never replaces it", path)
	// Checked first for a clear error before anything is created; the write
	// itself refuses a manifest that appears after this (see safefile.Create).
	for _, name := range config.FileNames() {
		existing := filepath.Join(repo, name)
		if _, err := os.Lstat(existing); err == nil {
			return fmt.Errorf("%s already exists; init only starts a repository that has no manifest", existing)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
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

	person := terminal(output)
	if err := showInitPlan(output, person, repo, home, path, platform, manifest, createHome); err != nil {
		return err
	}
	if inv.dryRun {
		text := "dry run: nothing changed\n"
		if person {
			text = dryRunLead(colorFor(output).on) + " nothing changed.\n"
		}
		_, err := io.WriteString(output, text)
		return err
	}
	if approved, err := approveInit(inv, input, output, person); err != nil || !approved {
		return err
	}
	if createHome {
		// Private, as every directory homeskillet creates is.
		if err := os.Mkdir(home, 0o700); err != nil {
			return err
		}
		if !person {
			if _, err := fmt.Fprintf(output, "created\t%s\n", home); err != nil {
				return err
			}
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
	if person {
		_, err = fmt.Fprintf(output, "%s %s. Put your dotfiles under %s, then run homeskillet link %s.\n",
			paint(colorFor(output).on, styleGood, "Created"), displayPath(path), displayPath(home), displayPath(repo))
		return err
	}
	_, err = fmt.Fprintf(output, "created\t%s\nnote\tfiles under %s/ are found without a list; add setup steps under \"setup\"\n", path, config.HomeDir)
	return err
}

// approveInit asks whether to create what init showed, unless --yes says so.
// At a terminal, no is an answer, and nothing more is said than that; a
// script that gave none gets an error, so it does not take nothing for
// success.
func approveInit(inv invocation, input io.Reader, output io.Writer, person bool) (bool, error) {
	if inv.yes {
		return true, nil
	}
	approved, err := setup.Confirm(input, output, "Create these?")
	if err != nil || approved {
		return approved, err
	}
	if person {
		_, err := fmt.Fprintln(output, paint(colorFor(output).on, styleAttention, "Nothing created."))
		return false, err
	}
	return false, errors.New("nothing was created: init asks before it creates anything; pass --yes to create without asking")
}

// showInitPlan shows what init would create before it asks: for a person,
// each path and what it is for, and the starter manifest; for a script, a
// line per path and the manifest.
func showInitPlan(output io.Writer, person bool, repo, home, path, platform, manifest string, createHome bool) error {
	if !person {
		text := ""
		if createHome {
			text += fmt.Sprintf("would create\t%s\n", home)
		}
		_, err := fmt.Fprintf(output, "%swould write\t%s\n%s", text, path, manifest)
		return err
	}
	color := colorFor(output).on
	var rows [][]string
	if createHome {
		rows = append(rows, []string{displayPath(home) + string(filepath.Separator), "the directory your dotfiles are linked from"})
	}
	rows = append(rows, []string{displayPath(path), "platforms: " + platform + "; no setup steps yet"})
	if _, err := fmt.Fprintf(output, "%s\n  repository: %s\n\n%s\n", paint(color, styleHeading, "homeskillet init"), displayPath(repo),
		paint(color, styleHeading+styleAttention, fmt.Sprintf("Creates (%d)", len(rows)))); err != nil {
		return err
	}
	if err := writeTable(output, rows, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintln(output)
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
