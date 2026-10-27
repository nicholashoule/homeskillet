// Package config reads what a configuration repository declares: the files
// homeskillet links into a home directory and the setup steps it offers to
// run.
//
// A repository declares them in a manifest at its root, bootstrap.json,
// dotfiles.json, or both, merged, or relies on a convention: when no manifest
// lists "files", every file under the repository's home directory is linked
// to the same relative path in the user's home, so home/.bashrc becomes
// ~/.bashrc. Setup steps always come from a manifest, because commands are
// never inferred.
//
// Validation here is lexical: it looks only at the manifest's text and at file
// names, so it applies to every entry regardless of platform. Checks that
// depend on what is actually on disk (symlinks, ownership, existing targets)
// live in packages link, setup, and trust, and apply equally to listed and
// discovered entries.
//
// Unlike the packages that use it, config builds on every operating system,
// so its rules are testable anywhere.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nicholashoule/homeskillet/internal/safefile"
)

const (
	// FileName is the manifest's name at the configuration repository root,
	// and the name init gives a new one: besides the dotfiles to link, a
	// manifest lists the setup steps that install what they use.
	FileName = "bootstrap.json"
	// DotfilesFileName is the manifest's other name, the one it first had. A
	// repository may use either name or both, for example listing its files
	// in dotfiles.json and its setup steps in bootstrap.json; Load merges
	// them (see Manifest.merge).
	DotfilesFileName = "dotfiles.json"
	// HomeDir is the repository directory that mirrors a home directory. Its
	// files are linked when the manifest is absent or omits "files".
	HomeDir = "home"
	// WSLHomeDir is the directory in HomeDir that mirrors the home of a WSL
	// distribution, for a Windows repository that configures WSL as well.
	// When it exists, its files are linked on Linux alone, which is what WSL
	// runs, whatever the manifest's platforms say, and HomeDir's other files
	// on the other systems the manifest covers.
	WSLHomeDir = HomeDir + "/wsl"
	// HomePrefix may start a path a manifest gives relative to the home, a
	// listed file's target or a step's creates, to say so the same way on
	// every system: "~/.bashrc" is ".bashrc", whether the home is /home/you
	// or C:\Users\you. "$HOME/" and "${HOME}/" may too (see fromHome).
	HomePrefix = "~/"
	// DocumentsPrefix starts a step's creates path that is relative to the
	// user's Documents folder rather than the home directory, such as
	// {documents}/PowerShell/profile.ps1, where PowerShell reads its profile.
	// On Windows, OneDrive and administrators can move Documents out of the
	// profile, so package setup asks Windows where it is.
	DocumentsPrefix = "{documents}/"
)

// Manifest is what a repository declares, as returned by Load.
type Manifest struct {
	// Platforms is the default for every file and step, listed or
	// discovered: the platforms they apply to, each an operating system such
	// as "darwin" or an operating system and architecture such as
	// "darwin/arm64" (see ParsePlatform). Empty means every supported
	// platform. A repository for one operating system says so once here
	// instead of on every entry.
	Platforms []string `json:"platforms,omitempty"`
	// Files lists the files to link. Load fills it from HomeDir when the
	// manifest omits "files".
	Files []Entry `json:"files,omitempty"`
	// Setup lists commands that prepare the environment, in the order they
	// are offered.
	Setup []Step `json:"setup,omitempty"`
}

// Entry maps one file in the repository to one path in the home directory.
type Entry struct {
	// Source is relative to the configuration repository root.
	Source string `json:"source"`
	// Target is relative to the home directory.
	Target string `json:"target"`
	// Platforms, when set, replaces Manifest.Platforms for this entry. Empty
	// means the manifest's default. To apply everywhere despite a narrower
	// default, list every platform.
	Platforms []string `json:"platforms,omitempty"`
	// from names the manifest that lists the entry, for errors. It is empty
	// for a file found under HomeDir, whose path says where it is.
	from string
}

// Step is a command that prepares the environment, such as installing a tool
// the linked files use. A step is code from the repository, so
// package setup shows each one and runs it only after the user approves it.
type Step struct {
	// Name identifies the step in prompts and output.
	Name string `json:"name"`
	// Run is what the step executes: one command, or several that belong
	// together, such as a brew install per tap, run in order as one step
	// (see Commands). Each is executed directly, not through a shell, so the
	// text shown for review is exactly what runs. A step that needs pipes or
	// other shell features says so explicitly with ["sh", "-c", "..."].
	Run Commands `json:"run"`
	// Platforms, when set, replaces Manifest.Platforms for this step, as
	// for Entry.Platforms.
	Platforms []string `json:"platforms,omitempty"`
	// Creates is a path relative to the home directory, or, starting with
	// DocumentsPrefix, relative to the user's Documents folder. If it exists,
	// the step is already done and is not offered.
	Creates string `json:"creates,omitempty"`
	// Provides is a command name. If it is found on PATH, the step is already
	// done and is not offered.
	Provides string `json:"provides,omitempty"`
	// from names the manifest that lists the step, for errors.
	from string
}

// ErrNothingToManage is returned by Load for a directory with nothing to
// link or set up, which most likely is not a configuration repository at all.
var ErrNothingToManage = errors.New("not a configuration repository")

// Load reads the declarations of the configuration repository rooted at repo.
//
// The manifest is optional, and may be split between FileName and
// DotfilesFileName, which Load merges. When no manifest lists "files", Load
// discovers the files under HomeDir, in lexical order. An explicit empty
// list, "files": [], links nothing, for a repository that only declares setup
// steps. A repository with neither a manifest nor a home directory is an
// error, not an empty configuration, because it most likely means the wrong
// directory was given.
//
// The error joins every issue found, so that one run shows all there is to
// fix; Issues splits it.
//
// Every read goes through an os.Root, so neither a symlinked manifest nor a
// symlink under HomeDir can make Load read from outside repo.
func Load(repo string) (Manifest, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := load(root)
	return manifest, errors.Join(err, root.Close())
}

func load(root *os.Root) (Manifest, error) {
	manifest, found, err := readManifests(root)
	if err != nil {
		return Manifest{}, err
	}
	// Files is nil only when no manifest exists or it omits "files". An
	// explicit [] decodes to an empty, non-nil slice and disables discovery.
	hasHome, listed := false, manifest.Files != nil
	if !listed {
		manifest.Files, hasHome, err = manifest.discoverAll(root, homes(root))
		if err != nil {
			return Manifest{}, err
		}
	}
	if err := manifest.checkWindowsNames(); err != nil {
		return Manifest{}, err
	}
	if !hasHome && len(manifest.Files) == 0 && len(manifest.Setup) == 0 {
		// Saying exactly what is missing tells a wrong directory, which has
		// neither a manifest nor a home directory, from a manifest that lists
		// nothing. HomeDir gets its slash so that it never reads as the home
		// directory homeskillet links into.
		why := fmt.Sprintf("it has no %s or %s and no %s/ directory", FileName, DotfilesFileName, HomeDir)
		names, verb := strings.Join(found, " and "), "lists"
		if len(found) > 1 {
			verb = "list"
		}
		switch {
		case len(found) > 0 && listed:
			why = fmt.Sprintf("its %s %s no files or setup steps", names, verb)
		case len(found) > 0:
			why = fmt.Sprintf("it has no %s/ directory, and its %s %s no setup steps", HomeDir, names, verb)
		}
		return Manifest{}, fmt.Errorf("%s is %w: %s", root.Name(), ErrNothingToManage, why)
	}
	return manifest, nil
}

// FileNames returns the names a manifest may have at the repository root, in
// the order Load merges them.
func FileNames() []string {
	return []string{FileName, DotfilesFileName}
}

// Issues splits an error from Load into the issues it joins, in the order
// they were found. Any other error is a single issue. Load joins issues only
// at the top, never inside an error that adds its own words, so splitting
// what errors.As finds keeps each issue whole.
func Issues(err error) []error {
	if err == nil {
		return nil
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		return []error{err}
	}
	var issues []error
	for _, issue := range joined.Unwrap() {
		issues = append(issues, Issues(issue)...)
	}
	return issues
}

// readManifests reads every manifest root has (see FileNames) and merges
// them, returning the names it found. What is valid in each is merged even
// when the other has issues, so that the error lists every issue in both,
// those of merging included.
func readManifests(root *os.Root) (Manifest, []string, error) {
	var (
		merged Manifest
		found  []string
		issues []error
	)
	for _, name := range FileNames() {
		manifest, exists, err := readManifest(root, name)
		if !exists {
			continue
		}
		found = append(found, name)
		if err != nil {
			issues = append(issues, err)
		}
		issues = append(issues, merged.merge(manifest)...)
	}
	return merged, found, errors.Join(issues...)
}

// readManifest decodes and validates the manifest called name in root, and
// reports whether it exists. A manifest that exists but cannot be read, such
// as a dangling symlink, is an error rather than an absent manifest, so a
// broken manifest never silently falls back to discovery.
func readManifest(root *os.Root, name string) (Manifest, bool, error) {
	if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	// The error names the file already.
	file, err := root.Open(name)
	if err != nil {
		return Manifest{}, true, err
	}
	// Not errors.Join: Issues splits joined errors, and this is one issue.
	data, err := safefile.ReadAll(file, maxManifest)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Manifest{}, true, fmt.Errorf("%s: %w", name, err)
	}
	manifest, err := decode(bytes.NewReader(data), name)
	return manifest, true, err
}

// merge adds next, a manifest read after the ones already in m, to m, and
// returns the issues that keep them apart. Platforms and files are combined,
// with next's files after m's and next's steps after m's, so a repository can
// keep its files in one manifest and its setup steps in the other. An entry
// or step m already has, the same in every field, is skipped, so a line
// copied into both counts once. Two different steps with one name are an
// issue rather than one silently replacing the other: steps are chosen by
// name, and the one left out could be the one just edited.
func (m *Manifest) merge(next Manifest) []error {
	for _, platform := range next.Platforms {
		if !slices.Contains(m.Platforms, platform) {
			m.Platforms = append(m.Platforms, platform)
		}
	}
	var issues []error
	// A listed "files", even an empty one, turns discovery off (see load),
	// so it must stay non-nil when merged.
	if next.Files != nil {
		earlier := m.Files
		if m.Files == nil {
			m.Files = []Entry{}
		}
		for _, entry := range next.Files {
			if !slices.ContainsFunc(earlier, entry.same) {
				m.Files = append(m.Files, entry)
			}
		}
	}
	earlier := m.Setup
	for _, step := range next.Setup {
		i := slices.IndexFunc(earlier, func(other Step) bool { return other.Name == step.Name })
		switch {
		case i < 0:
			m.Setup = append(m.Setup, step)
		case !earlier[i].same(step):
			issues = append(issues, fmt.Errorf("%ssetup step %s is listed twice, differently; keep one", origin(earlier[i].from, step.from), step.Name))
		}
	}
	return issues
}

// same reports whether e and other are the same entry, field for field.
func (e Entry) same(other Entry) bool {
	return e.Source == other.Source && e.Target == other.Target && slices.Equal(e.Platforms, other.Platforms)
}

// same reports whether s and other are the same step, field for field.
func (s Step) same(other Step) bool {
	return s.Name == other.Name && s.Creates == other.Creates && s.Provides == other.Provides &&
		slices.Equal(s.Platforms, other.Platforms) && slices.EqualFunc(s.Run, other.Run, slices.Equal)
}

// origin starts an error about entries or steps with the manifests that list
// them, as "bootstrap.json: " or "bootstrap.json and dotfiles.json: ". It is
// empty for files found under HomeDir, whose paths say where they are.
func origin(from ...string) string {
	var names []string
	for _, name := range from {
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, " and ") + ": "
}

// maxManifest bounds what readManifest reads. A manifest listing thousands of
// files is a few hundred kilobytes, so a larger file is a mistake, such as a
// device or a huge file swapped in for the manifest, and is refused before
// it can exhaust memory.
const maxManifest = 1 << 20

// decode parses and validates the manifest called name. It returns every
// issue it finds, joined, rather than only the first, with the manifest
// without the entries and steps that have them, so the rest can still be
// merged and checked against the other manifest.
//
// Decoding is strict. Unknown fields and trailing values are rejected so that
// a typo such as a singular "platform" key fails loudly instead of silently
// linking a file on every operating system.
func decode(r io.Reader, name string) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", name, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%s must contain exactly one JSON object", name)
	}
	// Errors name entries by position rather than quoting them, because a
	// rejected value may itself be unsafe to print.
	var issues []error
	if err := validatePlatforms(manifest.Platforms); err != nil {
		issues = append(issues, fmt.Errorf("%s platforms: %w", name, err))
		manifest.Platforms = nil
	}
	// Slicing keeps a listed "files", even an empty one, non-nil (see load),
	// and an omitted one nil.
	files := manifest.Files[:0:0]
	for i, entry := range manifest.Files {
		entry.from = name
		entry.Target = fromHome(entry.Target)
		if unexpanded(entry.Source) || unexpanded(entry.Target) {
			issues = append(issues, fmt.Errorf(`%s file %d: a source is relative to the repository and a target to the home, as ".bashrc", "~/.bashrc", or "$HOME/.bashrc"; `+
				"neither may otherwise start with ~, $, or %%, as homeskillet expands no variables", name, i+1))
			continue
		}
		if err := entry.validate(); err != nil {
			issues = append(issues, fmt.Errorf("%s file %d: %w", name, i+1, err))
			continue
		}
		files = append(files, entry)
	}
	manifest.Files = files
	var steps []Step
	names := make(map[string]bool)
	for i, step := range manifest.Setup {
		step.from = name
		step.Creates = fromHome(step.Creates)
		switch err := step.validate(); {
		case err != nil:
			issues = append(issues, fmt.Errorf("%s setup step %d: %w", name, i+1, err))
		// Only a valid name is printed, as it cannot rewrite the terminal.
		case names[step.Name]:
			issues = append(issues, fmt.Errorf("%s setup step %d: duplicate name %s", name, i+1, step.Name))
		default:
			steps = append(steps, step)
		}
		names[step.Name] = true
	}
	manifest.Setup = steps
	return manifest, errors.Join(issues...)
}

// homes returns the directories other than HomeDir whose files are linked on
// one operating system alone, by system: WSLHomeDir, for Linux, when root
// has it.
func homes(root *os.Root) map[string]string {
	if info, err := root.Stat(WSLHomeDir); err == nil && info.IsDir() {
		return map[string]string{"linux": WSLHomeDir}
	}
	return nil
}

// homeRoot is a directory files are found under and the operating systems
// whose homes they are linked into (see homes).
type homeRoot struct {
	dir     string
	systems []string
}

// homeRoots returns the directories files are found under, HomeDir's first,
// each with the operating systems it serves.
func homeRoots(homes map[string]string) []homeRoot {
	var roots []homeRoot
	for _, system := range operatingSystems {
		dir, ok := homes[system]
		if !ok {
			dir = HomeDir
		}
		i := slices.IndexFunc(roots, func(r homeRoot) bool { return r.dir == dir })
		if i < 0 {
			roots = append(roots, homeRoot{dir: dir})
			i = len(roots) - 1
		}
		roots[i].systems = append(roots[i].systems, system)
	}
	slices.SortStableFunc(roots, func(a, b homeRoot) int {
		return boolOrder(a.dir != HomeDir) - boolOrder(b.dir != HomeDir)
	})
	return roots
}

// boolOrder is 1 for true and 0 for false, for sorting.
func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}

// discoverAll lists the files under every directory files are found under
// (see homes) and reports whether any of them exists. With HomeDir alone,
// its entries take the manifest's platforms as listed ones do. Otherwise
// each directory's entries apply to the systems it serves, as their own
// platforms: WSLHomeDir's to Linux, whatever the default says, since that is
// what the directory is for, and HomeDir's to the default's other systems.
// A directory never lists the files of another one inside it, so
// WSLHomeDir's files are never also linked from HomeDir.
func (m Manifest) discoverAll(root *os.Root, homes map[string]string) (entries []Entry, found bool, err error) {
	if len(homes) == 0 {
		return discover(root, HomeDir, nil, nil)
	}
	roots := homeRoots(homes)
	for _, r := range roots {
		var inside []string
		for _, other := range roots {
			if strings.HasPrefix(other.dir, r.dir+"/") {
				inside = append(inside, other.dir)
			}
		}
		platforms := m.rootPlatforms(r, homes)
		if len(platforms) == 0 {
			continue
		}
		listed, exists, err := discover(root, r.dir, inside, platforms)
		if err != nil {
			return nil, false, err
		}
		entries = append(entries, listed...)
		found = found || exists
	}
	return entries, found, nil
}

// rootPlatforms returns the platforms the files found under r apply to (see
// discoverAll): each system that has r as a home of its own, and each other
// system r serves that the manifest's default covers, with the default's
// architecture.
func (m Manifest) rootPlatforms(r homeRoot, homes map[string]string) []string {
	var platforms []string
	for _, system := range r.systems {
		if _, own := homes[system]; own || len(m.Platforms) == 0 {
			platforms = append(platforms, system)
			continue
		}
		for _, value := range m.Platforms {
			if p, err := ParsePlatform(value); err == nil && p.OS == system && !slices.Contains(platforms, value) {
				platforms = append(platforms, value)
			}
		}
	}
	return platforms
}

// discover lists every file under dir, except under the directories in skip,
// as an entry linking it to the same relative path in the home directory,
// with platforms as its own. It reports whether dir exists.
//
// Symlinks are listed, not followed. Package link then requires each source,
// listed or discovered, to resolve to a regular file inside the repository,
// and checks the ownership of every directory from the repository root down to
// it. So a file that another user added, or placed in a directory another user
// can write, is still refused even though no manifest names it.
func discover(root *os.Root, dir string, skip, platforms []string) (entries []Entry, found bool, err error) {
	// Stat rather than Lstat: a home directory that is a symlink within the
	// repository, for example to one of several per-machine directories, is
	// fine, and os.Root refuses one that leads outside.
	info, err := root.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("%s in %s is not a directory", dir, root.Name())
	}
	err = fs.WalkDir(root.FS(), dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Checking every name, directories included, before WalkDir reads a
		// directory means neither an entry nor a later error message can
		// carry a name that rewrites the terminal.
		if !Printable(name) {
			return fmt.Errorf("%q: names must not contain control or formatting characters", name)
		}
		if name != dir && ignored(d.Name()) || d.IsDir() && slices.Contains(skip, name) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// fs.FS names always use forward slashes and are rooted at the
		// repository, so the target is the name without the directory.
		entry := Entry{
			Source:    filepath.FromSlash(name),
			Target:    filepath.FromSlash(strings.TrimPrefix(name, dir+"/")),
			Platforms: platforms,
		}
		// The same lexical rules as a listed entry, so the two can never
		// drift apart.
		if err := entry.validate(); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		entries = append(entries, entry)
		return nil
	})
	return entries, true, err
}

// ignored reports whether discovery skips a file or directory name. These
// names are written by tools, never by a repository's author:
//
//   - macOS Finder creates .DS_Store in every directory it displays, and
//     ~/.DS_Store exists on most Macs, so linking it would be a conflict that
//     blocks every link.
//   - Windows Explorer creates Thumbs.db and desktop.ini the same way, and
//     folders such as Documents already hold a desktop.ini.
//   - .git holds a nested repository's or submodule's metadata, which is not
//     configuration.
//
// Case is ignored, as Windows and macOS file systems usually ignore it. A
// repository that needs finer control lists its files in the manifest.
func ignored(name string) bool {
	return slices.ContainsFunc([]string{".DS_Store", "Thumbs.db", "desktop.ini", ".git"}, func(skip string) bool {
		return strings.EqualFold(name, skip)
	})
}

// FilesFor returns the file entries that apply to p, in manifest order:
// those whose own platforms, or else the manifest's, cover p.
func (m Manifest) FilesFor(p Platform) []Entry {
	var selected []Entry
	for _, entry := range m.Files {
		if m.appliesTo(entry.Platforms, p) {
			selected = append(selected, entry)
		}
	}
	return selected
}

// SetupFor returns the setup steps that apply to p, in manifest order,
// chosen as in FilesFor.
func (m Manifest) SetupFor(p Platform) []Step {
	var selected []Step
	for _, step := range m.Setup {
		if m.appliesTo(step.Platforms, p) {
			selected = append(selected, step)
		}
	}
	return selected
}

// Printable reports whether s is free of control and formatting characters.
// Terminal escape sequences (ESC is a control character) can erase or forge
// lines of output, and bidirectional overrides such as U+202E RIGHT-TO-LEFT
// OVERRIDE reorder text, so either could disguise what a link or setup step
// really does.
func Printable(s string) bool {
	// Invalid UTF-8 is refused too: a raw byte such as 0x9B is a control
	// character to a terminal that is not in UTF-8 mode, and encoding/json
	// would replace it, so a record could no longer name the file.
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
	})
}

// appliesTo reports whether an entry or step with its own platforms applies
// to p. Its own list, when set, replaces the manifest's default rather than
// narrowing it, so one entry can apply where the default does not.
func (m Manifest) appliesTo(own []string, p Platform) bool {
	platforms := own
	if len(platforms) == 0 {
		platforms = m.Platforms
	}
	return len(platforms) == 0 || slices.ContainsFunc(platforms, p.Matches)
}

// Platform is an operating system and a processor architecture, as Go names
// them: GOOS "linux" or "darwin", and GOARCH "amd64" or "arm64". In a
// manifest, or in --platform, Arch may be left out to mean any architecture.
type Platform struct {
	OS   string
	Arch string // "" means any architecture
}

// The operating systems and architectures a platform may name. With
// --platform, homeskillet acts for one of them from another, as --platform
// windows checks a Windows repository from Linux or macOS: its files link into
// a scratch home and its steps can be listed, never run.
var (
	operatingSystems = []string{"linux", "darwin", "windows"}
	architectures    = []string{"amd64", "arm64"}
)

// PlatformValues lists every value ParsePlatform accepts, for shell
// completion: each operating system alone, then with each architecture.
func PlatformValues() []string {
	var values []string
	for _, system := range operatingSystems {
		values = append(values, system)
		for _, arch := range architectures {
			values = append(values, system+"/"+arch)
		}
	}
	return values
}

// ParsePlatform parses "os" or "os/arch", such as "darwin" or "darwin/arm64".
// Only supported operating systems and architectures are accepted, so a typo
// such as "macos" or "darwin/arm" fails instead of silently matching nothing.
func ParsePlatform(s string) (Platform, error) {
	system, arch, hasArch := strings.Cut(s, "/")
	if !slices.Contains(operatingSystems, system) || hasArch && !slices.Contains(architectures, arch) {
		return Platform{}, fmt.Errorf("unknown platform %q: use %s, optionally followed by /%s",
			s, strings.Join(operatingSystems, " or "), strings.Join(architectures, " or /"))
	}
	return Platform{OS: system, Arch: arch}, nil
}

// String returns p as ParsePlatform reads it.
func (p Platform) String() string {
	if p.Arch == "" {
		return p.OS
	}
	return p.OS + "/" + p.Arch
}

// Matches reports whether value, a manifest platform such as "darwin" or
// "darwin/arm64", covers p. A value without an architecture covers every
// architecture of its operating system. A value with one never matches a p
// whose architecture is unknown.
func (p Platform) Matches(value string) bool {
	want, err := ParsePlatform(value)
	return err == nil && want.OS == p.OS && (want.Arch == "" || want.Arch == p.Arch)
}

// targets reports whether an entry or step with its own platforms is meant
// for operating system goos: whether its platforms, or else the manifest's,
// name goos, with or without an architecture. An entry with no platforms at
// all is not counted, since the rules a target checks may not apply where it
// was never meant to run.
func (m Manifest) targets(own []string, goos string) bool {
	platforms := own
	if len(platforms) == 0 {
		platforms = m.Platforms
	}
	return slices.ContainsFunc(platforms, func(value string) bool {
		p, err := ParsePlatform(value)
		return err == nil && p.OS == goos
	})
}

// checkWindowsNames applies Windows' naming rules to every file and step
// meant for Windows (see targets), so a repository checked on Linux or macOS
// cannot hold names that Windows could not create: device names such as CON
// or NUL, names ending in a dot or a space, the characters <>:"|?*\, and two
// targets that differ only in case, which Windows treats as one file. A
// repository with anything for Windows is cloned there, so the source of
// every file in it is held to the same rules, whatever system the file is
// for, as a Windows repository's home/wsl is.
//
// It returns every issue, joined, rather than only the first.
func (m Manifest) checkWindowsNames() error {
	var issues []error
	cloned := m.forWindows()
	targets, sources := make(map[string]Entry), make(map[string]Entry)
	for _, entry := range m.Files {
		meant := m.targets(entry.Platforms, "windows")
		if !meant && !cloned {
			continue
		}
		paths := []string{entry.Source}
		if meant {
			paths = append(paths, entry.Target)
		}
		for _, path := range paths {
			if err := windowsPath(path); err != nil {
				issues = append(issues, fmt.Errorf("%s%q cannot exist on Windows: %w", origin(entry.from), path, err))
			}
		}
		key := strings.ToLower(filepath.ToSlash(entry.Source))
		if other, ok := sources[key]; !ok {
			sources[key] = entry
		} else if other.Source != entry.Source {
			issues = append(issues, fmt.Errorf("%ssources %q and %q are the same file on Windows, which ignores case",
				origin(other.from, entry.from), other.Source, entry.Source))
		}
		if !meant {
			continue
		}
		key = strings.ToLower(filepath.ToSlash(entry.Target))
		if other, ok := targets[key]; ok {
			issues = append(issues, fmt.Errorf("%stargets %q and %q are the same file on Windows, which ignores case",
				origin(other.from, entry.from), other.Target, entry.Target))
			continue
		}
		targets[key] = entry
	}
	for _, step := range m.Setup {
		if step.Creates != "" && m.targets(step.Platforms, "windows") {
			if err := windowsPath(strings.TrimPrefix(step.Creates, DocumentsPrefix)); err != nil {
				issues = append(issues, fmt.Errorf("%ssetup step %s: creates %q cannot exist on Windows: %w", origin(step.from), step.Name, step.Creates, err))
			}
		}
	}
	return errors.Join(issues...)
}

// forWindows reports whether anything in m is meant for Windows, so that the
// repository is cloned there (see checkWindowsNames).
func (m Manifest) forWindows() bool {
	return m.targets(nil, "windows") ||
		slices.ContainsFunc(m.Files, func(entry Entry) bool { return m.targets(entry.Platforms, "windows") }) ||
		slices.ContainsFunc(m.Setup, func(step Step) bool { return m.targets(step.Platforms, "windows") })
}

// windowsReserved are the device names Windows reserves in every directory,
// with or without an extension, so neither nul nor nul.txt can be a file.
var windowsReserved = []string{
	"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "COM¹", "COM²", "COM³",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "LPT¹", "LPT²", "LPT³",
}

// windowsPath checks each name in path, which separates names with '/' as
// manifests do, against Windows' rules (see checkWindowsNames).
func windowsPath(path string) error {
	for name := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		switch {
		case strings.ContainsAny(name, `<>:"|?*\`):
			return fmt.Errorf("%q holds one of <>:\"|?*\\", name)
		case strings.HasSuffix(name, ".") && name != "." && name != "..", strings.HasSuffix(name, " "):
			return fmt.Errorf("%q ends in a dot or a space", name)
		}
		base, _, _ := strings.Cut(name, ".")
		if slices.ContainsFunc(windowsReserved, func(reserved string) bool { return strings.EqualFold(base, reserved) }) {
			return fmt.Errorf("%q is a reserved device name", name)
		}
	}
	return nil
}

func validatePlatforms(platforms []string) error {
	for _, platform := range platforms {
		if _, err := ParsePlatform(platform); err != nil {
			return err
		}
	}
	return nil
}

// localFile reports whether path names a file below its root. IsLocal rejects
// empty, absolute, and ".."-escaping paths without touching the filesystem. It
// accepts paths that clean to "." (the root itself), which name a directory
// rather than a file, so those are rejected separately. Escapes through
// symlinks can only be seen on disk and are caught in package link.
func localFile(path string) bool {
	return filepath.IsLocal(path) && filepath.Clean(path) != "."
}

// homePrefixes are the ways a manifest path may say it is relative to the
// home: HomePrefix, and $HOME as shells write it. Each is a fixed prefix
// that fromHome removes, never a variable that is expanded.
var homePrefixes = []string{HomePrefix, "$HOME/", "${HOME}/"}

// fromHome returns p, a path relative to the home, in the one form the rest
// of homeskillet takes, without any of homePrefixes.
func fromHome(p string) string {
	for _, prefix := range homePrefixes {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			return rest
		}
	}
	return p
}

// unexpanded reports whether p, a manifest path after fromHome, starts as a
// shell path homeskillet does not expand would: with ~, as in ~user, $, as
// in $XDG_CONFIG_HOME, or %, as in %USERPROFILE%. homeskillet expands no
// variables, so such a path would name a directory called ~user or
// $XDG_CONFIG_HOME, which is never what was meant.
func unexpanded(p string) bool {
	return strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$") || strings.HasPrefix(p, "%")
}

func (e Entry) validate() error {
	if !localFile(e.Source) || !localFile(e.Target) {
		return errors.New("source and target must be nonempty relative file paths without escaping their roots")
	}
	// status and dry runs print these paths, so they must not be able to
	// disguise that output.
	if !Printable(e.Source) || !Printable(e.Target) {
		return errors.New("source and target must not contain control or formatting characters")
	}
	return validatePlatforms(e.Platforms)
}

// validName keeps step names short identifiers that are safe to print and to
// use as keys in future state files: lowercase ASCII letters and digits, plus
// '.', '_', and '-' after the first character.
func validName(name string) bool {
	for i, r := range name {
		switch {
		case 'a' <= r && r <= 'z', '0' <= r && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return name != ""
}

// Commands is a setup step's run: argument vectors, executed in order. In a
// manifest, "run" holds one command, ["brew", "install", "jq"], or a list of
// them, [["brew", "update"], ["brew", "install", "jq"]]. The list is shown
// and approved as one step and stops at the first command that fails.
type Commands [][]string

// UnmarshalJSON reads either form of "run" (see Commands).
func (c *Commands) UnmarshalJSON(data []byte) error {
	var one []string
	if err := json.Unmarshal(data, &one); err == nil {
		*c = Commands{one}
		return nil
	}
	var several [][]string
	if err := json.Unmarshal(data, &several); err != nil {
		return errors.New(`run must be a command, such as ["brew", "install", "jq"], or a list of commands`)
	}
	*c = several
	return nil
}

// MarshalJSON writes one command in the plain form, as a manifest would
// hold it, and several as a list.
func (c Commands) MarshalJSON() ([]byte, error) {
	if len(c) == 1 {
		return json.Marshal(c[0])
	}
	return json.Marshal([][]string(c))
}

func (s Step) validate() error {
	if !validName(s.Name) {
		return errors.New("name must be lowercase letters, digits, '.', '_', or '-', starting with a letter or digit")
	}
	if len(s.Run) == 0 {
		return errors.New("run must name a command")
	}
	for _, command := range s.Run {
		if len(command) == 0 || command[0] == "" {
			return errors.New("run must name a command")
		}
		// The command is shown for approval. Any argument that could rewrite
		// the terminal could hide part of what the user is approving.
		for _, arg := range command {
			if !Printable(arg) {
				return errors.New("run must not contain control or formatting characters")
			}
		}
	}
	// Only DocumentsPrefix may start with a brace, so a misspelled prefix,
	// such as {document}/, fails instead of naming a directory in home.
	if creates, documents := strings.CutPrefix(s.Creates, DocumentsPrefix); s.Creates != "" &&
		(!localFile(creates) || !Printable(creates) || unexpanded(creates) || !documents && strings.HasPrefix(creates, "{")) {
		return errors.New("creates must be a printable path in the home directory, as .local/bin/uv, ~/.local/bin/uv, or $HOME/.local/bin/uv, or start with " +
			DocumentsPrefix + "; homeskillet expands no other variables")
	}
	// A name with a separator or, on Windows, a drive would be a path, which
	// command lookup returns as it is, found or not.
	if s.Provides != "" && (strings.ContainsAny(s.Provides, `/\:`) || !Printable(s.Provides)) {
		return errors.New(`provides must be a printable command name without '/', '\', or ':'`)
	}
	return validatePlatforms(s.Platforms)
}
