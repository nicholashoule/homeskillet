//go:build linux || darwin || windows

// The check command, which reads a repository as the other commands do and
// lists every issue they would refuse it for, or says that it is valid.

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
	"github.com/nicholashoule/homeskillet/internal/link"
	"github.com/nicholashoule/homeskillet/internal/setup"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// runCheck checks the repository for check and reports each issue, or that
// it is valid, exiting with an error when there are issues. It changes
// nothing and never looks at the home directory, so it suits a repository
// being edited and continuous integration alike; status says what linking
// would change in a home.
func runCheck(inv invocation, output io.Writer) error {
	repo, err := canonicalDirectory(inv.repo)
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckLocation(repo); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	manifest, issues := checkRepository(repo)
	v := newView(output)
	if v == nil {
		return checkLines(repo, issues, output)
	}
	return v.check(output, repo, manifest, issues)
}

// check writes check's result for a person: the repository and its
// manifests, then each issue and a closing line, or else what the valid
// repository holds. It returns a reported error when there are issues.
func (v *view) check(output io.Writer, repo string, manifest config.Manifest, issues []error) error {
	var found []string
	for _, name := range config.FileNames() {
		if _, err := os.Lstat(filepath.Join(repo, name)); err == nil {
			found = append(found, name)
		}
	}
	manifests := strings.Join(found, ", ")
	if manifests == "" {
		manifests = "none; files are found under " + config.HomeDir + "/"
	}
	text := fmt.Sprintf("homeskillet check\n  %-11s %s\n  %-11s %s\n\n", "repository:", v.path(repo), "manifests:", manifests)
	if len(issues) == 0 {
		platforms := "every platform"
		if len(manifest.Platforms) > 0 {
			platforms = strings.Join(manifest.Platforms, ", ")
		}
		var parts []string
		if n := len(manifest.Files); n > 0 {
			parts = append(parts, files(n, "file"))
		}
		if n := len(manifest.Setup); n > 0 {
			parts = append(parts, files(n, "setup step"))
		}
		contents := list(parts...)
		if contents == "" {
			contents = "nothing to link or set up yet"
		}
		_, err := fmt.Fprintf(output, "%sValid: %s, for %s.\n", text, contents, platforms)
		return err
	}
	if _, err := io.WriteString(output, text); err != nil {
		return err
	}
	g := group{heading: fmt.Sprintf("Issues (%d)", len(issues))}
	for _, issue := range issues {
		g.entries = append(g.entries, [2]string{oneLine(issue), ""})
	}
	if err := v.writeGroup(output, g); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "Not valid: link, status, and setup refuse this repository until these are fixed."); err != nil {
		return err
	}
	return reportedError{invalid(repo, issues)}
}

// checkRepository returns the repository's merged manifest and every issue
// that would make the other commands refuse it, for every platform rather
// than only this one: where it is and who may change it (see
// trust.CheckRepository), its manifests (see config.Load), each file they
// list or home/ holds (see link.CheckSource), and each setup step's inputs
// (see setup.CheckInput).
func checkRepository(repo string) (config.Manifest, []error) {
	var issues []error
	owned := trust.CheckRepository(repo)
	if owned != nil {
		issues = append(issues, owned)
	}
	manifest, err := config.Load(repo)
	issues = append(issues, config.Issues(err)...)
	// A repository others can change would fail the same check again for
	// every file in it, so its files are checked one by one only once it
	// passes.
	if err != nil || owned != nil {
		return manifest, issues
	}
	// Entries for different platforms may share a source; it is one issue.
	checked := make(map[string]bool)
	for _, entry := range manifest.Files {
		source := filepath.Join(repo, entry.Source)
		if checked[source] {
			continue
		}
		checked[source] = true
		if err := link.CheckSource(repo, source); errors.Is(err, fs.ErrNotExist) {
			issues = append(issues, fmt.Errorf("%s is listed, but the repository has no such file", displayPath(entry.Source)))
		} else if err != nil {
			issues = append(issues, err)
		}
	}
	// A step's input that cannot be read would stop setup before any step runs.
	for i := range manifest.Setup {
		step := &manifest.Setup[i]
		for _, input := range step.Inputs {
			if err := setup.CheckInput(repo, input); errors.Is(err, fs.ErrNotExist) {
				issues = append(issues, fmt.Errorf("setup step %s: input %s is listed, but the repository has no such file", step.Name, input))
			} else if err != nil {
				issues = append(issues, fmt.Errorf("setup step %s: %w", step.Name, err))
			}
		}
	}
	return manifest, issues
}

// checkLines writes check's result for scripts: a line per issue, "issue"
// and the issue, or else "valid" and the repository.
func checkLines(repo string, issues []error, output io.Writer) error {
	if len(issues) == 0 {
		_, err := fmt.Fprintf(output, "valid\t%s\n", repo)
		return err
	}
	var text strings.Builder
	for _, issue := range issues {
		text.WriteString("issue\t" + oneLine(issue) + "\n")
	}
	if _, err := io.WriteString(output, text.String()); err != nil {
		return err
	}
	return invalid(repo, issues)
}

// invalid is the error check exits with when the repository has issues.
func invalid(repo string, issues []error) error {
	return fmt.Errorf("%s is not valid: %s", displayPath(repo), files(len(issues), "issue"))
}

// oneLine returns err's text on one line, with any lines of advice after the
// first joined to it, so each issue stays one entry in the view and one line
// for scripts.
func oneLine(err error) string {
	lines := strings.Split(err.Error(), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " ")
}
