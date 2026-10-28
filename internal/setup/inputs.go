//go:build linux || darwin || windows

// Steps with inputs: the fingerprint of a step's commands and input files,
// and the record of the fingerprint each such step last ran and succeeded
// with, setup.json in the data directory.

package setup

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// A step that installs from a list, such as packages to install, has no file
// whose presence shows it done, as creates needs: a marker it leaves would
// stay when the list grows, and the new package would never be installed. So
// a step with inputs is done only while the record says it last ran, and
// succeeded, with the same fingerprint as now: its commands and its inputs'
// contents. Changing either offers it again. Contents rather than times decide,
// so a fresh clone, or the copy homeskillet wsl keeps, counts as unchanged.
//
// The record lives in the data directory, never in a repository, which other
// machines share, and as for the record of links (see package link), by home
// and then by repository, since one data directory can serve several homes.
// It only ever keeps a step from being offered, never runs one, so the worst
// a damaged or forged record can do is skip a step; even so, it is read only
// from a data directory that passes the ownership rules.

// recordVersion is the record format's version. A record of another version
// is refused rather than misread.
const recordVersion = 1

// recordLimit bounds what loadRecord reads. An entry is one fingerprint and a
// time, so a megabyte holds thousands of steps.
const recordLimit = 1 << 20

// maxInput bounds each input a fingerprint reads. Inputs are lists and
// scripts, so a larger one is a mistake, such as a disk image listed by hand.
const maxInput = 64 << 20

// fingerprintSize is the length of a fingerprint in bytes, 128 bits of FNV-1a.
// A match only ever skips a step, and whoever could make one by changing an
// input could change what the step runs anyway, so a cryptographic hash would
// add size to homeskillet and no safety.
const fingerprintSize = 16

// RecordPath returns the record's path in data, homeskillet's data directory.
func RecordPath(data string) string {
	return filepath.Join(data, "setup.json")
}

type record struct {
	Version int `json:"version"`
	// Homes maps each home directory's canonical path to the repositories
	// whose steps ran there, and each repository's canonical path to its
	// steps with inputs, by name.
	Homes map[string]map[string]map[string]ran `json:"homes"`
}

// ran is what the record keeps of a step with inputs that ran and succeeded.
type ran struct {
	Fingerprint string    `json:"fingerprint"`
	Time        time.Time `json:"time"`
}

// last returns what the record says of the step called name in home from
// repo, and whether it says anything.
func (r record) last(home, repo, name string) (ran, bool) {
	step, ok := r.Homes[home][repo][name]
	return step, ok
}

// recordFor returns the record in data when needed, as it is when a step has
// inputs, and an empty one otherwise, so setup without such steps never
// reads or needs a data directory.
func recordFor(data string, needed bool) (record, error) {
	if !needed {
		return record{}, nil
	}
	if data == "" {
		return record{}, errors.New("steps with inputs need a data directory to record them in")
	}
	if err := trust.CheckCreatable(data); err != nil {
		return record{}, err
	}
	return loadRecord(data)
}

// loadRecord reads the record in data, or returns an empty one if there is
// none. Every value is validated, as every name in it is printed.
func loadRecord(data string) (record, error) {
	empty := record{Version: recordVersion, Homes: map[string]map[string]map[string]ran{}}
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
		for repo, steps := range repos {
			if !cleanAbsolute(repo) {
				return record{}, invalidRecord(path, errors.New("a repository is not a clean, printable absolute path"))
			}
			for name, step := range steps {
				if decoded, err := hex.DecodeString(step.Fingerprint); err != nil || len(decoded) != fingerprintSize || !config.Printable(name) || name == "" {
					return record{}, invalidRecord(path, errors.New("a step's name or fingerprint is malformed"))
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
	return fmt.Errorf("%s is invalid (%w); delete it, and setup offers every step with inputs again", printable(path), err)
}

// recordRan records in data that the step called name ran from repo in home,
// and succeeded, with fingerprint, and returns the record as it now is. It
// reads the record again first, so another homeskillet's entries are kept.
func recordRan(data, home, repo, name, fingerprint string) (record, error) {
	if err := trust.CheckCreatable(data); err != nil {
		return record{}, err
	}
	rec, err := loadRecord(data)
	if err != nil {
		return record{}, err
	}
	if rec.Homes[home] == nil {
		rec.Homes[home] = map[string]map[string]ran{}
	}
	if rec.Homes[home][repo] == nil {
		rec.Homes[home][repo] = map[string]ran{}
	}
	rec.Homes[home][repo][name] = ran{Fingerprint: fingerprint, Time: time.Now().UTC().Truncate(time.Second)}
	err = safefile.Write(RecordPath(data), 0o600, func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rec)
	})
	return rec, err
}

// fingerprint returns what decides whether a step with inputs changed since it
// last ran: a hash of its commands and of each input's name and contents. Each
// part goes in with its length first, so no two different steps hash the same.
func fingerprint(step config.Step, repo string) (string, error) {
	commands, err := json.Marshal(step.Run)
	if err != nil {
		return "", err
	}
	sum := fnv.New128a()
	part(sum, []byte("homeskillet setup 1"))
	part(sum, commands)
	for _, input := range step.Inputs {
		part(sum, []byte(input))
		if err := hashInput(sum, repo, input); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// part writes b to sum after its length.
func part(sum hash.Hash, b []byte) {
	_, _ = fmt.Fprintf(sum, "%d:", len(b)) // a hash.Hash never fails to write
	_, _ = sum.Write(b)
}

// hashInput writes the contents of input, a step's input in repo, to sum,
// after its length, as part does.
func hashInput(sum hash.Hash, repo, input string) (err error) {
	file, size, err := openInput(repo, input)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, _ = fmt.Fprintf(sum, "%d:", size) // a hash.Hash never fails to write
	// The size came from the open file, so a file that grows meanwhile is
	// read only that far, and one that shrinks fails.
	if _, err := io.CopyN(sum, file, size); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	return nil
}

// CheckInput returns why input, a path a step's inputs name, could not be read
// for the step's fingerprint, or nil if it could: it must be a regular file
// in repo, reached only through private directories and symlinks that stay
// inside repo (see trust.Resolve), and no larger than maxInput. A missing
// input gives an error matching fs.ErrNotExist. repo must be canonical.
func CheckInput(repo, input string) error {
	file, _, err := openInput(repo, input)
	if err != nil {
		return err
	}
	return file.Close()
}

// openInput opens input as CheckInput requires it, and returns its size.
func openInput(repo, input string) (*os.File, int64, error) {
	resolved, err := trust.Resolve(repo, filepath.Join(repo, filepath.FromSlash(input)))
	if err != nil {
		return nil, 0, fmt.Errorf("input %s: %w", input, err)
	}
	// Checked before opening, as opening a named pipe would wait for a writer.
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("not a regular file")
		}
		return nil, 0, fmt.Errorf("input %s: %w", input, err)
	}
	file, err := os.Open(resolved) //nolint:gosec // G304: resolved inside the repository by trust.Resolve
	if err != nil {
		return nil, 0, fmt.Errorf("input %s: %w", input, err)
	}
	info, err := file.Stat()
	switch {
	case err != nil:
	case !info.Mode().IsRegular():
		err = errors.New("not a regular file")
	case info.Size() > maxInput:
		err = fmt.Errorf("larger than %d MiB, more than a list or a script", maxInput>>20)
	}
	if err != nil {
		return nil, 0, errors.Join(fmt.Errorf("input %s: %w", input, err), file.Close())
	}
	return file, info.Size(), nil
}
