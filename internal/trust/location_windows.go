// The system-directory refusals on Windows: places homeskillet never acts on,
// such as C:\Windows, C:\Program Files, a drive's root, or C:\Users, checked
// before a home, repository, prefix, or data directory is read or written.

package trust

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nicholashoule/homeskillet/internal/host"
)

// windowsLocations are the directories CheckLocation refuses on Windows,
// found once. Each comes from the known folder Windows records for it, so a
// system installed in another directory or on another drive is still
// covered, and falls back to its usual place on the system drive when that
// lookup fails.
var windowsLocations = sync.OnceValues(func() (trees, parents []string) {
	drive := os.Getenv("SystemDrive")
	if len(drive) != 2 || drive[1] != ':' {
		drive = "C:"
	}
	find := func(folder host.Folder, fallback string) string {
		if path, err := folder.Path(); err == nil && filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return drive + fallback
	}
	users := find(host.UserProfiles, `\Users`)
	// The system's own directories, and the profiles every user shares:
	// Public, and Default, which new profiles are copied from.
	trees = []string{
		find(host.Windows, `\Windows`),
		find(host.ProgramFiles, `\Program Files`),
		find(host.ProgramFilesX86, `\Program Files (x86)`),
		find(host.ProgramData, `\ProgramData`),
		find(host.Public, `\Users\Public`),
		filepath.Join(users, "Default"),
	}
	// The directory holding every profile, refused exactly, like /home; the
	// profiles below it are not.
	return trees, []string{users}
})

// driveTrees are directories Windows keeps at the root of every drive.
var driveTrees = []string{"$Recycle.Bin", "System Volume Information"}

// CheckLocation refuses a path that homeskillet has no reason to act on:
// anything other than a clean absolute path; a device path, which starts
// with \\?\ or \\.\ and skips the normalization these comparisons rely on;
// a WSL distribution's files, whose ownership Windows cannot report; the
// root of a drive or network share, which holds many users' files; the
// directory holding every user's profile; and a system directory or anything
// below one (see windowsLocations). Callers apply it to the canonical home
// directory, repository, and installation directories before reading or
// changing anything there.
//
// Names are compared without regard to case, as Windows compares them.
func CheckLocation(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s is not a clean absolute path", printable(path))
	}
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return fmt.Errorf("refusing to act on %s, a device path; name it as a drive path such as C:\\Users\\you instead", printable(path))
	}
	// Windows reports no owner or access list for a WSL distribution's files,
	// so the ownership rules could never pass there; say what to do instead
	// of failing on the first file.
	if wslPath(path) {
		return fmt.Errorf("refusing to act on %s, which is inside WSL; run homeskillet inside WSL for files there, "+
			"and keep what this one uses under your Windows profile", printable(path))
	}
	volume := filepath.VolumeName(path)
	if path == volume || path == volume+`\` {
		return fmt.Errorf("refusing to act on %s, the root of a drive or share, which holds other users' directories, not one user's files", printable(path))
	}
	trees, parents := windowsLocations()
	for _, parent := range parents {
		if strings.EqualFold(path, parent) {
			return fmt.Errorf("refusing to act on %s, which holds other users' directories, not one user's files", printable(path))
		}
	}
	for _, name := range driveTrees {
		trees = append(trees, volume+`\`+name)
	}
	for _, tree := range trees {
		if within(tree, path) {
			return fmt.Errorf("refusing to act on %s, which is inside the system directory %s", printable(path), tree)
		}
	}
	return nil
}

// wslPath reports whether path is in a WSL distribution's file system, which
// Windows shares as \\wsl.localhost\<distribution> and \\wsl$\<distribution>.
func wslPath(path string) bool {
	for _, share := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		if len(path) >= len(share) && strings.EqualFold(path[:len(share)], share) {
			return true
		}
	}
	return false
}

// within reports whether path is tree or below it, ignoring case.
func within(tree, path string) bool {
	if len(path) < len(tree) || !strings.EqualFold(path[:len(tree)], tree) {
		return false
	}
	return len(path) == len(tree) || path[len(tree)] == '\\'
}
