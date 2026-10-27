// Tests of the parts of wsl on Windows that need no WSL: reading a release's
// SHA256SUMS, and refusing a --from file that is not a Linux build.

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	sums := sum + "  homeskillet-linux-amd64\n" + strings.Repeat("cd", 32) + " *homeskillet-linux-arm64\n" +
		"short  homeskillet-darwin-amd64\n" + strings.Repeat("zz", 32) + "  homeskillet-windows-amd64.exe\n"
	for name, want := range map[string]string{
		"homeskillet-linux-amd64":       sum,
		"homeskillet-linux-arm64":       strings.Repeat("cd", 32),
		"homeskillet-darwin-amd64":      "",
		"homeskillet-windows-amd64.exe": "",
		"homeskillet-linux":             "",
	} {
		if got := checksum(sums, name); got != want {
			t.Errorf("checksum(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestReadLinuxBuild(t *testing.T) {
	dir := t.TempDir()
	windows := filepath.Join(dir, "homeskillet.exe")
	if err := os.WriteFile(windows, []byte("MZ\x90\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLinuxBuild(windows); err == nil || !strings.Contains(err.Error(), "not a Linux build") {
		t.Fatalf("Windows build: %v", err)
	}
	linux := filepath.Join(dir, "homeskillet-linux-amd64")
	if err := os.WriteFile(linux, []byte("\x7fELF\x02\x01"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readLinuxBuild(linux); err != nil || len(data) != 6 {
		t.Fatalf("Linux build: %d bytes, %v", len(data), err)
	}
	makeShared(t, linux)
	if _, err := readLinuxBuild(linux); err == nil {
		t.Fatal("a build others can change was read")
	}
}
