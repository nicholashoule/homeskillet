// Package config reads what a configuration repository declares: the files
// homeskillet links into a home directory and the setup steps it offers to
// run.
//
// A repository declares them in a dotfiles.json manifest at its root, or
// relies on a convention: when the manifest is absent or omits "files", every
// file under the repository's home directory is linked to the same relative
// path in the user's home, so home/.bashrc becomes ~/.bashrc. Setup steps
// always come from the manifest, because commands are never inferred.
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
	// FileName is the manifest's name at the configuration repository root.
	FileName = "dotfiles.json"
	// HomeDir is the repository directory that mirrors a home directory. Its
	// files are linked when the manifest is absent or omits "files".
	HomeDir = "home"
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
	// Creates is a path relative to the home directory. If it exists, the
	// step is already done and is not offered.
	Creates string `json:"creates,omitempty"`
	// Provides is a command name. If it is found on PATH, the step is already
	// done and is not offered.
	Provides string `json:"provides,omitempty"`
}

// ErrNothingToManage is returned by Load for a directory with nothing to
// link or set up, which most likely is not a configuration repository at all.
var ErrNothingToManage = errors.New("not a configuration repository")

// Load reads the declarations of the configuration repository rooted at repo.
//
// The manifest is optional. Without one, or when it omits "files", Load
// discovers the files under HomeDir, in lexical order. An explicit empty list,
// "files": [], links nothing, for a repository that only declares setup
// steps. A repository with neither a manifest nor a home directory is an
// error, not an empty configuration, because it most likely means the wrong
// directory was given.
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
	manifest, err := readManifest(root)
	if err != nil {
		return Manifest{}, err
	}
	// Files is nil only when no manifest exists or it omits "files". An
	// explicit [] decodes to an empty, non-nil slice and disables discovery.
	hasHome, listed := false, manifest.Files != nil
	if !listed {
		manifest.Files, hasHome, err = discover(root)
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
		why := fmt.Sprintf("it has no %s and no %s/ directory", FileName, HomeDir)
		if _, err := root.Lstat(FileName); err == nil && listed {
			why = fmt.Sprintf("its %s lists no files or setup steps", FileName)
		} else if err == nil {
			why = fmt.Sprintf("it has no %s/ directory, and its %s lists no setup steps", HomeDir, FileName)
		}
		return Manifest{}, fmt.Errorf("%s is %w: %s", root.Name(), ErrNothingToManage, why)
	}
	return manifest, nil
}

// readManifest decodes and validates FileName from root. It returns an empty
// Manifest when the repository has none. A manifest that exists but cannot be
// read, such as a dangling symlink, is an error rather than an absent
// manifest, so a broken manifest never silently falls back to discovery.
func readManifest(root *os.Root) (Manifest, error) {
	if _, err := root.Lstat(FileName); errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	file, err := root.Open(FileName)
	if err != nil {
		return Manifest{}, err
	}
	data, err := safefile.ReadAll(file, maxManifest)
	if err := errors.Join(err, file.Close()); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", FileName, err)
	}
	return decode(bytes.NewReader(data))
}

// maxManifest bounds what readManifest reads. A manifest listing thousands of
// files is a few hundred kilobytes, so a larger file is a mistake, such as a
// device or a huge file swapped in for the manifest, and is refused before
// it can exhaust memory.
const maxManifest = 1 << 20

// decode parses and validates a manifest.
//
// Decoding is strict. Unknown fields and trailing values are rejected so that
// a typo such as a singular "platform" key fails loudly instead of silently
// linking a file on every operating system.
func decode(r io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%s must contain exactly one JSON object", FileName)
	}
	// Errors name entries by position rather than quoting them, because a
	// rejected value may itself be unsafe to print.
	if err := validatePlatforms(manifest.Platforms); err != nil {
		return Manifest{}, fmt.Errorf("%s platforms: %w", FileName, err)
	}
	for i, entry := range manifest.Files {
		if err := entry.validate(); err != nil {
			return Manifest{}, fmt.Errorf("%s file %d: %w", FileName, i+1, err)
		}
	}
	names := make(map[string]bool)
	for i, step := range manifest.Setup {
		if err := step.validate(); err != nil {
			return Manifest{}, fmt.Errorf("%s setup step %d: %w", FileName, i+1, err)
		}
		if names[step.Name] {
			return Manifest{}, fmt.Errorf("%s setup step %d: duplicate name %s", FileName, i+1, step.Name)
		}
		names[step.Name] = true
	}
	return manifest, nil
}

// discover lists every file under HomeDir as an entry linking it to the same
// relative path in the home directory. It reports whether HomeDir exists.
//
// Symlinks are listed, not followed. Package link then requires each source,
// listed or discovered, to resolve to a regular file inside the repository,
// and checks the ownership of every directory from the repository root down to
// it. So a file that another user added, or placed in a directory another user
// can write, is still refused even though no manifest names it.
func discover(root *os.Root) (entries []Entry, found bool, err error) {
	// Stat rather than Lstat: a home directory that is a symlink within the
	// repository, for example to one of several per-machine directories, is
	// fine, and os.Root refuses one that leads outside.
	info, err := root.Stat(HomeDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("%s in %s is not a directory", HomeDir, root.Name())
	}
	err = fs.WalkDir(root.FS(), HomeDir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Checking every name, directories included, before WalkDir reads a
		// directory means neither an entry nor a later error message can
		// carry a name that rewrites the terminal.
		if !Printable(name) {
			return fmt.Errorf("%q: names must not contain control or formatting characters", name)
		}
		if name != HomeDir && ignored(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// fs.FS names always use forward slashes and are rooted at the
		// repository, so the target is the name without the HomeDir prefix.
		entry := Entry{
			Source: filepath.FromSlash(name),
			Target: filepath.FromSlash(strings.TrimPrefix(name, HomeDir+"/")),
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

// The operating systems and architectures a platform may name. homeskillet
// runs only on Linux and macOS so far, but a manifest may already target
// windows, and --platform windows checks such a repository from Linux or
// macOS: its files link into a scratch home and its steps can be listed, never
// run. See docs/windows.md for what running on Windows still needs.
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
// targets that differ only in case, which Windows treats as one file.
func (m Manifest) checkWindowsNames() error {
	seen := make(map[string]string)
	for _, entry := range m.Files {
		if !m.targets(entry.Platforms, "windows") {
			continue
		}
		for _, path := range []string{entry.Source, entry.Target} {
			if err := windowsPath(path); err != nil {
				return fmt.Errorf("%s: %q cannot exist on Windows: %w", FileName, path, err)
			}
		}
		key := strings.ToLower(filepath.ToSlash(entry.Target))
		if other, ok := seen[key]; ok {
			return fmt.Errorf("%s: targets %q and %q are the same file on Windows, which ignores case", FileName, other, entry.Target)
		}
		seen[key] = entry.Target
	}
	for _, step := range m.Setup {
		if step.Creates != "" && m.targets(step.Platforms, "windows") {
			if err := windowsPath(step.Creates); err != nil {
				return fmt.Errorf("%s setup step %s: creates %q cannot exist on Windows: %w", FileName, step.Name, step.Creates, err)
			}
		}
	}
	return nil
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
	if s.Creates != "" && (!localFile(s.Creates) || !Printable(s.Creates)) {
		return errors.New("creates must be a printable relative path inside the home directory")
	}
	if s.Provides != "" && (strings.ContainsRune(s.Provides, '/') || !Printable(s.Provides)) {
		return errors.New("provides must be a printable command name without '/'")
	}
	return validatePlatforms(s.Platforms)
}
