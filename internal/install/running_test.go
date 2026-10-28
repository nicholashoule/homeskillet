//go:build linux || darwin || windows

// Tests of AboutRunning, which recognizes the line an install writes about
// which version runs after it.

package install

import (
	"bytes"
	"strings"
	"testing"
)

func TestAboutRunning(t *testing.T) {
	var b bytes.Buffer
	// A pinned version stays current.
	if err := reportRunning(&b, State{Current: "v0.1.0"}, []string{"v0.1.0", "v0.2.0"}, "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	// A clean build ranks above a +dirty one of the same version. Versions
	// are in order, lowest first.
	if err := reportRunning(&b, State{Current: "v0.2.0", Following: true}, []string{"v0.2.0+dirty", "v0.2.0"}, "v0.2.0+dirty"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("reportRunning wrote %q", b.String())
	}
	for _, line := range lines {
		if !AboutRunning(line) {
			t.Errorf("not recognized: %q", line)
		}
	}
	for _, line := range []string{
		"installed\tv0.2.0\t/home/you/.local/share/homeskillet/versions/v0.2.0/homeskillet",
		"note\t/home/you/.local/bin is not on PATH; add it in your shell startup file",
		"pinned\tv0.1.0",
		"current\tv0.2.0 (pinned)",
		"",
	} {
		if AboutRunning(line) {
			t.Errorf("recognized: %q", line)
		}
	}
}
