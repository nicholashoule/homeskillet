//go:build linux || darwin || windows

// Output for status, link, and unlink: the view a person at a terminal reads,
// with a header, entries grouped by what happens to them, and a closing line,
// or else the tab-separated lines scripts parse.

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/setup"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// terminal reports whether w is a terminal, where a person reads the output
// rather than a script parsing it. Scripts and pipes get the tab-separated
// lines, one per entry, which stay stable for them to parse.
func terminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && isTerminal(file)
}

// view writes results for a person at a terminal: what is running and where,
// entries grouped by what happens to them with problems first, and a closing
// line saying what changed or what to do next. A nil *view means the
// tab-separated lines instead.
type view struct {
	// shadows maps each target a shell would run instead of another command
	// to that command (see shadowed).
	shadows map[string]string
	// next, when set, follows the closing line of link and status with what
	// to run next (see nextSetup).
	next string
	// copyOf, when set, says in the header that the repository is a copy
	// homeskillet wsl keeps (see describeCopy).
	copyOf string
}

// newView returns a view for output, or nil when output is not a terminal.
func newView(output io.Writer) *view {
	if !terminal(output) {
		return nil
	}
	return &view{}
}

// path returns p for display. Paths are shown in full, never with ~ for the
// home directory, so the output names exactly which home, and which
// directory in it, homeskillet acted on, even when --home chose another. A
// path with control or formatting characters is quoted, so it cannot rewrite
// the terminal (see config.Printable).
func (v *view) path(p string) string {
	return displayPath(p)
}

// displayPath returns p for display, quoted if it holds control or formatting
// characters, for output outside the view too: a PATH entry or a file name is
// not homeskillet's to trust, wherever it is printed.
func displayPath(p string) string {
	if !config.Printable(p) {
		return strconv.Quote(p)
	}
	return p
}

// header says what is about to run and where, before any result, so a person
// can see at once that the right repository and home were chosen. remembered
// marks a repository taken from the record because none was named.
func (v *view) header(w io.Writer, command, repo string, remembered bool, home string, platform config.Platform, dryRun bool) error {
	repository := v.path(repo)
	if remembered {
		repository += "  (linked into this home before; none was named)"
	}
	text := fmt.Sprintf("homeskillet %s\n  %-11s %s\n", command, "repository:", repository)
	if v.copyOf != "" {
		text += fmt.Sprintf("  %-11s %s\n", "copy of:", v.copyOf)
	}
	text += fmt.Sprintf("  %-11s %s\n  %-11s %s\n", "home:", v.path(home), "platform:", platform)
	if dryRun {
		text += fmt.Sprintf("  %-11s %s\n", "dry run:", "nothing will change")
	}
	_, err := fmt.Fprint(w, text+"\n")
	return err
}

// reportedError is an error a view has already explained on the output, so
// printing it again would only repeat the closing line.
type reportedError struct{ error }

func (e reportedError) Unwrap() error { return e.error }

// Reported reports whether err was already explained on the output, so the
// caller should only set the exit status. Scripts never get such an error:
// without a view, the error is their only explanation.
func Reported(err error) bool {
	var reported reportedError
	return errors.As(err, &reported)
}

// group is one heading and its entries, each a path and an optional note.
type group struct {
	heading string
	entries [][2]string
}

// links writes the results of status, link, or unlink. applied says whether
// the plans were carried out rather than only checked; force is link --force,
// and moved says where it moved each conflicting target (see link.Backup).
func (v *view) links(w io.Writer, command string, plans []link.Plan, applied, force bool, moved map[string]string) error {
	byState := map[link.State][]link.Plan{}
	for _, plan := range plans {
		byState[plan.State] = append(byState[plan.State], plan)
	}
	var groups []group
	// add appends a group titled with its count, then what it means:
	// "Conflicts (3): not homeskillet's links, ...".
	add := func(title string, plans []link.Plan, note func(link.Plan) string) {
		if len(plans) == 0 {
			return
		}
		heading, meaning, _ := strings.Cut(title, ": ")
		g := group{heading: fmt.Sprintf("%s (%d)", heading, len(plans))}
		if meaning != "" {
			g.heading += ": " + meaning
		}
		for _, plan := range plans {
			text := ""
			if note != nil {
				text = note(plan)
			}
			g.entries = append(g.entries, [2]string{v.path(plan.Target), text})
		}
		groups = append(groups, g)
	}
	// would picks the heading for changes made or, in a dry run or a
	// blocked run, changes that would be made.
	would := func(done, pending string) string {
		if applied {
			return done
		}
		return pending
	}
	switch command {
	case "unlink":
		add(would("Unlinked", "Would unlink"), append(byState[link.Linked], byState[link.Orphaned]...), nil)
		add(would("Skipped: not homeskillet's links", "Would skip: not homeskillet's links"), byState[link.Conflict], v.conflict)
	default:
		conflicts, describe := "Conflicts: not homeskillet's links, so never replaced without --force", v.conflict
		switch {
		case force && applied:
			conflicts = "Replaced: moved into the backup, then linked"
			describe = func(plan link.Plan) string { return "backup: " + v.path(moved[plan.Target]) }
		case force:
			conflicts = "Would replace: move into the backup, then link"
		}
		add(conflicts, byState[link.Conflict], describe)
		add("Absent sources: listed, but not in the repository", byState[link.NoSource], func(plan link.Plan) string {
			return "expected at " + v.path(plan.Source)
		})
		shadowing := slices.DeleteFunc(slices.Clone(plans), func(plan link.Plan) bool { return v.shadows[plan.Target] == "" })
		add("Shadowing commands: a shell runs these first; link them only from a repository you trust", shadowing, func(plan link.Plan) string {
			return "instead of " + v.path(v.shadows[plan.Target])
		})
		removed := would("Removed: no longer in the repository", "Would remove: no longer in the repository")
		if command == "status" {
			removed = "Orphaned: no longer in the repository; link or unlink removes them"
		}
		add(removed, byState[link.Orphaned], nil)
		pending := "Would link"
		if command == "status" {
			pending = "Not linked yet"
		}
		add(would("Linked", pending), byState[link.Missing], nil)
		// Links already in place are listed too, so every file in the
		// repository is accounted for, whatever the command did.
		already := "Already linked"
		if command == "status" {
			already = "Linked"
		}
		add(already, byState[link.Linked], nil)
	}
	for _, g := range groups {
		if err := v.writeGroup(w, g); err != nil {
			return err
		}
	}
	text := summary(command, byState, applied, force) + "\n"
	// Setup comes after linking; a blocked link has its own next step.
	blocked := len(byState[link.NoSource]) > 0 || len(byState[link.Conflict]) > 0 && !force
	if v.next != "" && !blocked {
		text += v.next + "\n"
	}
	_, err := io.WriteString(w, text)
	return err
}

// nextSetup returns the line that ends link and status at a terminal when the
// repository has setup steps pending for this platform, steps, so that a
// person who has just linked it learns that, for example, the step that makes
// their shell load what was linked has not run. It is "" when no step is
// pending, or when the steps cannot be checked, which setup itself reports.
// remembered marks a repository taken from the record, as setup takes it too.
// data is the data directory, with the record of steps with inputs.
func nextSetup(steps []config.Step, repo, home, data string, remembered bool) string {
	plans, err := setup.Prepare(steps, repo, home, data)
	if err != nil {
		return ""
	}
	pending := 0
	for i := range plans {
		if plans[i].State == setup.Pending {
			pending++
		}
	}
	if pending == 0 {
		return ""
	}
	// The repository can go unnamed when it is the current directory, or the
	// one in the record, which setup falls back to as link did.
	command := "homeskillet setup"
	if cwd, err := os.Getwd(); !remembered && (err != nil || !samePlace(cwd, repo)) {
		command += " " + setup.Quote([]string{repo})
	}
	return fmt.Sprintf("Next: %s offers %s, each shown before it runs.", command, files(pending, "pending setup step"))
}

// samePlace reports whether the directory dir, as given, is repo, which is
// canonical.
func samePlace(dir, repo string) bool {
	resolved, err := filepath.EvalSymlinks(dir)
	return err == nil && trust.SamePath(resolved, repo)
}

// steps writes a dry run's setup steps for a person: the done steps with what
// makes each done, the pending ones with their commands, and a closing line.
func (v *view) steps(w io.Writer, plans []setup.Plan, yes bool) error {
	var groups []group
	for _, state := range []setup.State{setup.Pending, setup.Done} {
		var entries [][2]string
		for i := range plans {
			if plans[i].State == state {
				entries = append(entries, [2]string{plans[i].Step.Name, setup.Detail(plans[i])})
			}
		}
		if len(entries) == 0 {
			continue
		}
		heading := fmt.Sprintf("Done (%d)", len(entries))
		switch {
		case state == setup.Pending && yes:
			heading = fmt.Sprintf("Pending (%d): shown in full before each runs; --yes asks nothing", len(entries))
		case state == setup.Pending:
			heading = fmt.Sprintf("Pending (%d): shown in full and asked about before each runs", len(entries))
		}
		groups = append(groups, group{heading: heading, entries: entries})
	}
	for _, g := range groups {
		if err := v.writeGroup(w, g); err != nil {
			return err
		}
	}
	pending := len(plans) - len(slices.DeleteFunc(slices.Clone(plans), func(p setup.Plan) bool { return p.State != setup.Done }))
	var closing string
	switch {
	case len(plans) == 0:
		closing = "No setup steps for this platform."
	case pending == 0:
		closing = "Nothing to do: " + files(len(plans), "step is", "steps are") + " done."
	default:
		closing = "Dry run: nothing changed; setup would offer " + files(pending, "step") + "."
	}
	_, err := fmt.Fprintln(w, closing)
	return err
}

// doneSteps writes the Done group alone, before a run offers the pending
// steps, and a closing line when nothing is pending.
func (v *view) doneSteps(w io.Writer, plans []setup.Plan) error {
	var entries [][2]string
	for i := range plans {
		if plans[i].State == setup.Done {
			entries = append(entries, [2]string{plans[i].Step.Name, setup.Detail(plans[i])})
		}
	}
	if len(entries) > 0 {
		if err := v.writeGroup(w, group{heading: fmt.Sprintf("Done (%d)", len(entries)), entries: entries}); err != nil {
			return err
		}
	}
	switch {
	case len(plans) == 0:
		_, err := fmt.Fprintln(w, "No setup steps for this platform.")
		return err
	case len(entries) == len(plans):
		_, err := fmt.Fprintln(w, "Nothing to do: "+files(len(plans), "step is", "steps are")+" done.")
		return err
	}
	return nil
}

// stepReporter returns a setup.Options.Report for a person at a terminal: a
// step that turned out done during the run, after an earlier step did its
// work, is said so, and each step that ran or was skipped gets a line. Steps
// done before the run were listed by doneSteps and are not repeated.
func (v *view) stepReporter(w io.Writer, plans []setup.Plan) func(outcome string, plan setup.Plan) error {
	listed := map[string]bool{}
	for i := range plans {
		if plans[i].State == setup.Done {
			listed[plans[i].Step.Name] = true
		}
	}
	return func(outcome string, plan setup.Plan) error {
		var err error
		switch {
		case outcome == string(setup.Done) && listed[plan.Step.Name]:
		case outcome == string(setup.Done):
			_, err = fmt.Fprintf(w, "done     %s  (%s, by an earlier step)\n", plan.Step.Name, setup.Detail(plan))
		default:
			_, err = fmt.Fprintf(w, "%-8s %s\n", outcome, plan.Step.Name)
		}
		return err
	}
}

// writeGroup writes a heading and its entries, aligning the notes.
func (v *view) writeGroup(w io.Writer, g group) error {
	width := 0
	for _, entry := range g.entries {
		width = max(width, len([]rune(entry[0])))
	}
	// Built whole, then written once, so the one write error is the only one.
	var text strings.Builder
	text.WriteString(g.heading + "\n")
	for _, entry := range g.entries {
		if entry[1] == "" {
			text.WriteString("  " + entry[0] + "\n")
		} else {
			fmt.Fprintf(&text, "  %-*s  %s\n", width, entry[0], entry[1])
		}
	}
	text.WriteString("\n")
	_, err := io.WriteString(w, text.String())
	return err
}

// conflict describes what occupies a conflicting target, so the user knows
// what is in the way before moving it aside.
func (v *view) conflict(plan link.Plan) string {
	info, err := os.Lstat(plan.Target)
	switch {
	case err != nil:
		return "unreadable"
	case info.Mode()&os.ModeSymlink != 0:
		destination, err := os.Readlink(plan.Target)
		if err != nil {
			return "a symlink"
		}
		return "a symlink to " + v.path(destination)
	case info.IsDir():
		return "a directory"
	case info.Mode().IsRegular():
		return "a file"
	default:
		return "a special file"
	}
}

// summary is the closing line: what changed, or would, or why nothing did
// and what to do. It always accounts for every file, so a run with nothing to
// show still says why.
func summary(command string, byState map[link.State][]link.Plan, applied, force bool) string {
	count := func(states ...link.State) int {
		n := 0
		for _, state := range states {
			n += len(byState[state])
		}
		return n
	}
	conflicts, absent, already := count(link.Conflict), count(link.NoSource), count(link.Linked)
	blocking, replaced := absent, 0
	if force {
		replaced = conflicts
	} else {
		blocking += conflicts
	}
	if command == "unlink" {
		unlinked := count(link.Linked, link.Orphaned)
		switch {
		case unlinked+conflicts == 0:
			return "Nothing to unlink."
		case applied:
			return "Done: " + list(counted(unlinked, "%d unlinked"), counted(conflicts, "%d skipped")) + "."
		default:
			return "Dry run: nothing changed; unlink would " + list(counted(unlinked, "remove %d"), counted(conflicts, "skip %d")) + "."
		}
	}
	if blocking > 0 {
		fix := "Fix the manifest or restore the absent sources, then run the command again."
		if absent == 0 {
			fix = "Move the conflicting files aside, or rerun with --force to move them into a backup and link."
		}
		if command == "link" {
			return "Nothing changed. " + fix
		}
		return "link would change nothing until these are resolved. " + fix
	}
	linked, removed := count(link.Missing), count(link.Orphaned)
	if linked+replaced+removed == 0 {
		switch {
		case already == 0:
			return "Nothing to link."
		case command == "status":
			return "Everything is linked: " + files(already, "file") + "."
		case applied:
			return "Nothing to change: " + files(already, "file is", "files are") + " already linked."
		default:
			return "Dry run: nothing would change; " + files(already, "file is", "files are") + " already linked."
		}
	}
	if applied {
		text := "Done: " + list(counted(linked, "%d linked"), counted(replaced, "%d replaced (originals in the backup)"),
			counted(removed, "%d removed"))
		if already > 0 {
			text += fmt.Sprintf("; %d already linked", already)
		}
		return text + "."
	}
	would := list(counted(linked, "link %d"), counted(replaced, "replace %d"), counted(removed, "remove %d"))
	if command == "status" {
		return "homeskillet link would " + would + "."
	}
	return "Dry run: nothing changed; link would " + would + "."
}

// counted formats n with format, or returns "" when n is zero, so list
// leaves it out.
func counted(n int, format string) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(format, n)
}

// list joins the items that are not empty as a phrase: "a", "a and b", or
// "a, b and c".
func list(items ...string) string {
	var kept []string
	for _, item := range items {
		if item != "" {
			kept = append(kept, item)
		}
	}
	if len(kept) < 2 {
		return strings.Join(kept, "")
	}
	return strings.Join(kept[:len(kept)-1], ", ") + " and " + kept[len(kept)-1]
}

// files formats n with the singular or plural noun, such as "1 file is" or
// "27 files are"; the plural is the singular plus "s" when not given.
func files(n int, singular string, plural ...string) string {
	if n == 1 {
		return "1 " + singular
	}
	if len(plural) > 0 {
		return fmt.Sprintf("%d %s", n, plural[0])
	}
	return fmt.Sprintf("%d %ss", n, singular)
}
