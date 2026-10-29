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

// UpdateCheckVariable turns off, in the distribution, the note homeskillet
// shows when a newer release is available. The commands wsl runs there need
// the Linux build to stay the version of the Windows build that runs them,
// so its advice to update there would break the next wsl command.
const UpdateCheckVariable = "HOMESKILLET_UPDATE_CHECK"

// environment returns environ for wsl.exe: WSL's messages in UTF-8 rather
// than UTF-16, so they read as text, and UpdateCheckVariable set to off and
// named in WSLENV, the list of variables WSL passes from Windows into the
// distribution, keeping any names already listed there.
func environment(environ []string) []string {
	shared := ""
	result := make([]string, 0, len(environ)+3)
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		switch {
		// Windows compares variable names without regard to case.
		case strings.EqualFold(name, "WSLENV"):
			shared = value
		case strings.EqualFold(name, UpdateCheckVariable), strings.EqualFold(name, "WSL_UTF8"):
		default:
			result = append(result, entry)
		}
	}
	listed := false
	for item := range strings.SplitSeq(shared, ":") {
		listed = listed || strings.EqualFold(strings.SplitN(item, "/", 2)[0], UpdateCheckVariable)
	}
	switch {
	case shared == "":
		shared = UpdateCheckVariable
	case !listed:
		shared += ":" + UpdateCheckVariable
	}
	return append(result, "WSL_UTF8=1", UpdateCheckVariable+"=off", "WSLENV="+shared)
}
