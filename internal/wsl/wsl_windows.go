// Finding a distribution and running commands in it through wsl.exe.

package wsl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nicholashoule/homeskillet/internal/host"
)

// lxss is the registry key, under HKEY_CURRENT_USER, where WSL records each
// distribution, one subkey each, and which is the default.
const lxss = `Software\Microsoft\Windows\CurrentVersion\Lxss`

// Starting a distribution: how many tries, how long each may take, which
// covers starting WSL's virtual machine from cold, and the pause between.
const (
	startAttempts = 3
	startTimeout  = 2 * time.Minute
	startPause    = 3 * time.Second
)

// maxOutput bounds what Output keeps of a command's output. The commands it
// runs print a line or two.
const maxOutput = 1 << 20

// Runner runs commands in one distribution.
type Runner struct {
	program      string
	distribution string
}

// Open returns a Runner for the distribution called name, or for WSL's
// default distribution when name is empty. It fails, saying what to do, when
// WSL or the distribution is missing.
func Open(name string) (Runner, error) {
	windows, err := host.Windows.Path()
	if err != nil {
		return Runner{}, err
	}
	// wsl.exe from the system directory, never from PATH, where a file in the
	// current directory or a directory another program added could stand in.
	program := filepath.Join(windows, "System32", "wsl.exe")
	if _, err := os.Stat(program); err != nil {
		return Runner{}, fmt.Errorf("WSL is not installed (%s is missing); install it with wsl --install, then run this again", program)
	}
	distribution, err := find(name)
	if err != nil {
		return Runner{}, err
	}
	return Runner{program: program, distribution: distribution}, nil
}

// Distribution returns the name of the distribution r runs commands in.
func (r Runner) Distribution() string {
	return r.distribution
}

// find returns the distribution called name, ignoring case, as WSL does, or
// the default one when name is empty.
func find(name string) (string, error) {
	keys, err := host.RegistrySubkeys(syscall.HKEY_CURRENT_USER, lxss)
	if err != nil && !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return "", fmt.Errorf("reading WSL's distributions: %w", err)
	}
	fallback, _ := host.RegistryString(syscall.HKEY_CURRENT_USER, lxss, "DefaultDistribution")
	var names []string
	chosen := ""
	for _, key := range keys {
		found, err := host.RegistryString(syscall.HKEY_CURRENT_USER, lxss+`\`+key, "DistributionName")
		if err != nil || !ValidName(found) {
			continue
		}
		names = append(names, found)
		if name == "" && strings.EqualFold(key, fallback) || name != "" && strings.EqualFold(found, name) {
			chosen = found
		}
	}
	switch {
	case chosen != "":
		return chosen, nil
	case len(names) == 0:
		return "", errors.New("WSL has no Linux distribution; install one with wsl --install -d Ubuntu, then run this again")
	case name != "":
		return "", fmt.Errorf("WSL has no distribution called %s; it has %s", name, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("WSL has no default distribution; name one with --distribution: %s", strings.Join(names, ", "))
}

// command returns wsl.exe running args in the distribution, directly rather
// than through a shell, from the home directory of its default user. WSL's
// own messages come in UTF-8 rather than UTF-16, so they read as text.
func (r Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"--distribution", r.distribution, "--cd", "~", "--exec"}, args...)
	cmd := exec.CommandContext(ctx, r.program, full...) //nolint:gosec // G204: wsl.exe from the system directory; --exec passes args to the program as they are
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	return cmd
}

// Start makes sure the distribution is running and answers, by running a
// command that changes nothing. Only it is retried, never a command that
// could have done something the first time, so a slow or failed start costs
// a retry and nothing else.
func (r Runner) Start(ctx context.Context) error {
	var last error
	for attempt := range startAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(startPause):
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, startTimeout)
		_, err := r.Output(attemptCtx, nil, "true")
		cancel()
		if err == nil {
			return nil
		}
		last = err
	}
	return fmt.Errorf("WSL did not start %s: %w\n  running wsl --shutdown, then this again, usually helps", r.distribution, last)
}

// Output runs args with stdin as their input and returns their output. It
// fails with what they wrote to standard error.
func (r Runner) Output(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := r.command(ctx, args...)
	cmd.Stdin = stdin
	stdout, stderr := &bounded{}, &bounded{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return "", fmt.Errorf("%s in %s: %w", args[0], r.distribution, err)
		}
		return "", fmt.Errorf("%s in %s: %w: %s", args[0], r.distribution, err, message)
	}
	return stdout.String(), nil
}

// Run runs args with the given streams, for a command whose output a person
// reads and whose prompts they answer. Passed the console's own files, the
// command in the distribution gets a terminal, as it would run there.
func (r Runner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	cmd := r.command(ctx, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// bounded keeps the first maxOutput bytes written to it and drops the rest,
// so a command that prints far more than expected cannot exhaust memory.
type bounded struct{ bytes.Buffer }

func (b *bounded) Write(p []byte) (int, error) {
	if room := maxOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
