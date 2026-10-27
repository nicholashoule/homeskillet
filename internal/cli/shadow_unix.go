//go:build linux || darwin

// What a command is on Linux and macOS, for shadowed: an executable file,
// named exactly as it is typed.

package cli

import (
	"os"
	"path/filepath"
)

// userBins are the directories in home that login scripts on common systems,
// such as Fedora's .bashrc and Ubuntu's .profile, put at the front of PATH
// once they exist, which linking into them may be what makes them do.
var userBins = []string{filepath.Join(".local", "bin"), "bin"}

// commandName returns the name a shell runs the file at path by.
func commandName(path string) string {
	return filepath.Base(path)
}

// candidates returns the files in dir a shell's search for name would run.
func candidates(dir, name string) []string {
	return []string{filepath.Join(dir, name)}
}

// executable reports whether info, the file at path, is a regular file that
// someone may execute, as a shell's command search requires.
func executable(_ string, info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
