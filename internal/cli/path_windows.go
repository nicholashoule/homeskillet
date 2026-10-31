// What install does on Windows when the directory it put the command in is
// not on PATH: it adds the directory to the user's Path, which Windows keeps
// in the registry and gives every program started afterward, so homeskillet
// runs as a command however it was installed, and says how to use it in the
// terminal install ran in, whose environment was fixed when it started.

package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// onUserPath and appendUserPath read and append to the user's Path (see
// host.AppendUserPath, which only appends). Tests replace them, so that none
// changes the real Path.
var (
	onUserPath     = host.OnUserPath
	appendUserPath = host.AppendUserPath
)

// ownProfile reports whether home is the user's own profile, the only home
// whose commands belong on their Path. Tests replace it.
var ownProfile = func(home string) bool {
	profile, err := host.Profile.Path()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(profile); err == nil {
		profile = resolved
	}
	return trust.SamePath(profile, home)
}

// addToPath adds dir, where install put the command and which this process's
// PATH lacks, to the end of the user's Path, unless it is there already, and
// says so, with the line that adds it to this terminal's PATH as well. A dry
// run changes nothing and says the same.
//
// Every program in a directory on the Path runs as a command, so dir must
// first pass the rules for a directory homeskillet installs into (see
// checkPathDirectory), and the Path is only appended to (see
// host.AppendUserPath). A home that is not the user's profile, such as a
// scratch one given with --home, as tests and trial runs use, never touches
// the user's Path; it, a dir that fails the rules, and a Path that cannot be
// read or written get a note saying what to do instead.
func addToPath(dir, home string, dryRun bool, output io.Writer) error {
	if !ownProfile(home) {
		_, err := fmt.Fprintf(output, notOnPathLine, dir, pathAdvice(dir))
		return err
	}
	if err := checkPathDirectory(dir, dryRun); err != nil {
		_, err = fmt.Fprintf(output, "note\t%s is not on PATH, and homeskillet will not add it to your Path: %s\n", dir, oneLine(err))
		return err
	}
	there, err := onUserPath(dir)
	if err == nil && !there && !dryRun {
		var added bool
		added, err = appendUserPath(dir)
		there = err == nil && !added
	}
	switch {
	case err != nil:
		_, err = fmt.Fprintf(output, "note\t%s is not on PATH, and adding it to your Path failed: %s; %s\n", dir, oneLine(err), pathAdvice(dir))
	case there:
		_, err = fmt.Fprintf(output, "note\t%s is on your Path, but this terminal started before it was; open a new one%s\n", dir, orRun(dir))
	default:
		_, err = fmt.Fprintf(output, "note\tadded %s to your Path, for new terminals; open one%s\n", dir, orRun(dir))
	}
	return err
}

// checkPathDirectory applies to dir, before it goes on the user's Path, the
// rules for a directory homeskillet installs into: no system location, and
// owned by the user and changeable by nobody else, nor through any directory
// above it. Anyone who could add a program to a directory on the Path could
// have it run as a command, as the user. A dry run checks a directory install
// has not made yet as one it could make.
func checkPathDirectory(dir string, dryRun bool) error {
	if err := trust.CheckLocation(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); dryRun && errors.Is(err, fs.ErrNotExist) {
		return trust.CheckCreatable(dir)
	}
	return trust.CheckDirectory(dir)
}

// pathStep is prepare's next step while dir, where the command is, is not on
// this process's PATH: opening a new terminal when the user's Path has it
// already, and otherwise installing again, which adds it.
func pathStep(dir, home string) string {
	if ownProfile(home) {
		if there, err := onUserPath(dir); err == nil {
			if there {
				return displayPath(dir) + " is on your Path, but this terminal started before it was: open a new one" + orRun(dir) + "."
			}
			return displayPath(dir) + " is not on your Path; " + selfName() + " install adds it."
		}
	}
	return displayPath(dir) + " is not on PATH; " + pathAdvice(dir) + "."
}

// orRun returns ", or in this one run" and the PowerShell line that adds dir
// to this terminal's PATH, dir quoted as PowerShell's literal string, which
// expands nothing; or "" for a dir that is not safe to paste.
func orRun(dir string) string {
	if !config.Printable(dir) {
		return ""
	}
	return ", or in this one run $env:Path += '" + strings.ReplaceAll(";"+dir, "'", "''") + "'"
}
