//go:build linux || darwin || windows

// Tests of the note that a newer release is available: when a check starts,
// how often GitHub is asked, that the note is recorded as shown only once it
// is printed, that a failed check is not repeated at once, that a clock set
// ahead cannot turn it off, that an installed release is not announced, and
// what turns it off.

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicholashoule/homeskillet/internal/wsl"
)

// countingDownloader serves a fixed newest release, or fails, and counts the
// asks.
type countingDownloader struct {
	target string
	fail   bool
	asked  *int
}

func (c countingDownloader) get(context.Context, string, string) error { return nil }

func (c countingDownloader) redirect(context.Context, string) (string, error) {
	*c.asked++
	if c.fail {
		return "", errors.New("no network")
	}
	return c.target, nil
}

// asRelease makes the running build report v for the test.
func asRelease(t *testing.T, v string) {
	t.Helper()
	saved := runningVersion
	runningVersion = func() string { return v }
	t.Cleanup(func() { runningVersion = saved })
}

// noticeData returns a home with an existing, private data directory.
func noticeData(t *testing.T) (home, data string) {
	t.Helper()
	_, home = repository(t)
	data = filepath.Join(home, ".local", "share", "homeskillet")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	return home, data
}

func none() []string { return nil }

func TestPrepareNotice(t *testing.T) {
	_, data := noticeData(t)
	asked := 0
	useDownloader(t, countingDownloader{target: releaseTags + "v0.3.1", asked: &asked})
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	found := prepareNotice(context.Background(), data, "v0.3.0", none, now)
	if !strings.Contains(found.line, "v0.3.1 is available") || !strings.Contains(found.line, updateCheckVariable+"=off") {
		t.Fatalf("first run: %q", found.line)
	}
	// Not printed, so not recorded as shown: the next command shows it.
	if record := readUpdateRecord(data); !record.Noticed.IsZero() || record.Latest != "v0.3.1" {
		t.Errorf("before printing, the record is %+v", record)
	}
	if found := prepareNotice(context.Background(), data, "v0.3.0", none, now.Add(time.Minute)); found.line == "" || asked != 1 {
		t.Errorf("a note never printed is shown next time: %q, asked %d times", found.line, asked)
	}

	// Printed, so recorded: not shown again the same day. The deadline has
	// passed, but a result that is there is still printed.
	check := &updateCheck{data: data, now: now, deadline: now, done: make(chan notice, 1)}
	check.done <- found
	var output bytes.Buffer
	if err := check.print(&output); err != nil || output.String() != found.line {
		t.Fatalf("print: %q, %v", output.String(), err)
	}
	if found := prepareNotice(context.Background(), data, "v0.3.0", none, now.Add(time.Hour)); found.line != "" || asked != 1 {
		t.Errorf("an hour after it was shown: %q, asked %d times", found.line, asked)
	}
	// The next day: asked again, and shown again since nothing changed.
	if found := prepareNotice(context.Background(), data, "v0.3.0", none, now.Add(25*time.Hour)); found.line == "" || asked != 2 {
		t.Errorf("a day later: %q, asked %d times", found.line, asked)
	}
	// Running the newest release, or having it installed, says nothing.
	if found := prepareNotice(context.Background(), data, "v0.3.1", none, now.Add(50*time.Hour)); found.line != "" {
		t.Errorf("up to date: %q", found.line)
	}
	installed := func() []string { return []string{"v0.3.0", "v0.3.1"} }
	if found := prepareNotice(context.Background(), data, "v0.3.0", installed, now.Add(75*time.Hour)); found.line != "" {
		t.Errorf("newest installed but pinned away from: %q", found.line)
	}
}

func TestPrintAbandons(t *testing.T) {
	// A check with no result by its deadline prints nothing and records
	// nothing more.
	_, data := noticeData(t)
	check := &updateCheck{data: data, now: time.Now(), deadline: time.Now().Add(10 * time.Millisecond), done: make(chan notice, 1)}
	var output bytes.Buffer
	if err := check.print(&output); err != nil || output.Len() != 0 {
		t.Errorf("an abandoned check printed %q, %v", output.String(), err)
	}
	if _, err := os.Stat(filepath.Join(data, updateFile)); err == nil {
		t.Error("an abandoned check wrote the record")
	}
	var nothing *updateCheck
	nothing.wait()
}

func TestWaitKeepsTheAnswer(t *testing.T) {
	// After a failed command, the check finishes and its answer is kept,
	// but the note is not recorded as shown, so the next command shows it.
	home, data := noticeData(t)
	asked := 0
	useDownloader(t, countingDownloader{target: releaseTags + "v0.3.1", asked: &asked})
	asRelease(t, "v0.3.0")
	now := time.Now()
	check := newUpdateCheck(context.Background(), invocation{command: "link"}, home, true, now)
	if check == nil {
		t.Fatal("no check")
	}
	check.wait()
	if record := readUpdateRecord(data); record.Latest != "v0.3.1" || !record.Noticed.IsZero() {
		t.Errorf("after wait, the record is %+v", record)
	}
	if found := prepareNotice(context.Background(), data, "v0.3.0", none, now.Add(time.Minute)); found.line == "" || asked != 1 {
		t.Errorf("the next command: %q, asked %d times", found.line, asked)
	}
}

func TestPrepareNoticeFailedCheck(t *testing.T) {
	_, data := noticeData(t)
	asked := 0
	useDownloader(t, countingDownloader{fail: true, asked: &asked})
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	// A failed check counts as a check, so it is not repeated until
	// tomorrow.
	for _, later := range []time.Duration{0, time.Minute, time.Hour} {
		if found := prepareNotice(context.Background(), data, "v0.3.0", none, now.Add(later)); found.line != "" {
			t.Errorf("a failed check gave %q", found.line)
		}
	}
	if asked != 1 {
		t.Errorf("asked %d times in an hour after a failure", asked)
	}
	if record := readUpdateRecord(data); !record.Checked.Equal(now) {
		t.Errorf("the attempt was not recorded: %+v", record)
	}
}

func TestPrepareNoticeClockAhead(t *testing.T) {
	_, data := noticeData(t)
	asked := 0
	useDownloader(t, countingDownloader{target: releaseTags + "v0.3.1", asked: &asked})
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	ahead := now.AddDate(1, 0, 0)
	if err := writeUpdateRecord(data, updateRecord{Checked: ahead, Latest: "v0.3.0", Noticed: ahead}); err != nil {
		t.Fatal(err)
	}
	// Times in the future count as due, so the clock being corrected does
	// not turn the check and the note off for a year.
	if found := prepareNotice(context.Background(), data, "v0.3.0", none, now); found.line == "" || asked != 1 {
		t.Errorf("after a clock set ahead: %q, asked %d times", found.line, asked)
	}
}

func TestNewUpdateCheck(t *testing.T) {
	home, data := noticeData(t)
	asked := 0
	useDownloader(t, countingDownloader{target: releaseTags + "v99.0.0", asked: &asked})
	now := time.Now()
	status := invocation{command: "status"}

	asRelease(t, "v0.3.0")
	if check := newUpdateCheck(context.Background(), status, home, true, now); check == nil {
		t.Fatal("no check at a terminal, for a release, with a data directory")
	} else if err := check.print(new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	for name, inv := range map[string]invocation{
		"a script": status, "--quiet": {command: "status", quiet: true}, "a dry run": {command: "status", dryRun: true},
		"version": {command: "version"}, "self": {command: "self", sub: "list"},
	} {
		if check := newUpdateCheck(context.Background(), inv, home, name != "a script", now); check != nil {
			t.Errorf("%s started a check", name)
		}
	}
	t.Setenv(updateCheckVariable, "off")
	if check := newUpdateCheck(context.Background(), status, home, true, now); check != nil {
		t.Error("the variable set to off started a check")
	}
	t.Setenv(updateCheckVariable, "")

	// No data directory: nothing is read, asked, or written.
	_, bare := repository(t)
	if check := newUpdateCheck(context.Background(), status, bare, true, now); check != nil {
		t.Error("a home without a data directory started a check")
	}
	// A development build has no releases to compare with.
	asRelease(t, "v0.3.1-0.20261028120000-abcdef123456")
	if check := newUpdateCheck(context.Background(), status, home, true, now); check != nil {
		t.Error("a development build started a check")
	}

	asRelease(t, "v0.3.0")
	requireUnix(t)
	// A data directory others can write is not read: its record could hold
	// any note.
	makeShared(t, data)
	if check := newUpdateCheck(context.Background(), status, home, true, now); check != nil {
		t.Error("a shared data directory started a check")
	}
}

func TestNoticeOff(t *testing.T) {
	for value, off := range map[string]bool{"off": true, "OFF": true, "0": true, "false": true, "no": true, "": false, "on": false, "1": false} {
		t.Setenv(updateCheckVariable, value)
		if got := updateCheckOff(); got != off {
			t.Errorf("%s=%q: off %v, want %v", updateCheckVariable, value, got, off)
		}
	}
	// homeskillet wsl turns the note off in the distribution by this name.
	if wsl.UpdateCheckVariable != updateCheckVariable {
		t.Errorf("wsl sets %s, but the note reads %s", wsl.UpdateCheckVariable, updateCheckVariable)
	}
}

func TestNoticeNotInScripts(t *testing.T) {
	// invoke writes to a buffer, not a terminal, so even a release build
	// shows no note, and asks and writes nothing.
	asRelease(t, "v0.3.0")
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
	var nothing *updateCheck
	if err := nothing.print(os.Stdout); err != nil {
		t.Error(err)
	}
}
