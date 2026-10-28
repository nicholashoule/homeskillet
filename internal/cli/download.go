//go:build linux || darwin || windows

// Downloading a published release of homeskillet: the binary for a platform
// and the SHA256SUMS that lists it, for self update and wsl install.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nicholashoule/homeskillet/internal/install"
	"github.com/nicholashoule/homeskillet/internal/safefile"
	"github.com/nicholashoule/homeskillet/internal/trust"
)

// project is the GitHub repository that publishes homeskillet, the only
// place self update and wsl install download from.
const project = "https://github.com/nicholashoule/homeskillet"

// releases is where published versions' binaries are downloaded from, and
// latestRelease a page that redirects to the newest release's page, which
// names its version (see latestVersion).
const (
	releases      = project + "/releases/download/"
	latestRelease = project + "/releases/latest"
	releaseTags   = project + "/releases/tag/"
	// releasesAPI lists the published releases as JSON, newest first, for
	// self releases; it needs no token for a public repository.
	releasesAPI = "https://api.github.com/repos/nicholashoule/homeskillet/releases?per_page=100"
)

// Bounds on what is read of a download: a build of homeskillet, the
// SHA256SUMS file that lists a release's binaries, and the list of releases.
const (
	maxProgram  = 64 << 20
	maxSums     = 64 << 10
	maxReleases = 4 << 20
)

// A downloader fetches release files over HTTPS. The system's curl does it
// (see curlDownloader); tests substitute one that serves files of their own.
type downloader interface {
	// get downloads url to file.
	get(ctx context.Context, url, file string) error
	// redirect returns where url redirects to, without following it, or ""
	// when it does not redirect.
	redirect(ctx context.Context, url string) (string, error)
}

// newDownloader returns the downloader self update and wsl install use.
var newDownloader = func() (downloader, error) {
	path, err := curlPath()
	if err != nil {
		return nil, err
	}
	return curl{path: path}, nil
}

// curl downloads with the system's curl, which Windows and macOS include and
// nearly every Linux has, rather than net/http, which would add a few hundred
// KiB to the binary (docs/development.md). It is the system's own copy, at a
// fixed path checked by trust.CheckProgram, never one found on PATH: what
// curl fetches is then run, so a curl another user could replace would let
// them choose the program.
type curl struct{ path string }

// options are passed on every request: HTTPS and TLS 1.2 or later only, so
// no redirect can lead to plain HTTP, an error status fails the request, and
// a stalled transfer stops instead of hanging a terminal.
var curlOptions = []string{"--fail", "--silent", "--show-error", "--location", "--proto", "=https", "--proto-redir", "=https", "--tlsv1.2", "--max-time", "600"}

func (c curl) get(ctx context.Context, url, file string) error {
	args := append(append([]string{}, curlOptions...), "--output", file, url)
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, c.path, args...) //nolint:gosec // G204: the system's curl, checked by curlPath, fetching a fixed URL over HTTPS
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return describeFailure("downloading "+url, err, stderr.String())
	}
	return nil
}

func (c curl) redirect(ctx context.Context, url string) (string, error) {
	// --head asks for headers only, and %{redirect_url} is where the
	// response points without following it, so the version is read from
	// one small response.
	args := append(append([]string{}, curlOptions...), "--head", "--output", os.DevNull, "--write-out", "%{redirect_url}", url)
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, c.path, args...) //nolint:gosec // G204: the system's curl, checked by curlPath, fetching a fixed URL over HTTPS
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", describeFailure("asking "+url+" for the latest release", err, stderr.String())
	}
	return strings.TrimSpace(string(output)), nil
}

// describeFailure turns a failed curl run into an error that says, on its
// first line, what was being done and what went wrong, and on an indented
// second line what to do about it, ending with the assurance that nothing was
// changed. curl's exit status names the kind of failure, and for an HTTP
// error its message carries the status code; anything unrecognized keeps
// curl's own words.
func describeFailure(doing string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	what, advice := "", "Try again later."
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		what = err.Error()
	} else {
		switch exit.ExitCode() {
		case 6:
			what, advice = "github.com could not be found", "There is no network, or no DNS; check the connection."
		case 7:
			what, advice = "github.com could not be reached", "Check the connection, or a proxy or firewall."
		case 28:
			what, advice = "timed out", "The network may be slow or down."
		case 35, 51, 58, 60, 77:
			what, advice = "a secure connection could not be made ("+stderr+")", "The download could not be trusted, so it was not used."
		case 22:
			status := strings.TrimSpace(stderr[strings.LastIndex(stderr, ":")+1:])
			what = "GitHub answered " + status
			switch status {
			case "404":
				what = "GitHub has nothing there (404)"
				advice = "Either no such release is published, or the repository is private and its releases need a sign-in homeskillet does not have. " +
					"Download the release and run its self install instead (docs/install.md)."
			case "403", "429":
				advice = "That is usually GitHub's rate limit for anonymous requests; try again in a few minutes."
			}
		default:
			what = stderr
		}
	}
	return fmt.Errorf("%s: %s\n  %s Nothing was changed", doing, what, advice)
}

// systemProgram returns the first of candidates, each an absolute path, that
// exists, after checking that it is a regular file that only its owner, the
// user or root, could change (see trust.CheckProgram). The tools found this
// way fetch and check the program self update then runs, so one that another
// user could replace is refused like a program homeskillet copies.
func systemProgram(what string, candidates ...string) (string, error) {
	for _, candidate := range candidates {
		file, err := os.Open(candidate) //nolint:gosec // G304: a fixed system path
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		err = trust.CheckProgram(file)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%s is not installed at %s, and self update needs it; install it, or download the release yourself (docs/install.md)", what, strings.Join(candidates, " or "))
}

// releaseFile is the name of version v's binary for platform os/arch in its
// release, as make cross and the release workflow name it.
func releaseFile(os, arch string) string {
	name := install.Binary + "-" + os + "-" + arch
	if os == "windows" {
		name += ".exe"
	}
	return name
}

// latestVersion returns the newest published release's version, which the
// latest release page redirects to, such as v0.2.0.
func latestVersion(ctx context.Context, from downloader) (string, error) {
	target, err := from.redirect(ctx, latestRelease)
	if err != nil {
		return "", err
	}
	v, ok := strings.CutPrefix(target, releaseTags)
	if !ok || !release(v) {
		return "", fmt.Errorf("%s names no published release (it leads to %q); give a version, such as v0.2.0", latestRelease, target)
	}
	return v, nil
}

// downloadRelease downloads version v's release file name into dir, with the
// SHA256SUMS the release publishes, and returns the SHA-256 that lists for
// it. Only a release has files to download (see release).
func downloadRelease(ctx context.Context, from downloader, v, name, dir string) (sum string, err error) {
	for _, file := range []string{name, "SHA256SUMS"} {
		if err := from.get(ctx, releases+v+"/"+file, filepath.Join(dir, file)); err != nil {
			return "", err
		}
	}
	sums, err := readFile(filepath.Join(dir, "SHA256SUMS"), maxSums)
	if err != nil {
		return "", err
	}
	if sum = checksum(string(sums), name); sum == "" {
		return "", fmt.Errorf("the SHA256SUMS of %s lists no %s", v, name)
	}
	return sum, nil
}

// verifyDownload checks that the file at name, just downloaded, has the
// SHA-256 sum, computed by the system's hashing tool (see hashCommand), and
// bounds its size first, so a wrong download is never run. The hash is
// computed by a system tool rather than crypto/sha256, which would add about
// 150 KiB to the binary (docs/development.md).
func verifyDownload(ctx context.Context, name, sum string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if info.Size() > maxProgram {
		return fmt.Errorf("%s is %d bytes, larger than any build of homeskillet; nothing was installed", filepath.Base(name), info.Size())
	}
	args, err := hashCommand(name)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // G204: the system's hashing tool, checked by hashCommand, over a file in a private temporary directory
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	if got := hexDigest(string(output)); !strings.EqualFold(got, sum) {
		return errors.New("the downloaded build does not match the release's SHA256SUMS; nothing was installed")
	}
	return nil
}

// hexDigest returns the first 64-digit hexadecimal number in output, which is
// how sha256sum, shasum, and certutil each print a SHA-256, or "".
func hexDigest(output string) string {
	for field := range strings.FieldsSeq(output) {
		if isHex(field) && len(field) == 64 {
			return strings.ToLower(field)
		}
	}
	// certutil before Windows 10 1709 writes the digest as byte pairs
	// separated by spaces, on one line.
	for line := range strings.Lines(output) {
		if joined := strings.ReplaceAll(strings.TrimSpace(line), " ", ""); len(joined) == 64 && isHex(joined) {
			return strings.ToLower(joined)
		}
	}
	return ""
}

// isHex reports whether s is one or more hexadecimal digits.
func isHex(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return !strings.ContainsRune("0123456789abcdefABCDEF", r) })
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
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 && isHex(fields[0]) {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

// release describes one published release, as the GitHub API lists it.
type releaseInfo struct {
	Version    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// listReleases returns the published releases, newest first, leaving out
// drafts and anything that is not a version with a leading v, such as a tag
// made by hand.
func listReleases(ctx context.Context, from downloader) ([]releaseInfo, error) {
	dir, err := os.MkdirTemp("", "homeskillet-releases-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory; failing to remove it changes nothing
	file := filepath.Join(dir, "releases.json")
	if err := from.get(ctx, releasesAPI, file); err != nil {
		return nil, err
	}
	data, err := readFile(file, maxReleases)
	if err != nil {
		return nil, err
	}
	var listed []releaseInfo
	if err := json.Unmarshal(data, &listed); err != nil {
		return nil, fmt.Errorf("reading the list of releases: %w", err)
	}
	var published []releaseInfo
	for _, r := range listed {
		if !r.Draft && release(r.Version) {
			published = append(published, r)
		}
	}
	return published, nil
}
