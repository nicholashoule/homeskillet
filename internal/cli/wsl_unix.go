//go:build linux || darwin

// The wsl command on Linux and macOS, where there is no WSL to reach into.

package cli

import (
	"context"
	"errors"
	"io"
)

// runWSL refuses: wsl reaches into WSL from Windows, and inside WSL
// homeskillet acts on its own home directly.
func runWSL(context.Context, invocation, io.Reader, io.Writer, io.Writer) error {
	return errors.New("wsl runs on Windows, where it reaches into WSL; inside WSL, run homeskillet itself")
}
