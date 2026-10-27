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
		_, err := fmt.Fprintf(output, "Dry run: nothing changed; purge would delete %s.\n", files(len(kept), "backup"))
		return err
	}
	if !inv.yes {
		approved, err := setup.Confirm(input, output, fmt.Sprintf("Delete %s? A deleted backup cannot be restored.", files(len(kept), "backup")))
		if err != nil || !approved {
			if err == nil {
				_, err = fmt.Fprintln(output, "Nothing deleted.")
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
		_, err = fmt.Fprintf(output, "Deleted %s.\n", files(len(kept), "backup"))
	}
	return err
}

// listBackups writes the backups: for a person, the data directory, each
// backup with when it was moved and its size, and a closing line unless a
// purge follows; for scripts, a line per backup: "backup", its path, the
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
	if _, err := fmt.Fprintf(output, "homeskillet backups\n  %-11s %s\n\n", "data:", v.path(data)); err != nil {
		return err
	}
	if len(kept) == 0 {
		_, err := fmt.Fprintln(output, "No backups.")
		return err
	}
	g := group{heading: fmt.Sprintf("Backups (%d): files link --force moved aside; move one back to restore it", len(kept))}
	for _, backup := range kept {
		name, err := filepath.Rel(data, backup.Path)
		if err != nil {
			name = backup.Path
		}
		g.entries = append(g.entries, [2]string{v.path(name), backup.Moved.Format("2006-01-02 15:04") + ", " + byteSize(backup.Size)})
	}
	if err := v.writeGroup(output, g); err != nil {
		return err
	}
	if purging {
		return nil
	}
	_, err := fmt.Fprintf(output, "%s; homeskillet backups purge deletes them after asking.\n", files(len(kept), "backup"))
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
