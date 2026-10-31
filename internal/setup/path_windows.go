// Finding programs on Windows: the PATH installers record in the registry,
// the home's .local\bin and winget's links, and lookup through PATHEXT.

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// homebrewBins is empty: Homebrew does not run on Windows.
var homebrewBins []string

// appended returns the directories searchPath adds to PATH, in the order a
// new terminal would search them: the system's PATH and the user's, as the
// registry holds them now, where installers such as winget's record the
// directories they add; then the home's .local\bin, where uv and other
// installers put programs, and winget's Links directory, where it puts the
// commands of portable packages.
func appended(home string) []string {
	dirs := registryPath(syscall.HKEY_LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)
	dirs = append(dirs, registryPath(syscall.HKEY_CURRENT_USER, "Environment")...)
	return append(dirs,
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "AppData", "Local", "Microsoft", "WinGet", "Links"))
}

// registryPath returns the entries of the Path value in the registry key
// root\key, with environment variables such as %SystemRoot% expanded, or
// nothing when it cannot be read.
func registryPath(root syscall.Handle, key string) []string {
	value, err := host.RegistryString(root, key, "Path")
	if err != nil || value == "" {
		return nil
	}
	return filepath.SplitList(host.ExpandVariables(value))
}

// lookPath finds the program a step names, as exec.LookPath does but
// searching path rather than the process's own PATH, which lacks what
// searchPath adds. Windows has no execute permission, so, as Windows does,
// it tries the name with each extension in PATHEXT, such as .exe and .cmd,
// and the name as given when it has one of those already. A name containing
// a slash, a backslash, or a drive is a path and returned unchanged; exec
// resolves a relative one against the repository, the step's directory.
//
// Relative PATH entries, including empty ones, are skipped, as exec.LookPath
// does since Go 1.19: a step runs in the repository, and a file there must
// not be able to stand in for a command. That includes the current directory,
// which Windows's own command search tries first.
func lookPath(name, path string) (string, error) {
	if strings.ContainsAny(name, `/\:`) {
		return name, nil
	}
	var candidates []string
	extensions := PathExtensions()
	if slices.Contains(extensions, strings.ToLower(filepath.Ext(name))) {
		candidates = append(candidates, name)
	}
	for _, extension := range extensions {
		candidates = append(candidates, name+extension)
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, candidate := range candidates {
			// Not only regular files: the commands Windows installs for
			// Store apps, such as winget, are reparse points that only
			// Windows itself can open, and exec runs them as it would.
			candidate = filepath.Join(dir, candidate)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%q %w", name, ErrNotFound)
}

// PathExtensions returns the extensions Windows tries when a command is named
// without one, lowercase: those in PATHEXT, or Windows's default when it is
// not set.
func PathExtensions() []string {
	var extensions []string
	for extension := range strings.SplitSeq(strings.ToLower(os.Getenv("PATHEXT")), ";") {
		if strings.HasPrefix(extension, ".") && len(extension) > 1 {
			extensions = append(extensions, extension)
		}
	}
	if len(extensions) == 0 {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	return extensions
}

// documents returns the folder a step's creates path after
// config.DocumentsPrefix lies in. When home is the user's profile, that is
// their Documents folder wherever Windows keeps it, as OneDrive moves it into
// itself when it backs it up; PowerShell reads profiles from there and
// nowhere else. For any other home, such as a scratch one given with --home,
// it is the Documents folder in home, so a trial run never looks at the
// user's own.
func documents(home string) (string, error) {
	profile, err := host.Profile.Path()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(profile); err == nil {
		profile = resolved
	}
	if !trust.SamePath(profile, home) {
		return filepath.Join(home, "Documents"), nil
	}
	dir, err := host.Documents.Path()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the Documents folder Windows names, %q, is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	// Prepare checks home. A Documents folder moved out of it, which others
	// could write, would let them plant the file that makes a step look
	// done, so it must pass the same rules.
	if !trust.Within(home, dir) {
		if err := trust.CheckLocation(dir); err != nil {
			return "", fmt.Errorf("documents folder: %w", err)
		}
		if err := trust.CheckCreatable(dir); err != nil {
			return "", fmt.Errorf("documents folder: %w", err)
		}
	}
	return dir, nil
}
