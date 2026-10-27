//go:build linux || darwin

// The Linux and macOS layout: latest, previous, and current are symlinks, and
// the command links through current, so choosing a version never touches
// Bin.

package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// exe is the executable suffix, which Linux and macOS do not use.
const exe = ""

// commandTarget is what the homeskillet command in Bin links to. It goes
// through current, so choosing a version never touches Bin.
func (l Layout) commandTarget() string {
	return filepath.Join(l.Data, current, programFile)
}

// ownsCommand reports whether target, what the command in Bin links to, is
// homeskillet's link.
func (l Layout) ownsCommand(target string) bool {
	return target == l.commandTarget()
}

// commandTargets describes what ownsCommand accepts, for errors.
func (l Layout) commandTargets() string {
	return l.commandTarget()
}

// pointCommand creates the command in Bin when create is set. It needs no
// change otherwise, since it reaches the chosen version through current.
func (l Layout) pointCommand(_ string, create, dryRun bool, log io.Writer) error {
	if !create {
		return nil
	}
	if err := say(log, "linked\t%s -> %s\n", l.Command(), l.commandTarget()); err != nil || dryRun {
		return err
	}
	return l.createCommand(l.commandTarget())
}

// currentTarget returns what current names, latest or a version directory
// relative to Data, or "" when there is no current.
func (l Layout) currentTarget() (string, error) {
	link := filepath.Join(l.Data, current)
	target, err := os.Readlink(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("%s must be a symlink: %w", link, err)
	}
	return target, nil
}

// setCurrent points current at target, latest or a version directory
// relative to Data.
func (l Layout) setCurrent(target string) error {
	return replaceLink(target, filepath.Join(l.Data, current))
}

// linkChanges lists the changes that make latest and previous name the
// highest and second-highest of installed, which is sorted by order. An
// existing file at either path that is not a symlink is an error.
func (l Layout) linkChanges(installed []string) ([]linkChange, error) {
	state := State{Versions: installed}
	var changes []linkChange
	for name, v := range map[string]string{Latest: state.Latest(), Previous: state.Previous()} {
		change := linkChange{name: name, version: v, path: filepath.Join(l.Data, name)}
		if v != "" {
			change.target = filepath.Join(versions, v)
		}
		target, err := os.Readlink(change.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s must be a symlink: %w", change.path, err)
		}
		if target != change.target {
			changes = append(changes, change)
		}
	}
	// Map order is random; report latest before previous every time.
	slices.SortFunc(changes, func(a, b linkChange) int { return strings.Compare(a.name, b.name) })
	return changes, nil
}
