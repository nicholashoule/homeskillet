// Package wsl runs commands in a WSL distribution from Windows, for the wsl
// command. It finds distributions where WSL records them, in the registry,
// rather than parsing wsl.exe's output; starts one with a command that
// changes nothing, retried, since WSL can take a while to answer at first;
// and runs every command with --exec, directly, so that no shell in the
// distribution reads its arguments. The rest of this file builds on every
// system, so its rules are testable anywhere.
package wsl

import (
	"fmt"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
)

// Arch returns the Go architecture for what uname -m prints in a
// distribution, so the Linux build installed there matches its hardware.
func Arch(machine string) (string, error) {
	switch strings.TrimSpace(machine) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	}
	return "", fmt.Errorf("the distribution runs on %q, for which there is no homeskillet build", strings.TrimSpace(machine))
}

// ValidName reports whether name can name a distribution on wsl.exe's
// command line: printable, and not an option.
func ValidName(name string) bool {
	return name != "" && config.Printable(name) && !strings.HasPrefix(name, "-")
}
