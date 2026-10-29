//go:build linux || darwin || windows

// Tests of the note that a newer release is available: when it is shown,
// how often GitHub is asked, that a failed check is not repeated at once,
// and what turns it off.

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// countingDownloader serves a fixed newest release and counts the asks.
type countingDownloader struct {
	target string
	asked  *int
}

func (c countingDownloader) get(context.Context, string, string) error { return nil }

func (c countingDownloader) redirect(context.Context, string) (string, error) {
	*c.asked++
	return c.target, nil
}

func TestNoticeLine(t *testing.T) {
	data := t.TempDir()
	asked := 0
	useDownloader(t, countingDownloader{target: releaseTags + "v0.3.1", asked: &asked})
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	line := noticeLine(context.Background(), data, "v0.3.0", now)
	if !strings.Contains(line, "v0.3.1 is available") || !strings.Contains(line, "homeskillet update") || !strings.Contains(line, updateCheckVariable+"=off") {
		t.Errorf("first run: %q", line)
	}
	// The same day: neither asked again nor shown again.
	if line := noticeLine(context.Background(), data, "v0.3.0", now.Add(time.Hour)); line != "" || asked != 1 {
		t.Errorf("an hour later: %q, asked %d times", line, asked)
	}
	// The next day: asked again, and shown again since nothing changed.
	if line := noticeLine(context.Background(), data, "v0.3.0", now.Add(25*time.Hour)); line == "" || asked != 2 {
		t.Errorf("a day later: %q, asked %d times", line, asked)
	}
	// Once the newest release runs, there is nothing to say.
	if line := noticeLine(context.Background(), data, "v0.3.1", now.Add(50*time.Hour)); line != "" {
		t.Errorf("up to date: %q", line)
	}
	raw, err := os.ReadFile(filepath.Join(data, updateFile))
	if err != nil {
		t.Fatal(err)
	}
	var record updateRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.Latest != "v0.3.1" {
		t.Errorf("record %s, %v", raw, err)
	}

	// A check that fails counts as a check, so it is not repeated until
	// tomorrow, and an earlier answer is kept.
	failing := 0
	useDownloader(t, countingDownloader{target: "", asked: &failing})
	later := now.Add(80 * time.Hour)
	if line := noticeLine(context.Background(), data, "v0.3.0", later); line == "" || failing != 1 {
		t.Errorf("after a failed check, the earlier answer: %q, asked %d times", line, failing)
	}
	if line := noticeLine(context.Background(), data, "v0.3.0", later.Add(time.Hour)); line != "" || failing != 1 {
		t.Errorf("an hour after a failed check: %q, asked %d times", line, failing)
	}
}

func TestNoticeOff(t *testing.T) {
	for value, off := range map[string]bool{"off": true, "OFF": true, "0": true, "false": true, "no": true, "": false, "on": false, "1": false} {
		t.Setenv(updateCheckVariable, value)
		if got := updateCheckOff(); got != off {
			t.Errorf("%s=%q: off %v, want %v", updateCheckVariable, value, got, off)
		}
	}
}

func TestNoticeNotInScripts(t *testing.T) {
	// invoke writes to a buffer, not a terminal, so the note is never shown
	// and nothing is asked or written.
	repo, home := fixture(t, ".bashrc")
	asked := 0
	useDownloader(t, countingDownloader{target: releaseTags + "v99.0.0", asked: &asked})
	output, err := invoke("status", repo, home)
	if err != nil || strings.Contains(output, "is available") || asked != 0 {
		t.Errorf("status in a script: %q, %v, asked %d times", output, err, asked)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "homeskillet", updateFile)); err == nil {
		t.Error("the record was written for a script")
	}
	if check := startUpdateCheck(context.Background(), invocation{command: "status", quiet: true}, home, os.Stdout); check != nil {
		t.Error("--quiet started a check")
	}
	if check := startUpdateCheck(context.Background(), invocation{command: "status", dryRun: true}, home, os.Stdout); check != nil {
		t.Error("a dry run started a check")
	}
	if check := startUpdateCheck(context.Background(), invocation{command: "version"}, home, os.Stdout); check != nil {
		t.Error("version started a check")
	}
	var nothing *updateCheck
	if err := nothing.print(os.Stdout); err != nil {
		t.Error(err)
	}
}
