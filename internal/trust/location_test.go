//go:build linux || darwin

// Tests of the system-directory refusals.

package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckLocation(t *testing.T) {
	refused := []string{
		// Directories that hold homes, and HOME=/ in particular.
		"/", "/home", "/Users", "/var/home",
		// System trees and anything below them, whatever the case.
		"/etc", "/etc/profile.d", "/usr", "/usr/local", "/usr/local/bin", "/bin",
		"/var/lib/postgresql", "/private/etc", "/System/Library", "/system", "/LIBRARY/x",
		// Not clean absolute paths.
		"", "relative", "./home", "/home/user/../../etc", "/home/user/",
	}
	for _, path := range refused {
		if err := CheckLocation(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	allowed := []string{
		"/root", "/home/user", "/Users/user", "/var/home/user", "/var/root",
		"/tmp/scratch", "/var/tmp/x", "/private/var/folders/ab/xyz/T", "/workspace/dotfiles",
		"/opt/dotfiles", "/srv/user", "/usrlocal", "/etcetera", "/home2/user",
	}
	for _, path := range allowed {
		if err := CheckLocation(path); err != nil {
			t.Errorf("refused %q: %v", path, err)
		}
	}
	// Whatever the platform's temporary directory is, tests and scratch
	// homes must be able to use it.
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckLocation(temporary); err != nil {
		t.Fatalf("temporary directory refused: %v", err)
	}
}

func TestPathChecksRequireAbsolutePaths(t *testing.T) {
	for name, check := range map[string]func() error{
		"CheckDirectory":  func() error { return CheckDirectory("relative") },
		"CheckRepository": func() error { return CheckRepository("") },
		"CheckTree":       func() error { return CheckTree(".") },
		"CheckPath":       func() error { return CheckPath("relative", "relative/file") },
		"CheckPath escape": func() error {
			return CheckPath("/tmp/root", "/tmp/elsewhere")
		},
	} {
		if err := check(); err == nil {
			t.Errorf("%s accepted a relative or escaping path", name)
		}
	}
}
