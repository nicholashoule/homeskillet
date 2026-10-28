// What a command is on Windows, for shadowed: a file whose extension makes it
// one, named without that extension, so git.cmd early on PATH runs in place
// of git.exe later on it.

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/setup"
)

// userBins is empty: nothing on Windows puts a directory in the home at the
// front of PATH as Unix login scripts do, so only directories already on
// PATH count.
var userBins []string

// extensions returns the extensions that make a file a command: those in
// PATHEXT, which every Windows shell tries, and .ps1, whose scripts
// PowerShell runs from PATH as well.
func extensions() []string {
	return append(setup.PathExtensions(), ".ps1")
}

// commandName returns the name a shell runs the file at path by: its name
// without the extension that makes it a command.
func commandName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// candidates returns the files in dir a shell's search for name would run,
// one for each extension.
func candidates(dir, name string) []string {
	var paths []string
	for _, extension := range extensions() {
		paths = append(paths, filepath.Join(dir, name+extension))
	}
	return paths
}

// executable reports whether info, the file at path, is a command: not a
// directory, and with an extension that makes it one. Windows has no execute
// permission, and the commands it installs for Store apps, such as winget,
// are reparse points rather than regular files.
func executable(path string, info os.FileInfo) bool {
	return !info.IsDir() && slices.Contains(extensions(), strings.ToLower(filepath.Ext(path)))
}
