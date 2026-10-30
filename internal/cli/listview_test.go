//go:build linux || darwin || windows

// Tests of how list and list-remote read at a terminal: grouped, newest
// first, aligned however long a version is, and closing with which version
// runs or what the newest release is to this installation.

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/version"
)

func TestVersionsView(t *testing.T) {
	requireSymlinks(t)
	_, home := repository(t)
	layout, err := selfLayout(invocation{}, home)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(t.TempDir(), "homeskillet")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	built := "2026-10-29T21:17:37Z"
	for _, info := range []version.Info{
		{Version: "v0.3.1", Revision: "4a3fa8e67664dc3c3342933c2b274687907263da", Built: built, Go: "go1.27.3"},
		{Version: "v0.3.2-0.20261029145923-b78e58646040", Revision: "b78e58646040aaaa", Built: built, Modified: true, Go: "go1.26.10"},
		{Version: "v0.4.0", Revision: "a5277a2d388ad45465bf009c8fa82ffffdf7d6f3", Built: built, Go: "go1.27.3"},
	} {
		if err := layout.Install(program, info, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	local := func() string {
		parsed, err := time.Parse(time.RFC3339, built)
		if err != nil {
			t.Fatal(err)
		}
		return parsed.Local().Format("2006-01-02 15:04")
	}()
	view := func() string {
		t.Helper()
		state, err := layout.Inspect()
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := versionsView(layout, state, &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	want := "homeskillet list\n  versions in: " + layout.Data + "\n\n" +
		"Releases (2)\n" +
		"  v0.4.0                                latest, current  a5277a2           " + local + "  go1.27.3\n" +
		"  v0.3.1                                                 4a3fa8e           " + local + "  go1.27.3\n\n" +
		"Development builds (1)\n" +
		"  v0.3.2-0.20261029145923-b78e58646040  previous         b78e586 modified  " + local + "  go1.26.10\n\n" +
		"v0.4.0 runs, following latest.\n"
	if got := view(); got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
	if err := layout.Use("v0.3.1", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := view(); !strings.HasSuffix(got, "v0.3.1 runs, pinned; homeskillet use latest follows the newest again.\n") ||
		!strings.Contains(got, "  v0.3.1                                current (pinned)  4a3fa8e") {
		t.Errorf("pinned list:\n%s", got)
	}

	// Nothing installed.
	_, empty := repository(t)
	bare, err := selfLayout(invocation{}, empty)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := versionsView(bare, install.State{}, &output); err != nil || !strings.HasSuffix(output.String(), "\n\nNo versions installed.\n") {
		t.Errorf("empty list: %q, %v", output.String(), err)
	}
}

func TestReleasesView(t *testing.T) {
	rows := [][]string{{"v0.5.0-rc.1", "pre-release"}, {"v0.4.0", "newest, installed"}, {"v0.3.1", "installed, current"}, {"v0.2.0", ""}}
	var output bytes.Buffer
	state := install.State{Versions: []string{"v0.3.1", "v0.4.0"}, Current: "v0.3.1"}
	if err := releasesView(rows, "v0.4.0", state, &output); err != nil {
		t.Fatal(err)
	}
	want := "homeskillet list-remote\n  from: " + project + "/releases\n\nPublished (4)\n" +
		"  v0.5.0-rc.1  pre-release\n" +
		"  v0.4.0       newest, installed\n" +
		"  v0.3.1       installed, current\n" +
		"  v0.2.0\n\n" +
		"v0.4.0 is the newest, and installed; homeskillet use latest runs it.\n"
	if output.String() != want {
		t.Errorf("list-remote:\n%s\nwant:\n%s", output.String(), want)
	}
	for _, test := range []struct {
		newest string
		state  install.State
		want   string
	}{
		{"v0.4.0", install.State{Versions: []string{"v0.4.0"}, Current: "v0.4.0"}, "v0.4.0 is the newest, and it runs here.\n"},
		{"v0.4.0", install.State{}, "v0.4.0 is the newest; homeskillet update installs it.\n"},
		{"", install.State{}, "No release is published yet, other than pre-releases.\n"},
	} {
		output.Reset()
		if err := releasesView(nil, test.newest, test.state, &output); err != nil || !strings.HasSuffix(output.String(), test.want) {
			t.Errorf("newest %q: %q, %v", test.newest, output.String(), err)
		}
	}
}
