//go:build linux || darwin

// Where Linux and macOS keep the system tools self update runs: curl, and a
// tool that prints a file's SHA-256.

package cli

import "errors"

// curlPath returns the system's curl: /usr/bin/curl, which macOS and most
// Linux distributions ship, or /bin/curl.
func curlPath() (string, error) {
	return systemProgram("curl", "/usr/bin/curl", "/bin/curl")
}

// hashCommand returns a command that prints the SHA-256 of file: sha256sum
// from coreutils on Linux, or shasum, which macOS ships, with -a 256. Each
// prints the digest first, then the name.
func hashCommand(file string) ([]string, error) {
	path, err := systemProgram("sha256sum", "/usr/bin/sha256sum", "/bin/sha256sum")
	if err == nil {
		return []string{path, "--", file}, nil
	}
	// Only an absent sha256sum falls through to shasum; one that is present
	// and refused by the ownership rules is reported as such.
	if !errors.Is(err, errNotInstalled) {
		return nil, err
	}
	path, err = systemProgram("sha256sum or shasum", "/usr/bin/shasum", "/bin/shasum")
	if err != nil {
		return nil, err
	}
	return []string{path, "-a", "256", "--", file}, nil
}
