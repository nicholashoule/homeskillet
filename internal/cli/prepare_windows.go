// Opening Windows's Settings at the page that turns on Developer Mode, for
// homeskillet prepare.

package cli

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/nicholashoule/homeskillet/internal/host"
)

// developerSettings returns the command that opens Settings at For
// developers: explorer.exe from the Windows directory, never one found on
// PATH, handing it the ms-settings: address.
func developerSettings() ([]string, error) {
	windows, err := host.Windows.Path()
	if err != nil {
		return nil, err
	}
	return []string{filepath.Join(windows, "explorer.exe"), "ms-settings:developers"}, nil
}

// windowsPowerShell returns Windows PowerShell in the Windows directory,
// which every Windows has, never one found on PATH.
func windowsPowerShell() (string, error) {
	windows, err := host.Windows.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(windows, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

// openSettings runs command, from developerSettings. explorer.exe exits with
// status 1 after opening an address it hands on, so only a failure to start
// it is an error.
func openSettings(ctx context.Context, command []string) error {
	err := exec.CommandContext(ctx, command[0], command[1:]...).Run() //nolint:gosec // G204: explorer.exe from the Windows directory, opening a fixed Settings page, after approval
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}
