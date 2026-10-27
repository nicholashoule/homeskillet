// Tests of finding linked commands that would run instead of others on
// Windows, where a command's name leaves out the extension that makes it one.

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/link"
)

func TestShadowedWindows(t *testing.T) {
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, later, repo := filepath.Join(root, "first"), filepath.Join(root, "later"), filepath.Join(root, "repo")
	for _, dir := range []string{first, later, repo} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(later, "tool.exe"), filepath.Join(later, "notes.exe"), filepath.Join(repo, "tool.cmd"), filepath.Join(repo, "notes.txt"), filepath.Join(repo, "helper.ps1")} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(later, "helper.cmd"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// PATH names the first directory in another case, as Windows allows.
	t.Setenv("PATH", strings.ToUpper(first)+";"+later)
	plans := []link.Plan{
		{Source: filepath.Join(repo, "tool.cmd"), Target: filepath.Join(first, "tool.cmd"), State: link.Missing},
		{Source: filepath.Join(repo, "notes.txt"), Target: filepath.Join(first, "notes.txt"), State: link.Missing},
		{Source: filepath.Join(repo, "helper.ps1"), Target: filepath.Join(first, "helper.ps1"), State: link.Missing},
	}
	found := shadowed(plans, root, false)
	// tool.cmd runs instead of tool.exe, and PowerShell runs helper.ps1
	// instead of helper.cmd; notes.txt is no command at all.
	want := map[string]string{
		plans[0].Target: filepath.Join(later, "tool.exe"),
		plans[2].Target: filepath.Join(later, "helper.cmd"),
	}
	if len(found) != len(want) {
		t.Fatalf("shadowed = %v, want %v", found, want)
	}
	for target, other := range want {
		if !strings.EqualFold(found[target], other) {
			t.Errorf("%s shadows %q, want %q", target, found[target], other)
		}
	}
}
