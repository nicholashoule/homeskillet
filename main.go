//go:build linux || darwin

// Command homeskillet links dotfiles from a configuration repository into a
// home directory and runs the setup steps the repository declares, such as
// installing oh-my-posh. It supports only Linux and macOS: the build
// constraint above enforces that, because the repository ownership checks read
// Unix file metadata. The implementation lives in the packages under internal/.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nicholashoule/homeskillet/internal/cli"
)

func main() {
	// Ctrl-C reaches homeskillet and any running setup step together, as the
	// terminal signals the whole foreground process group, so no signal
	// handling is needed here; the context lets callers such as tests impose
	// their own cancellation.
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "homeskillet:", err)
		os.Exit(1)
	}
}
