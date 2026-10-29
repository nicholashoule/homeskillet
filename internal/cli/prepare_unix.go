//go:build linux || darwin

// Developer Mode, which only Windows has, for homeskillet prepare.

package cli

import (
	"context"
	"errors"
)

// developerSettings is never reached on Linux and macOS, where any user may
// make symlinks (see host.ProbeSymlinks).
func developerSettings() ([]string, error) {
	return nil, errors.New("developer mode is a Windows setting")
}

// windowsPowerShell is never reached on Linux and macOS.
func windowsPowerShell() (string, error) {
	return "", errors.New("windows PowerShell is part of Windows")
}

// openSettings is never reached on Linux and macOS.
func openSettings(context.Context, []string) error {
	return errors.New("developer mode is a Windows setting")
}
