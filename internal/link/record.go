//go:build linux || darwin

package link

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// The record of links homeskillet made.
//
// An entry can disappear from a repository: a file deleted from home/, or an
// entry removed from a manifest. Without a record, homeskillet would no
// longer know that the link left in the home directory was its own, and the
// link would dangle, or keep pointing at a file nothing manages. So after
// each link or unlink, Save records which links each repository has made in
// each home directory, and Prepare reports each recorded link that the
// repository no longer declares as Orphaned.
//
// The record lives in homeskillet's data directory, never in a repository,
// which other machines share. The data directory can be anywhere, including
// one directory shared by several homes, such as the user's own and a scratch
// home given with --home, so links are recorded per home and then per
// repository, and one home's run never rewrites another's links.

// recordVersion is the record format's version. A record of another version
// is refused rather than misread.
const recordVersion = 1

// recordLimit bounds what loadRecord reads. A record lists one target and one
// source per link, so a megabyte holds thousands of links.
const recordLimit = 1 << 20

// RecordPath returns the record's path in data, homeskillet's data directory.
func RecordPath(data string) string {
	return filepath.Join(data, "links.json")
}

type record struct {
	Version int `json:"version"`
	// Homes maps each home directory's canonical path to the repositories
	// that made links there, and each repository's canonical path to its
	// links.
	Homes map[string]map[string][]recorded `json:"homes"`
}

type recorded struct {
	Target string `json:"target"` // relative to the home directory
	Source string `json:"source"` // absolute, inside the repository
}

// loadRecord reads the record in data, or returns an empty one if there is
// none. Every value is validated, because Remove deletes what the record
// names: a target must stay inside its home and a source inside its
// repository, and none may hold characters that could rewrite the terminal
// when printed.
func loadRecord(data string) (record, error) {
	empty := record{Version: recordVersion, Homes: map[string]map[string][]recorded{}}
	path := RecordPath(data)
	contents, err := safefile.Read(path, recordLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return record{}, err
	}
	var rec record
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rec); err != nil {
		return record{}, invalidRecord(path, err)
	}
	// Anything after the object means the file is not what Save wrote.
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record{}, invalidRecord(path, errors.New("more than one JSON value"))
	}
	if rec.Version != recordVersion {
		return record{}, invalidRecord(path, fmt.Errorf("version %d, not %d", rec.Version, recordVersion))
	}
	if rec.Homes == nil {
		rec.Homes = empty.Homes
	}
	for home, repos := range rec.Homes {
		if !cleanAbsolute(home) {
			return record{}, invalidRecord(path, errors.New("a home is not a clean, printable absolute path"))
		}
		for repo, links := range repos {
			if !cleanAbsolute(repo) {
				return record{}, invalidRecord(path, errors.New("a repository is not a clean, printable absolute path"))
			}
			for _, link := range links {
				if !filepath.IsLocal(link.Target) || !config.Printable(link.Target) ||
					!trust.Within(repo, link.Source) || link.Source == repo || !config.Printable(link.Source) {
					return record{}, invalidRecord(path, errors.New("a link's target leaves its home directory or its source leaves its repository"))
				}
			}
		}
	}
	return rec, nil
}

func cleanAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && config.Printable(path)
}

func invalidRecord(path string, err error) error {
	return fmt.Errorf("%s is invalid (%w); delete it, then run homeskillet link to record links again", path, err)
}

// Refresh returns plans with each target's state read again from the file
// system: Linked if it is now a symlink to its source, Missing if it is gone,
// Conflict if something else is there. An Orphaned plan whose link remains
// stays Orphaned. Callers pass it what they tried to apply, whether or not
// every change succeeded, so the record lists exactly the links that exist:
// a run that stopped partway still records the links it made.
func Refresh(plans []Plan) ([]Plan, error) {
	refreshed := slices.Clone(plans)
	for i, plan := range refreshed {
		if plan.State == NoSource {
			continue
		}
		state, err := targetState(plan.Source, plan.Target)
		if err != nil {
			return nil, err
		}
		if plan.State != Orphaned || state != Linked {
			refreshed[i].State = state
		}
	}
	return refreshed, nil
}

// Save records, in data, the links repo has in home: the Linked and Orphaned
// plans among plans (see Refresh). When scoped is false, plans cover every
// entry, and the record of repo in home is replaced. When scoped is true,
// plans cover only the files a command named, and recorded links to other
// targets are kept, so a partial run never forgets links it left alone.
// Other repositories and homes are always kept, even homes that do not exist
// now: one on an unmounted volume looks no different from one deleted, and
// forgetting its links would leave them unrecognized as homeskillet's. When
// nothing is left, the record file is removed.
func Save(repo, home, data string, plans []Plan, scoped bool) error {
	path := RecordPath(data)
	if err := trust.CheckCreatable(data); err != nil {
		return err
	}
	rec, err := loadRecord(data)
	if err != nil {
		return err
	}
	repos := rec.Homes[home]
	if repos == nil {
		repos = map[string][]recorded{}
	}
	var links []recorded
	if scoped {
		covered := map[string]bool{}
		for _, plan := range plans {
			covered[plan.Target] = true
		}
		for _, link := range repos[repo] {
			if !covered[filepath.Join(home, link.Target)] {
				links = append(links, link)
			}
		}
	}
	for _, plan := range plans {
		if plan.State != Linked && plan.State != Orphaned {
			continue
		}
		target, err := filepath.Rel(home, plan.Target)
		if err != nil {
			return err
		}
		links = append(links, recorded{Target: target, Source: plan.Source})
	}
	delete(repos, repo)
	if len(links) > 0 {
		repos[repo] = links
	}
	if len(repos) > 0 {
		rec.Homes[home] = repos
	} else {
		delete(rec.Homes, home)
	}
	if len(rec.Homes) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return safefile.Write(path, 0o600, func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rec)
	})
}

// Repositories returns, sorted, the repositories the record in data has links
// from in home. The data directory must pass the same location and ownership
// rules as for linking, since a repository named here may be acted on without
// the user naming it.
func Repositories(data, home string) ([]string, error) {
	if err := trust.CheckLocation(data); err != nil {
		return nil, err
	}
	if err := trust.CheckCreatable(data); err != nil {
		return nil, err
	}
	rec, err := loadRecord(data)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(rec.Homes[home])), nil
}

// orphans returns an Orphaned plan for each link the record in data says repo
// made in home whose target is not among declared, the targets of the
// entries repo declares now, and that is still that same link: a symlink
// naming the recorded source. Anything else now at a recorded target,
// including nothing or a file the user put there, is no longer homeskillet's
// and is left out. So is a target whose parent directories have changed into
// symlinks, which homeskillet never reaches through.
func orphans(repo, home, data string, declared []string) ([]Plan, error) {
	rec, err := loadRecord(data)
	if err != nil {
		return nil, err
	}
	isDeclared := make(map[string]bool, len(declared))
	for _, target := range declared {
		isDeclared[target] = true
	}
	var found []Plan
	for _, link := range rec.Homes[home][repo] {
		target := filepath.Join(home, link.Target)
		if isDeclared[target] || checkParents(home, target) != nil {
			continue
		}
		state, err := targetState(link.Source, target)
		if err != nil {
			return nil, err
		}
		if state == Linked {
			found = append(found, Plan{Source: link.Source, Target: target, State: Orphaned})
		}
	}
	return found, nil
}
