//go:build linux || darwin || windows

// The backups command: listing the files link --force moved into a backup,
// and purging them once you approve.

package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/setup"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// runBackups lists the backups in the data directory, oldest first, or for
// --older-than only those moved longer ago, and for backups purge deletes
// them. A purge is the one way homeskillet deletes files it did not create,
// so it shows every backup first and deletes nothing until the user answers
// y or yes, or --yes was given; a dry run only lists.
func runBackups(inv invocation, home string, input io.Reader, output io.Writer) error {
	age, err := parseAge(inv.olderThan)
	if err != nil {
		return fmt.Errorf("--older-than: %w", err)
	}
	data, err := dataDir(inv, home)
	if err != nil {
		return err
	}
	// The data directory decides what counts as a backup, so one another user
	// could change is refused, as for unlink.
	if err := trust.CheckCreatable(data); err != nil {
		return err
	}
	kept, err := link.Backups(data)
	if err != nil {
		return err
	}
	if age > 0 {
		cutoff := time.Now().Add(-age)
		kept = slices.DeleteFunc(kept, func(backup link.Kept) bool { return !backup.Moved.Before(cutoff) })
	}
	v := newView(output)
	if err := listBackups(v, output, data, kept, inv.sub == "purge"); err != nil {
		return err
	}
	if inv.sub != "purge" || len(kept) == 0 {
		return nil
	}
	if inv.dryRun {
		_, err := fmt.Fprintf(output, "%s nothing changed; purge would delete %s.\n", dryRunLead(colorFor(output).on), files(len(kept), "backup"))
		return err
	}
	if !inv.yes {
		approved, err := setup.Confirm(input, output, fmt.Sprintf("Delete %s? A deleted backup cannot be restored.", files(len(kept), "backup")))
		if err != nil || !approved {
			if err == nil {
				_, err = fmt.Fprintln(output, paint(colorFor(output).on, styleAttention, "Nothing deleted."))
			}
			return err
		}
	}
	log := output
	if v != nil {
		log = nil
	}
	if err := link.Purge(kept, data, log); err != nil {
		return err
	}
	if v != nil {
		_, err = fmt.Fprintf(output, "%s %s.\n", paint(colorFor(output).on, styleGood, "Deleted"), files(len(kept), "backup"))
	}
	return err
}

// listBackups writes the backups: for a person, where they are kept, each
// one's place in the home, newest first, with when it was moved and its
// size, and unless a purge follows, how to restore one; for scripts, a line per backup: "backup", its path, the
// path it came from in the home, the Unix time it was moved, and its size.
func listBackups(v *view, output io.Writer, data string, kept []link.Kept, purging bool) error {
	if v == nil {
		var text strings.Builder
		for _, backup := range kept {
			fmt.Fprintf(&text, "backup\t%s\t%s\t%d\t%d\n", backup.Path, backup.Original, backup.Moved.Unix(), backup.Size)
		}
		_, err := io.WriteString(output, text.String())
		return err
	}
	backups := filepath.Join(data, link.BackupDir)
	if _, err := fmt.Fprintf(output, "%s\n  backups in: %s\n\n", paint(v.color, styleHeading, "homeskillet backups"), v.path(backups)); err != nil {
		return err
	}
	if len(kept) == 0 {
		_, err := fmt.Fprintln(output, "No backups.")
		return err
	}
	// Each is named by its place in the home, which is what a person looks
	// for, newest first; the closing line says how to find its file.
	rows := make([][]string, 0, len(kept))
	for _, backup := range slices.Backward(kept) {
		rows = append(rows, []string{v.path(backup.Original), backup.Moved.Format("2006-01-02 15:04"), byteSize(backup.Size)})
	}
	if _, err := fmt.Fprintln(output, paint(v.color, styleHeading, fmt.Sprintf("Backups (%d)", len(kept)))); err != nil {
		return err
	}
	if err := writeTable(output, rows, nil); err != nil {
		return err
	}
	if purging {
		_, err := fmt.Fprintln(output)
		return err
	}
	example, err := filepath.Rel(backups, kept[len(kept)-1].Path)
	if err != nil {
		example = filepath.Base(kept[len(kept)-1].Path)
	}
	_, err = fmt.Fprintf(output, "\nTo restore one, move it back from that folder, where its name ends in the time it was moved, as in %s.\n"+
		"homeskillet backups purge deletes them after asking.\n", v.path(example))
	return err
}

// parseAge reads --older-than: a whole number of days, as 30d, weeks, as 2w,
// or anything time.ParseDuration reads, as 12h. Empty means no limit.
func parseAge(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[value[len(value)-1]]
	var age time.Duration
	if unit > 0 {
		n, err := strconv.Atoi(value[:len(value)-1])
		if err != nil || n <= 0 || n > 100*365 {
			return 0, fmt.Errorf("%q is not a number of days or weeks, such as 30d or 2w", value)
		}
		age = time.Duration(n) * unit
	} else {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("%q is not an age, such as 30d or 12h", value)
		}
		age = parsed
	}
	if age <= 0 {
		return 0, errors.New("the age must be more than nothing, such as 30d or 12h")
	}
	return age, nil
}

// byteSize formats n bytes for reading, as 311 B, 2.1 KiB, or 3.4 MiB.
func byteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	size, unit := float64(n)/1024, "KiB"
	for _, next := range []string{"MiB", "GiB"} {
		if size < 1024 {
			break
		}
		size, unit = size/1024, next
	}
	return fmt.Sprintf("%.1f %s", size, unit)
}
