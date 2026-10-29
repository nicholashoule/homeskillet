//go:build linux || darwin || windows

// The note that a newer release is available, after the everyday commands at
// a terminal. It is meant not to get in the way: GitHub is asked at most once
// a day, the note is shown at most once a day and not at all when the newest
// release is installed already, the check runs alongside the command and adds
// at most a couple of seconds once a day when GitHub is slow, nothing is
// written unless homeskillet already keeps a data directory here, and
// HOMESKILLET_UPDATE_CHECK=off turns the whole thing off.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
)

const (
	// updateCheckVariable, set to off, 0, false, or no, turns the check and
	// the note off. homeskillet wsl sets it for the commands it runs in a
	// distribution (see wsl.UpdateCheckVariable, which must match).
	updateCheckVariable = "HOMESKILLET_UPDATE_CHECK"
	// updateFile, in the data directory, records when GitHub was last asked
	// and what it answered, and when the note was last shown.
	updateFile = "update.json"
	// updateCheckEvery is how often GitHub is asked, and how often the note
	// is shown while a newer release stays uninstalled.
	updateCheckEvery = 24 * time.Hour
	// updateCheckWait is the most the check adds, counted from when it
	// starts, which is when the command does: a command that takes longer
	// costs nothing extra. GitHub gets updateAskWait of it to answer; the
	// rest is for saving the answer, so it is not lost when the process
	// exits.
	updateCheckWait = 2 * time.Second
	updateAskWait   = 1500 * time.Millisecond
	// maxUpdateFile bounds the record, which is a few lines.
	maxUpdateFile = 4 << 10
)

// noticeCommands are the commands whose output the note may follow: the
// everyday ones, not version, help, completion, or the self commands.
var noticeCommands = []string{"status", "link", "unlink", "setup"}

// runningVersion returns the version of this build; tests replace it, since
// a test binary is not a release.
var runningVersion = func() string { return version.Current().Version }

// updateRecord is what updateFile holds.
type updateRecord struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest,omitempty"`
	Noticed time.Time `json:"noticed,omitzero"`
}

// updateCheck is a check started alongside a command (see startUpdateCheck).
type updateCheck struct {
	data     string
	now      time.Time
	deadline time.Time
	done     chan notice
}

// notice is what a check found: the line to show, or "", and the record as
// it stands, to which print adds when the line was shown.
type notice struct {
	line   string
	record updateRecord
}

// startUpdateCheck starts the check for inv when a person at the terminal
// output would see the note (see newUpdateCheck), or returns nil.
func startUpdateCheck(ctx context.Context, inv invocation, home string, output io.Writer) *updateCheck {
	return newUpdateCheck(ctx, inv, home, terminal(output), time.Now())
}

// newUpdateCheck starts the check at now, unless it has nothing to do: inv
// must be one of noticeCommands, not --quiet, not a dry run, which changes
// nothing, including the record, at a terminal, from a release build, which
// has releases to compare with, with the variable not set to off. The data
// directory must already exist, so status never creates one, and pass the
// ownership rules before its record is read, so another account cannot
// plant a note. It returns nil when there is nothing to do.
func newUpdateCheck(ctx context.Context, inv invocation, home string, atTerminal bool, now time.Time) *updateCheck {
	running := runningVersion()
	if !slices.Contains(noticeCommands, inv.command) || inv.quiet || inv.dryRun || !atTerminal || updateCheckOff() || !release(running) {
		return nil
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return nil
	}
	if info, err := os.Stat(data); err != nil || !info.IsDir() { //nolint:gosec // G703: the data directory, which dataDir checked with trust.CheckLocation
		return nil
	}
	if trust.CheckDirectory(data) != nil {
		return nil
	}
	layout, err := selfLayout(inv, home)
	if err != nil {
		return nil
	}
	check := &updateCheck{data: data, now: now, deadline: now.Add(updateCheckWait), done: make(chan notice, 1)}
	go func() { check.done <- prepareNotice(ctx, data, running, installedLister(layout), now) }()
	return check
}

// installedLister returns a function that lists the versions installed in
// layout, none when it cannot be read; it is called only when there is a
// newer release to compare with.
func installedLister(layout install.Layout) func() []string {
	return func() []string {
		state, err := layout.Inspect()
		if err != nil {
			return nil
		}
		return state.Versions
	}
}

// print writes the note, if the check has one, after the command's own
// output, and only then records that it was shown, so a note lost to a
// failed command or a slow check is shown next time.
func (c *updateCheck) print(output io.Writer) error {
	found, ok := c.result()
	if !ok || found.line == "" {
		return nil
	}
	if _, err := io.WriteString(output, found.line); err != nil {
		return err
	}
	found.record.Noticed = c.now
	// The record only spaces out checks and notes, so a failure to keep it
	// costs one more note, and is not worth a word after a command that
	// succeeded.
	_ = writeUpdateRecord(c.data, found.record)
	return nil
}

// wait lets the check finish after a command that failed, which shows no
// note, so that GitHub's answer is recorded for the next command rather than
// lost when the process exits.
func (c *updateCheck) wait() {
	_, _ = c.result()
}

// result returns what the check found, waiting until its deadline at most.
// A check still running then has already recorded its attempt (see
// prepareNotice), so the next command does not ask again; ok is false.
func (c *updateCheck) result() (found notice, ok bool) {
	if c == nil {
		return notice{}, false
	}
	// A result already there is taken even after the deadline, which a
	// select on both would leave to chance.
	select {
	case found = <-c.done:
		return found, true
	default:
	}
	timer := time.NewTimer(max(time.Until(c.deadline), 0))
	defer timer.Stop()
	select {
	case found = <-c.done:
		return found, true
	case <-timer.C:
		return notice{}, false
	}
}

// updateCheckOff reports whether updateCheckVariable turns the check off.
func updateCheckOff() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(updateCheckVariable))) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// due reports whether updateCheckEvery has passed since then. A time in the
// future counts as due, so a run with the clock set ahead, as on a machine
// that booted before its clock was set, cannot turn the check off until the
// clock catches up.
func due(now, then time.Time) bool {
	elapsed := now.Sub(then)
	return elapsed < 0 || elapsed >= updateCheckEvery
}

// prepareNotice returns the note for a command run at now by the release
// running, and the record. When a check is due, the attempt is recorded
// before GitHub is asked, so a check that fails or is abandoned counts all
// the same and a machine without a network asks again tomorrow, not on every
// command; GitHub's answer is recorded after. The note is the newest release
// when it is higher than the one running, is not installed already, as after
// pinning an older version on purpose, and has not been shown within
// updateCheckEvery.
func prepareNotice(ctx context.Context, data, running string, installed func() []string, now time.Time) notice {
	record := readUpdateRecord(data)
	if due(now, record.Checked) {
		record.Checked = now
		_ = writeUpdateRecord(data, record)
		if from, err := newDownloader(); err == nil {
			ctx, cancel := context.WithTimeout(ctx, updateAskWait)
			latest, err := latestVersion(ctx, from)
			cancel()
			if err == nil && latest != record.Latest {
				record.Latest = latest
				_ = writeUpdateRecord(data, record)
			}
		}
	}
	if record.Latest == "" || version.Compare(record.Latest, running) <= 0 || !due(now, record.Noticed) || slices.Contains(installed(), record.Latest) {
		return notice{record: record}
	}
	line := fmt.Sprintf("note\t%s is available; homeskillet update installs it (%s=off hides this)\n", record.Latest, updateCheckVariable)
	return notice{line: line, record: record}
}

// readUpdateRecord returns the record in data, or the zero record when there
// is none or it cannot be read, which means a check is due.
func readUpdateRecord(data string) updateRecord {
	var record updateRecord
	raw, err := safefile.Read(filepath.Join(data, updateFile), maxUpdateFile)
	if err != nil || json.Unmarshal(raw, &record) != nil {
		return updateRecord{}
	}
	return record
}

// writeUpdateRecord writes record to data, which must exist and pass the
// ownership rules like every directory homeskillet keeps files in.
func writeUpdateRecord(data string, record updateRecord) error {
	if err := trust.CheckDirectory(data); err != nil {
		return err
	}
	return safefile.Write(filepath.Join(data, updateFile), 0o600, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(record)
	})
}
