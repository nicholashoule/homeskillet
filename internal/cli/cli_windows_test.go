// Tests of the command line on Windows: paths written with backslashes and
// drives, the system locations it refuses, and status and setup against a
// repository, which need no symlinks.

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestWindowsPaths(t *testing.T) {
	for word, want := range map[string]bool{
		`.\dotfiles`: true, `..\dotfiles`: true, `C:\dotfiles`: true, `C:dotfiles`: true, "./dotfiles": true,
		"setup": false, "link": false, "dotfiles": false,
	} {
		if got := isPath(word); got != want {
			t.Errorf("isPath(%q) = %v", word, got)
		}
	}
	for name, want := range map[string]string{`~`: "", `~\.config`: `\.config`, "~/.config": "/.config"} {
		if rest, ok := homeRelative(name); !ok || rest != want {
			t.Errorf("homeRelative(%q) = %q, %v", name, rest, ok)
		}
	}
	if _, ok := homeRelative("~other"); ok {
		t.Error(`~other read as the home`)
	}
}

func TestRefusesSystemLocationsWindows(t *testing.T) {
	repo, home := fixture(t, "config")
	system := os.Getenv("SystemRoot")
	drive := filepath.VolumeName(system) + `\`
	programs := os.Getenv("ProgramFiles")
	for _, args := range [][]string{
		{"status", "--repo", repo, "--home", drive},
		{"setup", "--repo", repo, "--home", system},
		{"status", "--repo", programs, "--home", home},
		{"status", "--repo", repo, "--home", home, "--data-dir", filepath.Join(system, "Temp")},
		{"self", "list", "--home", home, "--prefix", programs},
	} {
		err := Run(context.Background(), args, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "refusing to act on") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestStatusWindows(t *testing.T) {
	file := `.config\powershell\profile.ps1`
	repo, home := fixture(t, file)
	output, err := invoke("status", repo, home)
	// The fixture lists the file in a manifest, with its source at the root.
	want := "missing\t" + filepath.Join(home, file) + " -> " + filepath.Join(repo, file) + "\n"
	if err != nil || output != want {
		t.Fatalf("status: %q, %v; want %q", output, err, want)
	}
	// A file named by its target, with a backslash, or by ~\, selects it.
	for _, name := range []string{file, `~\` + file, filepath.Join(home, file), strings.ToUpper(filepath.Join(home, file))} {
		if output, err := invoke("status", repo, home, name); err != nil || output != want {
			t.Errorf("status %s: %q, %v", name, output, err)
		}
	}
}

func TestSetupDocumentsWindows(t *testing.T) {
	repo, home := fixture(t)
	manifest := `{"setup": [{"name": "profile", "run": ["cmd", "/d", "/c", "exit"], "creates": "{documents}/PowerShell/profile.ps1"}]}`
	if err := os.WriteFile(filepath.Join(repo, config.FileName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	// A scratch home has a Documents folder of its own.
	output, err := invoke("setup", repo, home, "--dry-run")
	if err != nil || !strings.HasPrefix(output, "pending\tprofile\t") {
		t.Fatalf("setup --dry-run: %q, %v", output, err)
	}
	profile := filepath.Join(home, "Documents", "PowerShell", "profile.ps1")
	if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := invoke("setup", repo, home, "--dry-run"); err != nil || output != "done\tprofile\t"+profile+"\n" {
		t.Fatalf("setup --dry-run with the profile: %q, %v", output, err)
	}
}

// TestPathAdvice checks that the note about Path points to the Windows
// guide's lines, at a heading that exists there.
func TestPathAdvice(t *testing.T) {
	got := pathAdvice(`C:\Users\you\.local\bin`)
	if !strings.Contains(got, windowsGuide+"#putting-the-command-on-your-path") {
		t.Errorf("pathAdvice: %s", got)
	}
	guide, err := os.ReadFile(filepath.Join("..", "..", "docs", "windows.md"))
	if err != nil || !strings.Contains(string(guide), "\n### Putting the Command on Your Path\n") {
		t.Errorf("docs/windows.md has no heading for the link: %v", err)
	}
}
