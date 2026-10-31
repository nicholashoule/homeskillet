//go:build linux || darwin || windows

// The init command, which writes a starter bootstrap.json for a repository
// that wants platforms or setup steps. A repository with only a home/
// directory needs no manifest.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/setup"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// runInit writes a starter manifest into the repository, for init. A
// repository needs no manifest: files under home/ are found without one. A
// manifest adds what only it can say, a default platform and setup steps, so
// the starter holds those two: this machine's operating system, or
// --platform, and an empty list of steps. It leaves out "files", so files are
// still found under home/, which it creates when missing so the repository
// works at once.
//
// It writes nothing into a repository that has a manifest under either name
// (see config.FileNames), so it never replaces one or adds to one, refuses a
// directory that another user could write to, as the manifest decides what
// runs, and refuses the home directory itself, which is no place for a
// repository. A repository directory that does not exist yet is created, with
// any missing directories above it, as when someone with no dotfiles yet runs
// homeskillet init ~/dotfiles, but only inside a directory that passes the
// same checks (see trust.CheckCreatable). It shows what it would create and
// creates it only once approved, with no as the default answer, or under
// --yes; with --dry-run it only shows it.
func runInit(inv invocation, input io.Reader, output io.Writer) error {
	// --quiet hides the question init asks, which would then wait unseen.
	if inv.quiet && !inv.yes && !inv.dryRun {
		return errors.New("init --quiet needs --yes: init asks before it creates anything, and --quiet would hide the question; nothing was created")
	}
	repo, createRepo, err := initRepository(inv.repo)
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckLocation(repo); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	// Run from the home directory, init would turn it into a repository,
	// with a manifest and a home/ inside it; that is never what is meant.
	if home, err := os.UserHomeDir(); err == nil {
		if home, err := canonicalDirectory(home); err == nil && trust.SamePath(home, repo) {
			return fmt.Errorf("%s is your home directory, which is no place for a repository\n  name a directory for it, as in %s init ~/dotfiles; nothing was created", repo, selfName())
		}
	}
	path := filepath.Join(repo, config.FileName)
	exists := fmt.Errorf("%s already exists; init never replaces it", path)
	createHome, err := checkInitTarget(repo, createRepo)
	if err != nil {
		return err
	}
	platform, manifest, err := starterManifest(inv.platform)
	if err != nil {
		return err
	}
	home := filepath.Join(repo, config.HomeDir)
	dirs, err := missingDirectories(repo, createRepo)
	if err != nil {
		return err
	}

	person := terminal(output)
	plan := initPlan{repo: repo, home: home, manifest: path, platform: platform, dirs: dirs, createHome: createHome}
	if err := showInitPlan(output, person, plan, manifest); err != nil {
		return err
	}
	if inv.dryRun {
		text := "dry run: nothing changed\n"
		if person {
			text = dryRunLead(colorFor(output).on) + " nothing changed.\n"
		}
		_, err := io.WriteString(output, text)
		return err
	}
	if approved, err := approveInit(inv, input, output, person); err != nil || !approved {
		return err
	}
	// created lists the directories made here, outermost first, so that a
	// failure removes them again, innermost first; each was empty when made.
	created, err := makeRepository(repo, dirs)
	undo := func(err error) error {
		for _, dir := range slices.Backward(created) {
			if removeErr := os.Remove(dir); removeErr != nil {
				return errors.Join(err, removeErr)
			}
		}
		return err
	}
	if err != nil {
		return undo(err)
	}
	if createHome {
		// Private, as every directory homeskillet creates is.
		if err := os.Mkdir(home, 0o700); err != nil {
			return undo(err)
		}
		created = append(created, home)
	}
	if !person {
		for _, dir := range created {
			if _, err := fmt.Fprintf(output, "created\t%s\n", dir); err != nil {
				return err
			}
		}
	}
	err = safefile.Create(path, 0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, manifest)
		return err
	})
	if errors.Is(err, fs.ErrExist) {
		err = exists
	}
	if err != nil {
		// Leave everything as it was.
		return undo(err)
	}
	if person {
		return initCreated(output, repo, home, path)
	}
	_, err = fmt.Fprintf(output, "created\t%s\nnote\tfiles under %s/ are found without a list; add setup steps under \"setup\"\n", path, config.HomeDir)
	return err
}

// initCreated says, for a person, what init created and what to do next:
// where the dotfiles go, then the commands that preview and make the links,
// ready to paste.
func initCreated(output io.Writer, repo, home, path string) error {
	self, color := selfName(), colorFor(output).on
	_, err := fmt.Fprintf(output, "%s %s. Put your dotfiles under %s, placed as they are in your home, then preview the links with %s and make them with %s.\n",
		paint(color, styleGood, "Created"), displayPath(path), displayPath(home),
		paint(color, styleName, self+" link "+shellWord(repo)+" --dry-run"), paint(color, styleName, self+" link "+shellWord(repo)))
	return err
}

// approveInit asks whether to create what init showed, unless --yes says so.
// No typed at a terminal is an answer, and nothing more is said than that;
// a script that gave none, its input piped or empty even when its output is
// a terminal, gets an error, so it does not take nothing for success.
func approveInit(inv invocation, input io.Reader, output io.Writer, person bool) (bool, error) {
	if inv.yes {
		return true, nil
	}
	approved, err := setup.Confirm(input, output, "Create these?")
	if err != nil || approved {
		return approved, err
	}
	if in, ok := input.(*os.File); person && ok && isTerminal(in) {
		_, err := fmt.Fprintln(output, paint(colorFor(output).on, styleAttention, "Nothing created."))
		return false, err
	}
	return false, errors.New("nothing was created: init asks before it creates anything; pass --yes to create without asking")
}

// initPlan is what init would create: dirs, the repository directory and
// the directories above it that do not exist yet, outermost first, none when
// the repository exists; its home directory when that does not; and the
// manifest.
type initPlan struct {
	repo, home, manifest, platform string
	dirs                           []string
	createHome                     bool
}

// showInitPlan shows what init would create before it asks: for a person,
// each path and what it is for, and the starter manifest; for a script, a
// line per path and the manifest.
func showInitPlan(output io.Writer, person bool, plan initPlan, manifest string) error {
	if !person {
		var text strings.Builder
		for _, dir := range plan.dirs {
			fmt.Fprintf(&text, "would create\t%s\n", dir)
		}
		if plan.createHome {
			fmt.Fprintf(&text, "would create\t%s\n", plan.home)
		}
		_, err := fmt.Fprintf(output, "%swould write\t%s\n%s", text.String(), plan.manifest, manifest)
		return err
	}
	color := colorFor(output).on
	var rows [][]string
	// Every directory init would make is shown, so a mistyped path cannot
	// create a chain of them unseen.
	for _, dir := range plan.dirs {
		what := "a new directory, above the repository"
		if dir == plan.repo {
			what = "the repository, a new directory"
		}
		rows = append(rows, []string{displayPath(dir) + string(filepath.Separator), what})
	}
	if plan.createHome {
		rows = append(rows, []string{displayPath(plan.home) + string(filepath.Separator), "the directory your dotfiles are linked from"})
	}
	rows = append(rows, []string{displayPath(plan.manifest), "platforms: " + plan.platform + "; no setup steps yet"})
	if _, err := fmt.Fprintf(output, "%s\n  repository: %s\n\n%s\n", paint(color, styleHeading, "homeskillet init"), displayPath(plan.repo),
		paint(color, styleHeading+styleAttention, fmt.Sprintf("Creates (%d)", len(rows)))); err != nil {
		return err
	}
	if err := writeTable(output, rows, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintln(output)
	return err
}

// checkInitTarget checks, before init shows or creates anything, where it
// would create: for a repository still to be created, the directory it would
// be made in, since whatever init creates is only as safe as that; for one
// that exists, that it has no manifest yet and passes the ownership rules. It
// reports whether home/ is to be created.
func checkInitTarget(repo string, createRepo bool) (createHome bool, err error) {
	if createRepo {
		return true, trust.CheckCreatable(repo)
	}
	// Checked first for a clear error before anything is created; the write
	// itself refuses a manifest that appears after this (see safefile.Create).
	for _, name := range config.FileNames() {
		existing := filepath.Join(repo, name)
		if _, err := os.Lstat(existing); err == nil {
			return false, fmt.Errorf("%s already exists; init only starts a repository that has no manifest", existing)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	if err := trust.CheckRepository(repo); err != nil {
		return false, err
	}
	return homeToCreate(repo)
}

// starterManifest returns the platform init writes, this machine's operating
// system unless chosen, from --platform, names another, and the starter
// manifest that names it.
func starterManifest(chosen string) (platform, manifest string, err error) {
	detected, _ := host.Detect()
	platform = detected.OS
	if chosen != "" {
		parsed, err := config.ParsePlatform(chosen)
		if err != nil {
			return "", "", fmt.Errorf("--platform: %w", err)
		}
		platform = parsed.String()
	}
	quoted, err := json.Marshal(platform)
	if err != nil {
		return "", "", err
	}
	return platform, fmt.Sprintf("{\n  \"platforms\": [%s],\n  \"setup\": []\n}\n", quoted), nil
}

// initRepository returns the canonical path of the repository init was
// named, and whether it is still to be created. A path that does not exist
// is canonical once the symlinks in the part that does are resolved (see
// trust.Canonical); anything else that is not a directory, such as a file or
// a dangling symlink, is an error, as for every other command.
func initRepository(named string) (repo string, create bool, err error) {
	repo, err = canonicalDirectory(named)
	if !errors.Is(err, fs.ErrNotExist) {
		return repo, false, err
	}
	expanded, err := expandHome(named)
	if err != nil {
		return "", false, err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Lstat(abs); !errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("%s is not a directory", named)
	}
	repo, err = trust.Canonical(abs)
	return repo, true, err
}

// missingDirectories returns, when create says repo does not exist yet, repo
// and the directories above it that do not exist either, outermost first,
// which init would create.
func missingDirectories(repo string, create bool) ([]string, error) {
	if !create {
		return nil, nil
	}
	var missing []string
	for dir := repo; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(dir) == dir {
			return nil, err
		}
		missing = append(missing, dir)
	}
	slices.Reverse(missing)
	return missing, nil
}

// makeRepository creates dirs, the directories init showed it would create,
// outermost first, each private, and returns those it made. Each is made in
// the one before, so a directory removed meanwhile fails the next rather
// than being made again unseen. It then checks repo as every repository is
// checked, so a directory made where the checks fail is reported, for the
// caller to remove.
func makeRepository(repo string, dirs []string) ([]string, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	var created []string
	for _, dir := range dirs {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return created, err
		}
		created = append(created, dir)
	}
	return created, trust.CheckRepository(repo)
}

// homeToCreate reports whether repo has no home directory, which init then
// creates. An existing one must be what config.Load accepts: a directory, or
// a symlink to one inside the repository. Anything else, such as a file, a
// dangling symlink, or one leading out of the repository, is refused before
// init writes anything, rather than leaving a manifest Load rejects. The
// checks go through an os.Root, which resolves symlinks as Load does.
func homeToCreate(repo string) (bool, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return false, err
	}
	create, err := checkHome(root)
	return create, errors.Join(err, root.Close())
}

func checkHome(root *os.Root) (bool, error) {
	if _, err := root.Lstat(config.HomeDir); errors.Is(err, fs.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	info, err := root.Stat(config.HomeDir)
	if err != nil {
		return false, fmt.Errorf("%s in %s: %w", config.HomeDir, root.Name(), err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s in %s is not a directory", config.HomeDir, root.Name())
	}
	return false, nil
}
