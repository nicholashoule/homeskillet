//go:build linux || darwin || windows

// The note that a newer release is available, after the everyday commands at
// a terminal. It is meant not to get in the way: GitHub is asked at most once
// a day, the note is shown at most once a day, the check runs alongside the
// command with a short time limit so it never slows or fails one, nothing is
// written unless homeskillet already keeps a data directory here, and
// HOMESKILLET_UPDATE_CHECK=off turns the whole thing off.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
)

const (
	// updateCheckVariable, set to off, 0, false, or no, turns the check and
	// the note off.
	updateCheckVariable = "HOMESKILLET_UPDATE_CHECK"
	// updateFile, in the data directory, records when GitHub was last asked
	// and what it answered, and when the note was last shown.
	updateFile = "update.json"
	// updateCheckEvery is how often GitHub is asked, and how often the note
	// is shown while a newer release stays uninstalled.
	updateCheckEvery = 24 * time.Hour
	// updateCheckWait is how long the check may take: it starts when the
	// command does and is collected when the command's output is done, so
	// it costs nothing unless the network is slow, and then at most this.
	updateCheckWait = 2 * time.Second
	// maxUpdateFile bounds the record, which is a few lines.
	maxUpdateFile = 4 << 10
)

// noticeCommands are the commands whose output the note may follow: the
// everyday ones, not version, help, completion, or the self commands.
var noticeCommands = []string{"status", "link", "unlink", "setup"}

// updateRecord is what updateFile holds.
type updateRecord struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest,omitempty"`
	Noticed time.Time `json:"noticed,omitzero"`
}

// updateCheck is a check started alongside a command (see startUpdateCheck).
type updateCheck struct {
	line chan string
}

// startUpdateCheck starts the check for inv when a person at a terminal
// would see the note: one of noticeCommands, not --quiet, not a dry run,
// which changes nothing, including the record, a release build, which has
// releases to compare with, the variable not set to off, and a data
// directory that already exists, so status never creates one. It returns
// nil when there is nothing to do.
func startUpdateCheck(ctx context.Context, inv invocation, home string, output io.Writer) *updateCheck {
	running := version.Current().Version
	if !slices.Contains(noticeCommands, inv.command) || inv.quiet || inv.dryRun || !terminal(output) || updateCheckOff() || !release(running) {
		return nil
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return nil
	}
	if info, err := os.Stat(data); err != nil || !info.IsDir() { //nolint:gosec // G703: the data directory, which dataDir checked with trust.CheckLocation
		return nil
	}
	check := &updateCheck{line: make(chan string, 1)}
	go func() { check.line <- noticeLine(ctx, data, running, time.Now()) }()
	return check
}

// print writes the note, if there is one, after the command's own output.
// A check that has not finished within updateCheckWait is abandoned; it
// recorded nothing, so the next command asks again.
func (c *updateCheck) print(output io.Writer) error {
	if c == nil {
		return nil
	}
	select {
	case line := <-c.line:
		_, err := io.WriteString(output, line)
		return err
	case <-time.After(updateCheckWait):
		return nil
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

// noticeLine returns the note to show after a command run at now by the
// release running, or "". GitHub is asked for the newest release when the
// record in data is older than updateCheckEvery, with updateCheckWait to
// answer; a failed or slow check is recorded as a check all the same, so a
// machine without a network asks again tomorrow, not on every command. The
// note is shown when the newest release is higher than the one running and
// has not been shown within updateCheckEvery.
func noticeLine(ctx context.Context, data, running string, now time.Time) string {
	record := readUpdateRecord(data)
	changed := false
	if now.Sub(record.Checked) >= updateCheckEvery {
		record.Checked = now
		changed = true
		if from, err := newDownloader(); err == nil {
			ctx, cancel := context.WithTimeout(ctx, updateCheckWait)
			defer cancel()
			if latest, err := latestVersion(ctx, from); err == nil {
				record.Latest = latest
			}
		}
	}
	line := ""
	if record.Latest != "" && version.Compare(record.Latest, running) > 0 && now.Sub(record.Noticed) >= updateCheckEvery {
		record.Noticed = now
		changed = true
		line = fmt.Sprintf("note\t%s is available; homeskillet update installs it (%s=off hides this)\n", record.Latest, updateCheckVariable)
	}
	if changed {
		// The record only spaces out checks and notes, so a failure to
		// keep it costs one more check tomorrow, and is not worth a word
		// after a command that succeeded.
		_ = writeUpdateRecord(data, record)
	}
	return line
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

// writeUpdateRecord writes record to data, which must pass the ownership
// rules like every file homeskillet keeps there.
func writeUpdateRecord(data string, record updateRecord) error {
	if err := trust.CheckCreatable(data); err != nil {
		return err
	}
	if _, err := os.Stat(data); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G703: the data directory, checked by trust.CheckCreatable just above
		return err
	}
	return safefile.Write(filepath.Join(data, updateFile), 0o600, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(record)
	})
}
