//go:build linux || darwin || windows

// Tests of the backups command: listing what link --force moved aside, and a
// purge that deletes nothing unless approved.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nicholashoule/homeskillet/internal/link"
)

// runBackupsCommand runs backups with args for home, answering prompts with
// input, and returns what it printed.
func runBackupsCommand(home, input string, args ...string) (string, error) {
	var output bytes.Buffer
	args = append(append([]string{"backups"}, args...), "--home", home)
	err := Run(context.Background(), args, strings.NewReader(input), &output, &output)
	return output.String(), err
}

func TestBackups(t *testing.T) {
	requireSymlinks(t)
	repo, home := repository(t, "home/.bashrc")
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("link", repo, home, "--force"); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, ".local", "share", "homeskillet")
	count := func() int {
		t.Helper()
		kept, err := link.Backups(data)
		if err != nil {
			t.Fatal(err)
		}
		return len(kept)
	}

	output, err := runBackupsCommand(home, "")
	if err != nil || !strings.HasPrefix(output, "backup\t"+filepath.Join(data, link.BackupDir, ".bashrc.")) ||
		!strings.Contains(output, "\t.bashrc\t") || !strings.HasSuffix(output, "\t5\n") {
		t.Fatalf("list: %q, %v", output, err)
	}
	for name, test := range map[string]struct {
		input string
		args  []string
		want  string
	}{
		"dry run":        {"y\n", []string{"purge", "--dry-run"}, "purge would delete 1 backup"},
		"declined":       {"n\n", []string{"purge"}, "Nothing deleted."},
		"end of input":   {"", []string{"purge"}, "Nothing deleted."},
		"too young":      {"y\n", []string{"purge", "--older-than", "30d"}, ""},
		"other question": {"yes please\n", []string{"purge"}, "Nothing deleted."},
	} {
		output, err := runBackupsCommand(home, test.input, test.args...)
		if err != nil || !strings.Contains(output, test.want) || count() != 1 {
			t.Fatalf("%s: %q, %v; %d backups left", name, output, err, count())
		}
	}
	output, err = runBackupsCommand(home, "y\n", "purge")
	if err != nil || !strings.Contains(output, "Delete 1 backup? A deleted backup cannot be restored. [y/N]") ||
		!strings.Contains(output, "deleted\t") || count() != 0 {
		t.Fatalf("approved: %q, %v; %d backups left", output, err, count())
	}
	// The linked file is untouched by a purge.
	if target, err := os.Readlink(filepath.Join(home, ".bashrc")); err != nil || target != filepath.Join(repo, "home", ".bashrc") {
		t.Fatalf("link after the purge: %q, %v", target, err)
	}

	// --yes deletes without asking.
	if err := os.Remove(filepath.Join(home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("link", repo, home, "--force"); err != nil {
		t.Fatal(err)
	}
	if output, err := runBackupsCommand(home, "", "purge", "--yes"); err != nil || strings.Contains(output, "[y/N]") || count() != 0 {
		t.Fatalf("--yes: %q, %v; %d backups left", output, err, count())
	}
}

func TestBackupsArguments(t *testing.T) {
	_, home := repository(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"backups", "bogus"}, "'backups bogus' is not a command; 'backups' takes list or purge"},
		{[]string{"backups", "purge", "extra"}, "unexpected arguments"},
		{[]string{"backups", "--older-than", "0d"}, "not a number of days"},
		{[]string{"backups", "--older-than", "soon"}, "not an age"},
		{[]string{"backups", "--older-than", "-5h"}, "more than nothing"},
		{[]string{"backups", "--force"}, "--force applies only to link"},
		{[]string{"status", "--older-than", "1d"}, "--older-than applies only to backups"},
	} {
		err := Run(context.Background(), append(test.args, "--home", home), nil, new(bytes.Buffer), new(bytes.Buffer))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%q: got %v, want %q", test.args, err, test.want)
		}
	}
	// A home with no backups yet lists none, and a purge has nothing to ask.
	if output, err := runBackupsCommand(home, "y\n", "purge"); err != nil || output != "" {
		t.Fatalf("no backups: %q, %v", output, err)
	}
}

func TestBackupsView(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	kept := []link.Kept{{Path: filepath.Join(data, link.BackupDir, ".bashrc.1700000000"), Original: ".bashrc", Moved: time.Unix(1700000000, 0), Size: 2150}}
	var output bytes.Buffer
	if err := listBackups(&view{}, &output, data, kept, false); err != nil {
		t.Fatal(err)
	}
	// Each backup by its place in the home, with when it was moved and its
	// size; then where to find its file to restore it.
	want := "homeskillet backups\n  backups in: " + filepath.Join(data, link.BackupDir) + "\n\n" +
		"Backups (1)\n  .bashrc  " + time.Unix(1700000000, 0).Format("2006-01-02 15:04") + "  2.1 KiB\n\n" +
		"To restore one, move it back from that folder, where its name ends in the time it was moved, as in .bashrc.1700000000.\n" +
		"homeskillet backups purge deletes them after asking.\n"
	if output.String() != want {
		t.Errorf("view:\n%s\nwant:\n%s", output.String(), want)
	}
	// Newest first.
	output.Reset()
	two := append(slices.Clone(kept), link.Kept{Path: filepath.Join(data, link.BackupDir, ".vimrc.1700000500"), Original: ".vimrc", Moved: time.Unix(1700000500, 0), Size: 10})
	if err := listBackups(&view{}, &output, data, two, false); err != nil || strings.Index(output.String(), ".vimrc ") > strings.Index(output.String(), ".bashrc ") {
		t.Errorf("not newest first: %v\n%s", err, output.String())
	}
	output.Reset()
	if err := listBackups(&view{}, &output, data, nil, false); err != nil || !strings.Contains(output.String(), "No backups.") {
		t.Fatalf("no backups: %q, %v", output.String(), err)
	}
}

func TestParseAge(t *testing.T) {
	for value, want := range map[string]string{"": "0s", "30d": "720h0m0s", "2w": "336h0m0s", "12h": "12h0m0s", "90m": "1h30m0s"} {
		if age, err := parseAge(value); err != nil || age.String() != want {
			t.Errorf("parseAge(%q) = %v, %v; want %s", value, age, err, want)
		}
	}
	for _, size := range []struct {
		n    int64
		want string
	}{{311, "311 B"}, {2150, "2.1 KiB"}, {3 << 20, "3.0 MiB"}, {5 << 30, "5.0 GiB"}} {
		if got := byteSize(size.n); got != size.want {
			t.Errorf("byteSize(%d) = %q, want %q", size.n, got, size.want)
		}
	}
}
