// Where Windows keeps the system tools self update and wsl install run: curl,
// and certutil, which prints a file's SHA-256.

package cli

import (
	"path/filepath"

	"github.com/nicholashoule/homeskillet/internal/host"
)

// curlPath returns curl.exe in the Windows system directory, which Windows 10
// 1803 and later include.
func curlPath() (string, error) {
	windows, err := host.Windows.Path()
	if err != nil {
		return "", err
	}
	return systemProgram("curl", filepath.Join(windows, "System32", "curl.exe"))
}

// hashCommand returns a command that prints the SHA-256 of file: certutil
// -hashfile, from the Windows system directory, which prints a heading, the
// digest, and a trailer.
func hashCommand(file string) ([]string, error) {
	windows, err := host.Windows.Path()
	if err != nil {
		return nil, err
	}
	path, err := systemProgram("certutil", filepath.Join(windows, "System32", "certutil.exe"))
	if err != nil {
		return nil, err
	}
	return []string{path, "-hashfile", file, "SHA256"}, nil
}
