//go:build linux || darwin || windows

// Tests of self update and self releases: reading SHA256SUMS and the digests
// system tools print, finding the newest release, the commands' arguments,
// and, on Linux and macOS, an update with a stand-in release served by a fake
// downloader.

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fakeDownloader serves files from memory in place of curl.
type fakeDownloader struct {
	files  map[string][]byte // by URL
	target string            // what latestRelease redirects to
}

func (f fakeDownloader) get(_ context.Context, url, file string) error {
	data, ok := f.files[url]
	if !ok {
		return fmt.Errorf("downloading %s: 404", url)
	}
	return os.WriteFile(file, data, 0o600)
}

func (f fakeDownloader) redirect(context.Context, string) (string, error) {
	return f.target, nil
}

// useDownloader makes self update and self releases use from for the test.
func useDownloader(t *testing.T, from downloader) {
	t.Helper()
	saved := newDownloader
	newDownloader = func() (downloader, error) { return from, nil }
	t.Cleanup(func() { newDownloader = saved })
}

func TestChecksumAndDigest(t *testing.T) {
	sums := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  homeskillet-linux-amd64\n" +
		"ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789 *homeskillet-windows-amd64.exe\n" +
		"deadbeef  homeskillet-darwin-arm64\n"
	for _, test := range []struct{ name, want string }{
		{"homeskillet-linux-amd64", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"homeskillet-windows-amd64.exe", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"},
		{"homeskillet-darwin-arm64", ""},
		{"homeskillet-darwin-amd64", ""},
	} {
		if got := checksum(sums, test.name); got != test.want {
			t.Errorf("checksum(%s) = %q, want %q", test.name, got, test.want)
		}
	}
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, output := range []string{
		digest + "  /tmp/x/homeskillet-linux-amd64\n",
		strings.ToUpper(digest) + " *homeskillet-darwin-arm64\n",
		"SHA256 hash of C:\\x\\homeskillet.exe:\r\n" + digest + "\r\nCertUtil: -hashfile command completed successfully.\r\n",
		"SHA256 hash of file:\r\n01 23 45 67 89 ab cd ef 01 23 45 67 89 ab cd ef 01 23 45 67 89 ab cd ef 01 23 45 67 89 ab cd ef\r\nCertUtil: -hashfile command completed successfully.\r\n",
	} {
		if got := hexDigest(output); got != digest {
			t.Errorf("hexDigest(%q) = %q, want %q", output, got, digest)
		}
	}
	if got := hexDigest("no digest here\n"); got != "" {
		t.Errorf("hexDigest of nothing = %q", got)
	}
}

func TestLatestVersion(t *testing.T) {
	for _, test := range []struct{ redirect, want, err string }{
		{releaseTags + "v0.2.0", "v0.2.0", ""},
		{releaseTags + "v1.0.0-rc.1", "", "names no published release"},
		{project + "/releases", "", "names no published release"},
		{"", "", "names no published release"},
	} {
		got, err := latestVersion(context.Background(), fakeDownloader{target: test.redirect})
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("redirect %q: got %q, %v; want error %q", test.redirect, got, err, test.err)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("redirect %q: got %q, %v; want %q", test.redirect, got, err, test.want)
		}
	}
}

func TestSelfArguments(t *testing.T) {
	for _, test := range []struct {
		args    []string
		sub     string
		operand string
		err     string
	}{
		{args: []string{"self", "update"}, sub: "update"},
		{args: []string{"self", "update", "v0.2.0"}, sub: "update", operand: "v0.2.0"},
		{args: []string{"self", "update", "0.2.0"}, sub: "update", operand: "v0.2.0"},
		{args: []string{"update", "--dry-run"}, sub: "update"},
		{args: []string{"update", "0.3.0", "--prefix", "/opt/hs"}, sub: "update", operand: "v0.3.0"},
		{args: []string{"install"}, sub: "install"},
		{args: []string{"use", "previous"}, sub: "use", operand: "previous"},
		{args: []string{"use", "0.1.1"}, sub: "use", operand: "v0.1.1"},
		{args: []string{"self", "use", "v0.1.1"}, sub: "use", operand: "v0.1.1"},
		{args: []string{"self", "releases"}, sub: "releases"},
		{args: []string{"use"}, err: "self use needs a version"},
		{args: []string{"self", "update", "a", "b"}, err: "unexpected arguments"},
		{args: []string{"update", "--force"}, err: "--force applies only to link"},
	} {
		inv, err := parse(test.args, new(bytes.Buffer))
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("%q: got %+v, %v; want error %q", test.args, inv, err, test.err)
			}
			continue
		}
		if err != nil || inv.command != "self" || inv.sub != test.sub || inv.operand != test.operand {
			t.Errorf("%q: got command %q sub %q operand %q, %v; want self %q %q", test.args, inv.command, inv.sub, inv.operand, err, test.sub, test.operand)
		}
	}
	if got := selfOptions(invocation{prefix: "/opt/hs", data: "data", root: true, dryRun: true, quiet: true}, "/home/me"); strings.Join(got, " ") !=
		"--home=/home/me --prefix=/opt/hs --data-dir=data --allow-root --dry-run --quiet" {
		t.Errorf("selfOptions = %q", got)
	}
	if got := selfOptions(invocation{}, "/home/me"); strings.Join(got, " ") != "--home=/home/me" {
		t.Errorf("selfOptions = %q", got)
	}
}

func TestSelfReleases(t *testing.T) {
	_, home := repository(t)
	useDownloader(t, fakeDownloader{files: map[string][]byte{
		releasesAPI: []byte(`[{"tag_name":"v0.3.0-rc.1","draft":false,"prerelease":true},{"tag_name":"v0.2.0","draft":false,"prerelease":false},` +
			`{"tag_name":"v0.9.0","draft":true,"prerelease":false},{"tag_name":"nightly","draft":false,"prerelease":false},{"tag_name":"v0.1.1","draft":false,"prerelease":false}]`),
	}})
	output, err := invoke("self", "", home, "releases")
	if err != nil {
		t.Fatal(err)
	}
	// Pre-releases are not versions with files to download (see release), so
	// they are left out; drafts and other tags too.
	if want := "v0.2.0\tnewest\nv0.1.1\t-\n"; output != want {
		t.Errorf("releases = %q, want %q", output, want)
	}
	useDownloader(t, fakeDownloader{files: map[string][]byte{releasesAPI: []byte(`[]`)}})
	if output, err := invoke("self", "", home, "releases"); err != nil || !strings.Contains(output, "no releases published") {
		t.Errorf("releases = %q, %v", output, err)
	}
	useDownloader(t, fakeDownloader{})
	if _, err := invoke("self", "", home, "releases"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("releases without the API: %v", err)
	}
}

// stubRelease is a release v of homeskillet made of a shell script that
// reports v and records the arguments of its self install, served by a
// fakeDownloader with a SHA256SUMS that lists it.
func stubRelease(t *testing.T, v, log string) fakeDownloader {
	t.Helper()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = version ]; then echo 'homeskillet " + v + "'; exit 0; fi\n" +
		"printf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"printf 'installed\\t" + v + "\\n'\n"
	name := releaseFile(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256([]byte(script))
	return fakeDownloader{
		target: releaseTags + v,
		files: map[string][]byte{
			releases + v + "/" + name:      []byte(script),
			releases + v + "/SHA256SUMS":   []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"),
			releases + "v0.0.9/" + name:    []byte("#!/bin/sh\necho 'homeskillet v0.0.9'\n"),
			releases + "v0.0.9/SHA256SUMS": []byte(strings.Repeat("0", 64) + "  " + name + "\n"),
		},
	}
}

func TestSelfUpdate(t *testing.T) {
	requireUnix(t)
	_, home := repository(t)
	log := filepath.Join(t.TempDir(), "install.log")
	useDownloader(t, stubRelease(t, "v9.9.9", log))

	output, err := invoke("self", "", home, "update", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %q, %v", output, err)
	}
	for _, want := range []string{"downloaded\tv9.9.9\t", "checked against its SHA256SUMS", "installed\tv9.9.9", "dry run: nothing changed"} {
		if !strings.Contains(output, want) {
			t.Errorf("dry run output %q lacks %q", output, want)
		}
	}
	if strings.Contains(output, "to go back") {
		t.Errorf("dry run output %q says how to go back from a change it did not make", output)
	}
	recorded, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := "self install --home=" + home + " --dry-run\n"; string(recorded) != want {
		t.Errorf("the download's self install ran with %q, want %q", recorded, want)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dry run wrote %s: %v", filepath.Join(home, ".local"), err)
	}

	// Naming a version downloads that one; one whose SHA256SUMS does not
	// match is never run.
	if _, err := invoke("self", "", home, "update", "0.0.9"); err == nil || !strings.Contains(err.Error(), "does not match the release's SHA256SUMS") {
		t.Errorf("a wrong checksum: %v", err)
	}
	if _, err := invoke("self", "", home, "update", "v0.0.1-rc.1"); err == nil || !strings.Contains(err.Error(), "not a published version") {
		t.Errorf("a pre-release: %v", err)
	}
	if _, err := invoke("self", "", home, "update", "v0.0.2"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a missing release: %v", err)
	}

	// A version that is installed is left alone.
	versionDir := filepath.Join(home, ".local", "share", "homeskillet", "versions", "v9.9.9")
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "homeskillet"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := invoke("self", "", home, "update"); err != nil || !strings.Contains(output, "unchanged\tv9.9.9\talready installed; homeskillet self use v9.9.9 runs it") {
		t.Errorf("update of an installed version: %q, %v", output, err)
	}
}

func TestSelfUpdateVersionMismatch(t *testing.T) {
	requireUnix(t)
	_, home := repository(t)
	from := stubRelease(t, "v9.9.9", filepath.Join(t.TempDir(), "install.log"))
	// The release page says v9.9.9, but its files are published under
	// v9.9.8 and report that.
	name := releaseFile(runtime.GOOS, runtime.GOARCH)
	script := "#!/bin/sh\necho 'homeskillet v9.9.8'\n"
	sum := sha256.Sum256([]byte(script))
	from.files[releases+"v9.9.9/"+name] = []byte(script)
	from.files[releases+"v9.9.9/SHA256SUMS"] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	useDownloader(t, from)
	if _, err := invoke("self", "", home, "update"); err == nil || !strings.Contains(err.Error(), "reports version v9.9.8, not v9.9.9") {
		t.Errorf("a build of another version: %v", err)
	}
}

func TestDescribeFailure(t *testing.T) {
	requireUnix(t)
	// sh exits with the status asked for, as curl would.
	exitWith := func(code int) error {
		return exec.CommandContext(t.Context(), "sh", "-c", "exit "+strconv.Itoa(code)).Run() //nolint:gosec // G204: the test's own command
	}
	for _, test := range []struct {
		code   int
		stderr string
		want   string
	}{
		{6, "curl: (6) Could not resolve host: github.com", "could not be found"},
		{7, "curl: (7) Failed to connect", "could not be reached"},
		{28, "curl: (28) Operation timed out", "timed out"},
		{60, "curl: (60) SSL certificate problem", "secure connection could not be made"},
		{22, "curl: (22) The requested URL returned error: 404", "repository is private"},
		{22, "curl: (22) The requested URL returned error: 403", "rate limit"},
		{22, "curl: (22) The requested URL returned error: 502", "GitHub answered 502"},
		{2, "curl: (2) Failed initialization", "Failed initialization"},
	} {
		err := describeFailure("downloading x", exitWith(test.code), test.stderr)
		if err == nil || !strings.Contains(err.Error(), test.want) || !strings.HasSuffix(err.Error(), "Nothing was changed") {
			t.Errorf("exit %d: %v; want %q and that nothing was changed", test.code, err, test.want)
		}
	}
	if err := describeFailure("downloading x", errors.New("fork failed"), ""); err == nil || !strings.Contains(err.Error(), "fork failed") {
		t.Errorf("a failure to run curl: %v", err)
	}
}
