// The wsl command on Windows: installing homeskillet's Linux build in a WSL
// distribution, and running status, link, unlink, and setup there, on a copy
// of a Windows clone or on a clone already in the distribution.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/mirror"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
	"github.com/nicholashoule/homeskillet/internal/version"
	"github.com/nicholashoule/homeskillet/internal/wsl"
)

// releases is where published versions' binaries are downloaded from.
const releases = "https://github.com/nicholashoule/homeskillet/releases/download/"

// Bounds on what wsl install reads: a Linux build, and the SHA256SUMS file
// that lists the release's binaries.
const (
	maxProgram = 64 << 20
	maxSums    = 64 << 10
)

// errStopped stops a copy being sent once the distribution has stopped
// reading it.
var errStopped = errors.New("the distribution stopped reading the copy")

// runWSL runs wsl: install, or one of the other commands in the
// distribution with homeskillet's Linux build, which must be this version so
// that the two read a copy alike. A repository written / or ~ first is
// already in the distribution; any other is a Windows clone, copied there
// first (see copyRepository).
func runWSL(ctx context.Context, inv invocation, input io.Reader, output, diagnostics io.Writer) error {
	runner, err := wsl.Open(inv.wsl.distribution)
	if err != nil {
		return err
	}
	if err := runner.Start(ctx); err != nil {
		return err
	}
	if inv.sub == "install" {
		return installWSL(ctx, runner, inv, output, diagnostics)
	}
	command := linuxCommand(inv.home)
	if err := checkLinuxBuild(ctx, runner, command); err != nil {
		return err
	}
	repo := inDistribution(inv.repo)
	if !linuxPath(inv.repo) {
		copied, cleanup, err := copyRepository(ctx, runner, command, inv, diagnostics)
		if err != nil {
			return err
		}
		defer cleanup()
		repo = copied
	}
	args := append([]string{command, inv.sub, repo}, inv.wsl.forward...)
	return ranInWSL(runner.Run(ctx, input, output, diagnostics, args...))
}

// ranInWSL returns the error wsl exits with after running a command in the
// distribution. A command that ran and failed has said why, as wsl.exe has
// when it could not run it, so only the exit status is left to report.
func ranInWSL(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return reportedError{err}
	}
	return err
}

// checkLinuxBuild makes sure the distribution has homeskillet at command, and
// that it is this version.
func checkLinuxBuild(ctx context.Context, runner wsl.Runner, command string) error {
	output, err := runner.Output(ctx, nil, command, "version")
	if err != nil {
		return fmt.Errorf("homeskillet does not run in %s (%w)\n  install it there with: homeskillet wsl install", runner.Distribution(), err)
	}
	if have, want := versionOf(output), version.Current().Version; have != want {
		return fmt.Errorf("%s has homeskillet %s, and this is %s; run homeskillet wsl install so the two match", runner.Distribution(), have, want)
	}
	return nil
}

// copyRepository copies the Windows clone inv names into the distribution,
// after the checks every other command makes of a repository, and returns
// where the copy is there, and what removes it afterward if it is temporary.
//
// link, unlink, and setup bring the copy in the data directory up to date
// first. status and dry runs change nothing: they use that copy as it is,
// saying so when it differs from the clone, since links lead into it, or a
// temporary copy when there is none yet.
func copyRepository(ctx context.Context, runner wsl.Runner, command string, inv invocation, diagnostics io.Writer) (string, func(), error) {
	none := func() {}
	repo, err := canonicalDirectory(inv.repo)
	// PowerShell can turn ~/dotfiles into a Windows path before homeskillet
	// sees it, so a clone meant to be in the distribution can arrive as one
	// that is not on Windows.
	if errors.Is(err, fs.ErrNotExist) {
		return "", none, fmt.Errorf("repository: %w\n  to name a clone inside %s, write it in quotes as '~/dotfiles', or in full as /home/you/dotfiles", err, runner.Distribution())
	}
	if err != nil {
		return "", none, fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckLocation(repo); err != nil {
		return "", none, fmt.Errorf("repository: %w", err)
	}
	if err := trust.CheckRepository(repo); err != nil {
		return "", none, err
	}
	if _, err := config.Load(repo); err != nil {
		return "", none, err
	}
	send := func(mode string) (string, error) {
		args := append([]string{command, mirrorCommand, mode, mirrorName(repo)}, mirrorOptions(inv)...)
		return sendCopy(ctx, runner, repo, args)
	}
	if inv.sub != "status" && !inv.dryRun {
		copied, err := send("update")
		return copied, none, err
	}
	compared, err := send("compare")
	if err != nil {
		return "", none, err
	}
	if compared != "none" {
		return existingCopy(compared, repo, runner.Distribution(), diagnostics)
	}
	copied, err := send("temporary")
	if err != nil {
		return "", none, err
	}
	// Only the directory runMirror made, never a path that merely came back.
	dir := path.Dir(copied)
	if !path.IsAbs(dir) || !strings.HasPrefix(path.Base(dir), "homeskillet-mirror-") {
		return "", none, fmt.Errorf("the temporary copy is at %q, not where it should be", copied)
	}
	return copied, func() { _, _ = runner.Output(context.WithoutCancel(ctx), nil, "rm", "-rf", "--", dir) }, nil
}

// existingCopy reads what comparing the clone with its copy found, "added
// changed removed path", says when the two differ, and returns the copy.
func existingCopy(compared, repo, distribution string, diagnostics io.Writer) (string, func(), error) {
	fields := strings.SplitN(compared, " ", 4)
	counts := make([]int, 3)
	for i := range counts {
		n, err := strconv.Atoi(fields[min(i, len(fields)-1)])
		if err != nil || len(fields) != 4 {
			return "", nil, fmt.Errorf("comparing the copy in %s gave %q", distribution, compared)
		}
		counts[i] = n
	}
	if counts[0]+counts[1]+counts[2] > 0 {
		if _, err := fmt.Fprintf(diagnostics, "homeskillet: the copy of %s in %s differs from it (%d added, %d changed, %d removed); homeskillet wsl link updates it\n",
			displayPath(repo), distribution, counts[0], counts[1], counts[2]); err != nil {
			return "", nil, err
		}
	}
	return fields[3], func() {}, nil
}

// mirrorOptions returns the options of inv that decide where the copy goes,
// to pass on to mirrorCommand.
func mirrorOptions(inv invocation) []string {
	var options []string
	if inv.home != "" {
		options = append(options, "--home="+inv.home)
	}
	if inv.data != "" {
		options = append(options, "--data-dir="+inv.data)
	}
	if inv.root {
		options = append(options, "--allow-root")
	}
	return options
}

// sendCopy runs args in the distribution with the copy of repo (see
// mirror.Send) as their input, and returns what they print, trimmed. A
// failure to send says why before the receiver's error, which would only
// say the copy stopped short.
func sendCopy(ctx context.Context, runner wsl.Runner, repo string, args []string) (string, error) {
	reader, writer := io.Pipe()
	sent := make(chan error, 1)
	go func() {
		err := mirror.Send(ctx, writer, repo)
		_ = writer.CloseWithError(err) // always nil
		sent <- err
	}()
	output, err := runner.Output(ctx, reader, args...)
	// A receiver that stopped early reads no more, and Send must not wait.
	_ = reader.CloseWithError(errStopped) // always nil
	sendErr := <-sent
	if sendErr != nil && !errors.Is(sendErr, errStopped) {
		return "", sendErr
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// installWSL installs homeskillet's Linux build, of this version, in the
// distribution: the build --from names, as make cross makes it, or else this
// version's release, downloaded and checked against its SHA256SUMS in the
// distribution. The build is copied into a private temporary directory
// there, checked to be whole and to run as this version, and then installs
// itself with self install, as on any Linux, with the options given.
func installWSL(ctx context.Context, runner wsl.Runner, inv invocation, output, diagnostics io.Writer) error {
	machine, err := runner.Output(ctx, nil, "uname", "-m")
	if err != nil {
		return err
	}
	arch, err := wsl.Arch(machine)
	if err != nil {
		return err
	}
	current := version.Current().Version
	var program []byte
	sum := ""
	if inv.wsl.from != "" {
		program, err = readLinuxBuild(inv.wsl.from)
	} else {
		program, sum, err = downloadRelease(ctx, current, arch)
	}
	if err != nil {
		return err
	}
	dir, err := runner.Output(ctx, nil, "mktemp", "-d")
	if dir = strings.TrimSpace(dir); err == nil && (!path.IsAbs(dir) || !config.Printable(dir) || strings.ContainsRune(dir, ' ')) {
		err = fmt.Errorf("mktemp in %s gave %q", runner.Distribution(), dir)
	}
	if err != nil {
		return err
	}
	defer runner.Output(context.WithoutCancel(ctx), nil, "rm", "-rf", "--", dir) //nolint:errcheck // a temporary directory; failing to remove it changes nothing
	file := path.Join(dir, install.Binary)
	if err := placeBuild(ctx, runner, program, sum, file); err != nil {
		return err
	}
	versionOutput, err := runner.Output(ctx, nil, file, "version")
	if err != nil {
		return fmt.Errorf("the Linux build does not run in %s: %w", runner.Distribution(), err)
	}
	if have := versionOf(versionOutput); have != current {
		return fmt.Errorf("the Linux build is %s, and this is %s; install a Linux build of the same version", have, current)
	}
	args := append([]string{file, "self", "install"}, inv.wsl.forward...)
	return ranInWSL(runner.Run(ctx, nil, output, diagnostics, args...))
}

// placeBuild writes program to file in the distribution and makes it
// executable, after checking that it arrived whole: its size, and, for a
// download, its SHA-256 against sum, computed there.
func placeBuild(ctx context.Context, runner wsl.Runner, program []byte, sum, file string) error {
	if _, err := runner.Output(ctx, bytes.NewReader(program), "dd", "of="+file, "bs=1M", "status=none"); err != nil {
		return err
	}
	size, err := runner.Output(ctx, nil, "stat", "--format=%s", "--", file)
	if err != nil {
		return err
	}
	if strings.TrimSpace(size) != strconv.Itoa(len(program)) {
		return fmt.Errorf("the Linux build arrived in %s incomplete; nothing was installed", runner.Distribution())
	}
	if sum != "" {
		hashed, err := runner.Output(ctx, nil, "sha256sum", "--", file)
		if err != nil {
			return err
		}
		if got, _, _ := strings.Cut(strings.TrimSpace(hashed), " "); !strings.EqualFold(got, sum) {
			return errors.New("the downloaded Linux build does not match the release's SHA256SUMS; nothing was installed")
		}
	}
	_, err = runner.Output(ctx, nil, "chmod", "0700", "--", file)
	return err
}

// readLinuxBuild reads the Linux build at name, for wsl install --from. It
// must pass the rules for a program homeskillet installs (see
// trust.CheckProgram) and be a Linux executable.
func readLinuxBuild(name string) ([]byte, error) {
	name, err := expandHome(name)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(name) //nolint:gosec // G304: the build the user named, checked by trust.CheckProgram before it is read
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // opened only to read; closing it cannot lose anything
	if err := trust.CheckProgram(file); err != nil {
		return nil, err
	}
	program, err := safefile.ReadAll(file, maxProgram)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if !bytes.HasPrefix(program, []byte("\x7fELF")) {
		return nil, fmt.Errorf("%s is not a Linux build; make cross builds one as bin\\homeskillet-linux-amd64", name)
	}
	return program, nil
}

// downloadRelease downloads this version's Linux build for arch, and the
// checksum SHA256SUMS lists for it, with curl, which Windows includes. Only a
// release has binaries to download.
func downloadRelease(ctx context.Context, current, arch string) ([]byte, string, error) {
	name := "homeskillet-linux-" + arch
	if !release(current) {
		return nil, "", fmt.Errorf("this is a development build, %s, with no release to download; build one for Linux with make cross, then:\n"+
			"  homeskillet wsl install --from bin\\%s", current, name)
	}
	windows, err := host.Windows.Path()
	if err != nil {
		return nil, "", err
	}
	curl := filepath.Join(windows, "System32", "curl.exe")
	dir, err := os.MkdirTemp("", "homeskillet-wsl-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory; failing to remove it changes nothing
	for _, file := range []string{name, "SHA256SUMS"} {
		url := releases + current + "/" + file
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, curl, "--fail", "--silent", "--show-error", "--location", //nolint:gosec // G204: curl from the system directory, fetching this version's release over HTTPS
			"--proto", "=https", "--tlsv1.2", "--max-time", "600", "--output", filepath.Join(dir, file), url)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, "", fmt.Errorf("downloading %s: %w: %s", url, err, strings.TrimSpace(stderr.String()))
		}
	}
	program, err := readFile(filepath.Join(dir, name), maxProgram)
	if err != nil {
		return nil, "", err
	}
	sums, err := readFile(filepath.Join(dir, "SHA256SUMS"), maxSums)
	if err != nil {
		return nil, "", err
	}
	sum := checksum(string(sums), name)
	if sum == "" {
		return nil, "", fmt.Errorf("the SHA256SUMS of %s lists no %s", current, name)
	}
	return program, sum, nil
}

// readFile reads at most limit bytes of the file at name.
func readFile(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name) //nolint:gosec // G304: a file just downloaded into a private temporary directory
	if err != nil {
		return nil, err
	}
	data, err := safefile.ReadAll(file, limit)
	return data, errors.Join(err, file.Close())
}

// checksum returns the SHA-256 that sums, in the format sha256sum writes,
// lists for the file name, or "" when it lists none.
func checksum(sums, name string) string {
	for line := range strings.Lines(sums) {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 &&
			!strings.ContainsFunc(fields[0], func(r rune) bool { return !strings.ContainsRune("0123456789abcdefABCDEF", r) }) {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}
