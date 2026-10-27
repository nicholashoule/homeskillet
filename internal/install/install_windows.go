// The Windows layout. Windows refuses to rename a link over another that
// leads to a directory, so there are no latest and previous links, current is
// a small file, and the command links straight to the chosen version's
// program; choosing another version renames a new link over it.

package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
)

// exe is the executable suffix Windows requires to run a program.
const exe = ".exe"

// ownsCommand reports whether target, what the command in Bin links to, is
// homeskillet's link: the program of a version, installed or not, in Data.
func (l Layout) ownsCommand(target string) bool {
	relative, err := filepath.Rel(filepath.Join(l.Data, versions), target)
	if err != nil {
		return false
	}
	v, file, ok := strings.Cut(relative, string(filepath.Separator))
	return ok && version.Valid(v) && strings.EqualFold(file, programFile)
}

// commandTargets describes what ownsCommand accepts, for errors.
func (l Layout) commandTargets() string {
	return filepath.Join(l.Data, versions, "<version>", programFile)
}

// pointCommand makes the command in Bin link to chosen's program: it creates
// the link when create is set, and otherwise replaces homeskillet's link
// when it names another program. A command that is not homeskillet's, or
// that is missing, is left as it is, as on Linux and macOS.
func (l Layout) pointCommand(chosen string, create, dryRun bool, log io.Writer) error {
	target := l.Program(chosen)
	if !create {
		info, err := os.Lstat(l.Command())
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case info.Mode()&os.ModeSymlink == 0:
			return nil
		}
		existing, err := os.Readlink(l.Command())
		if err != nil {
			return err
		}
		if !l.ownsCommand(existing) || trust.SamePath(existing, target) {
			return nil
		}
	}
	if err := say(log, "linked\t%s -> %s\n", l.Command(), target); err != nil || dryRun {
		return err
	}
	if create {
		return l.createCommand(target)
	}
	// Renaming a new link over the old one replaces it in one step, so the
	// command always runs a complete version, and Windows allows it even
	// while the program the old link names is running.
	return replaceLink(target, l.Command())
}

// currentLimit bounds what currentTarget reads; current holds a few bytes.
const currentLimit = 4 << 10

// currentTarget returns what current names, latest or a version directory
// relative to Data, as on Linux and macOS, or "" when there is no current.
func (l Layout) currentTarget() (string, error) {
	path := filepath.Join(l.Data, current)
	data, err := safefile.Read(path, currentLimit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("%s must be a file naming latest or a version: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// setCurrent records target, latest or a version directory relative to
// Data, in current.
func (l Layout) setCurrent(target string) error {
	return safefile.Write(filepath.Join(l.Data, current), 0o600, func(w io.Writer) error {
		_, err := io.WriteString(w, target+"\n")
		return err
	})
}

// linkChanges returns nothing: there are no latest and previous links on
// Windows (see State.Latest and State.Previous).
func (l Layout) linkChanges([]string) ([]linkChange, error) {
	return nil, nil
}
