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
	"slices"
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
		releasesAPI: []byte(`[{"tag_name":"v0.4.0","draft":false,"prerelease":true},{"tag_name":"v0.3.0-rc.1","draft":false,"prerelease":true},` +
			`{"tag_name":"v0.2.0","draft":false,"prerelease":false},` +
			`{"tag_name":"v0.9.0","draft":true,"prerelease":false},{"tag_name":"nightly","draft":false,"prerelease":false},{"tag_name":"v0.1.1","draft":false,"prerelease":false}]`),
	}})
	output, err := invoke("self", "", home, "releases")
	if err != nil {
		t.Fatal(err)
	}
	// A version with a pre-release part has no files to download (see
	// release), so it is left out, as are drafts and other tags. A release
	// GitHub flags as a pre-release is listed, but is not the newest, since
	// the latest release page skips it and so does update.
	if want := "v0.4.0\tpre-release\nv0.2.0\tnewest\nv0.1.1\t-\n"; output != want {
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
	// The stub runs as a command, so it must see only test directories.
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", t.TempDir())
	log := filepath.Join(t.TempDir(), "install.log")
	useDownloader(t, stubRelease(t, "v9.9.9", log))

	// A dry run downloads and checks, and runs nothing it downloaded.
	output, err := invoke("self", "", home, "update", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %q, %v", output, err)
	}
	for _, want := range []string{"downloaded\tv9.9.9\t", "checked against its SHA256SUMS", "install\tv9.9.9\twould be installed", "dry run: nothing changed"} {
		if !strings.Contains(output, want) {
			t.Errorf("dry run output %q lacks %q", output, want)
		}
	}
	if strings.Count(output, "dry run: nothing changed") != 1 || strings.Contains(output, "to go back") {
		t.Errorf("dry run output %q: the closing line must appear once, and nothing says how to go back", output)
	}
	if _, err := os.Lstat(log); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dry run ran the download: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dry run wrote %s: %v", filepath.Join(home, ".local"), err)
	}

	// A real run installs through the download's own self install, with the
	// same home, staged under the data directory, which is cleaned up.
	output, err = invoke("self", "", home, "update")
	if err != nil {
		t.Fatalf("update: %q, %v", output, err)
	}
	recorded, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := "self install --home=" + home + "\n"; string(recorded) != want {
		t.Errorf("the download's self install ran with %q, want %q", recorded, want)
	}
	if strings.Contains(output, "to go back") {
		t.Errorf("update output %q says how to go back when nothing ran before", output)
	}
	data := filepath.Join(home, ".local", "share", "homeskillet")
	if entries, err := os.ReadDir(data); err != nil || len(entries) != 0 {
		t.Errorf("staging left %v in %s (%v)", entries, data, err)
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
	t.Setenv("HOME", home)
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
		{22, "curl: (22) The requested URL returned error: 404 Not Found", "repository is private"},
		{22, "curl: (22) The requested URL returned error: 403", "rate limit"},
		{22, "curl: (22) The requested URL returned error: 502", "GitHub answered 502"},
		{22, "", "GitHub answered an error"},
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

func TestCurlOptions(t *testing.T) {
	// Reading a redirect must not follow it, or curl reports the redirect
	// as empty; a download follows redirects, to HTTPS only, and stops at
	// the bound on a build.
	head := strings.Join(headOptions(latestRelease), " ")
	if strings.Contains(head, "--location") || !strings.Contains(head, "--head") || !strings.Contains(head, "%{redirect_url}") {
		t.Errorf("head options: %s", head)
	}
	get := strings.Join(getOptions("out", releases+"v1.0.0/SHA256SUMS"), " ")
	for _, want := range []string{"--location", "--proto-redir =https", "--max-filesize " + strconv.FormatInt(maxProgram, 10), "--output out"} {
		if !strings.Contains(get, want) {
			t.Errorf("get options %s lack %q", get, want)
		}
	}
	for _, options := range [][]string{headOptions("x"), getOptions("out", "x")} {
		for _, want := range []string{"--fail", "--proto", "=https", "--tlsv1.2"} {
			if !slices.Contains(options, want) {
				t.Errorf("options %q lack %q", options, want)
			}
		}
	}
}
