// Tests of the command line on Windows: paths written with backslashes and
// drives, the system locations it refuses, status and setup against a
// repository, which need no symlinks, and --allow-open-profile.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/trust"
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

// TestAllowOpenProfileWindows checks that --allow-open-profile lets status and
// check go on past a repository in the profile that others may change, naming
// it once on each run, only for that run, and that the commands that run or
// install code refuse it.
func TestAllowOpenProfileWindows(t *testing.T) {
	file := `.config\powershell\profile.ps1`
	repo, home := fixture(t, file)
	profile, err := filepath.EvalSymlinks(os.Getenv("USERPROFILE"))
	if err != nil || !trust.Within(profile, repo) {
		t.Skipf("the temporary directory is not in the profile: %v", err)
	}
	makeShared(t, repo)
	warning := "homeskillet: warning: " + repo + `: BUILTIN\Users can change it; going on because of --allow-open-profile` + "\n"
	for _, command := range []string{"status", "check"} {
		if _, err := invoke(command, repo, home); err == nil {
			t.Fatalf("%s: repository others may change accepted", command)
		}
		args := []string{command, "--repo", repo, "--allow-open-profile"}
		if command == "status" {
			args = append(args, "--home", home)
		}
		for range 2 {
			var output, diagnostics bytes.Buffer
			if err := Run(context.Background(), args, strings.NewReader(""), &output, &diagnostics); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if diagnostics.String() != warning {
				t.Errorf("%v warned %q, want %q", args, diagnostics.String(), warning)
			}
		}
		if _, err := invoke(command, repo, home); err == nil {
			t.Fatalf("%s: repository others may change accepted after --allow-open-profile", command)
		}
	}
	for _, args := range [][]string{
		{"setup", "--allow-open-profile"}, {"init", "--allow-open-profile"}, {"prepare", "--allow-open-profile"},
		{"self", "install", "--allow-open-profile"}, {"update", "--allow-open-profile"},
	} {
		if _, err := parse(args, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "--allow-open-profile applies only to status, link, unlink, check") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := parse([]string{"wsl", "link", "--allow-open-profile"}, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "not in a WSL distribution") {
		t.Errorf("wsl link --allow-open-profile: %v", err)
	}
}

// TestPathAdvice checks that the note about Path says how to add it in
// Windows's own dialog, with no link a later change to the docs could break.
func TestPathAdvice(t *testing.T) {
	got := pathAdvice(`C:\Users\you\.local\bin`)
	if !strings.Contains(got, "Edit environment variables for your account") || strings.Contains(got, "https://") {
		t.Errorf("pathAdvice: %s", got)
	}
}
