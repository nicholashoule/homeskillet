// Tests of setup steps on Windows: command lookup through PATHEXT, the PATH
// steps run with, the environment, the Documents folder, and stopping a step
// and everything it started.

package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
)

// fixture creates a private repository whose manifest lists steps, plus an
// empty home directory, under a canonical temporary root.
func fixture(t *testing.T, steps ...config.Step) (repo, home string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, home = filepath.Join(root, "repo"), filepath.Join(root, "home")
	for _, directory := range []string{repo, home} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(config.Manifest{Setup: steps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, config.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, home
}

// powershell returns a step that runs script with Windows PowerShell, which
// every Windows has and which reads its command line as Go quotes it.
func powershell(name, script string) config.Step {
	return config.Step{Name: name, Run: config.Commands{{"powershell", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script}}}
}

func TestLookPathWindows(t *testing.T) {
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	dir := t.TempDir()
	for _, name := range []string{"tool.exe", "script.cmd", "data.txt", "both.cmd", "both.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.exe"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"tool":     "tool.exe",
		"TOOL":     "TOOL.exe",
		"tool.exe": "tool.exe",
		"script":   "script.cmd",
		// PATHEXT's order decides between extensions in one directory.
		"both": "both.exe",
	} {
		if got, err := lookPath(name, `C:\nonexistent;`+dir); err != nil || !strings.EqualFold(got, filepath.Join(dir, want)) {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"data", "data.txt", "folder", "missing"} {
		if got, err := lookPath(name, dir); err == nil {
			t.Errorf("%s resolved to %s", name, got)
		}
	}
	// Relative entries, including empty ones, are never searched, though
	// Windows's own search tries the current directory first.
	t.Chdir(dir)
	for _, path := range []string{"", ".", ";", `folder.exe\..`} {
		if got, err := lookPath("tool", path); err == nil {
			t.Errorf("PATH %q resolved tool in the current directory: %s", path, got)
		}
	}
	for _, name := range []string{`.\setup\x.ps1`, "setup/x.ps1", `C:\x.exe`, "C:x.exe"} {
		if got, err := lookPath(name, ""); err != nil || got != name {
			t.Errorf("path %s changed: %q, %v", name, got, err)
		}
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("HOMESKILLET_TEST_DIR", `C:\tools`)
	for in, want := range map[string]string{
		`%HOMESKILLET_TEST_DIR%\bin`:          `C:\tools\bin`,
		`%homeskillet_test_dir%\bin`:          `C:\tools\bin`,
		`%HOMESKILLET_TEST_UNSET%\bin`:        `%HOMESKILLET_TEST_UNSET%\bin`,
		`a%%b`:                                `a%%b`,
		`50%`:                                 `50%`,
		`%HOMESKILLET_TEST_DIR%;%x%;C:\plain`: `C:\tools;%x%;C:\plain`,
		`%HOMESKILLET_TEST_DIR%%HOMESKILLET_TEST_DIR%`: `C:\toolsC:\tools`,
	} {
		if got := expand(in); got != want {
			t.Errorf("expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchPathAppendsWindows(t *testing.T) {
	_, home := fixture(t)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(os.Getenv("SystemRoot"), "System32")
	t.Setenv("PATH", system)
	got := filepath.SplitList(searchPath(home))
	if len(got) == 0 || got[0] != system {
		t.Fatalf("inherited PATH not kept first: %q", got)
	}
	count := 0
	for _, entry := range got {
		if strings.EqualFold(entry, bin) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf(".local\\bin appears %d times in %q", count, got)
	}
	// Already listed, in any case, it is not added again.
	t.Setenv("PATH", strings.ToUpper(bin)+";"+system)
	if got := searchPath(home); strings.Count(strings.ToLower(got), strings.ToLower(bin)) != 1 {
		t.Fatalf("listed .local\\bin added again: %q", got)
	}
}

func TestCreatesInDocuments(t *testing.T) {
	steps := []config.Step{{Name: "profile", Run: config.Commands{{"cmd", "/d", "/c", "exit"}}, Creates: config.DocumentsPrefix + "PowerShell/profile.ps1"}}
	repo, home := fixture(t, steps...)
	// A home that is not the user's profile has its own Documents folder,
	// so a trial run never looks at the real one.
	path, err := createsPath(steps[0].Creates, home)
	if want := filepath.Join(home, "Documents", "PowerShell", "profile.ps1"); err != nil || path != want {
		t.Fatalf("createsPath = %q, %v; want %q", path, err, want)
	}
	if plans := prepare(t, steps, repo, home); plans[0].State != Pending {
		t.Fatal("done before the profile exists")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if plans := prepare(t, steps, repo, home); plans[0].State != Done || plans[0].Found != path {
		t.Fatalf("profile in the home's Documents not recognized: %+v", plans[0])
	}
	// The user's own profile has the Documents folder Windows records,
	// wherever OneDrive or an administrator moved it.
	profile, err := host.Profile.Path()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(profile); err == nil {
		profile = resolved
	}
	documents, err := host.Documents.Path()
	if err != nil {
		t.Fatal(err)
	}
	if path, err := createsPath(steps[0].Creates, profile); err != nil || !strings.EqualFold(path, filepath.Join(documents, "PowerShell", "profile.ps1")) {
		t.Fatalf("createsPath for the profile = %q, %v; Documents is %s", path, err, documents)
	}
}

func prepare(t *testing.T, steps []config.Step, repo, home string) []Plan {
	t.Helper()
	plans, err := Prepare(steps, repo, home)
	if err != nil {
		t.Fatal(err)
	}
	return plans
}

func TestRunEnvironmentWindows(t *testing.T) {
	steps := []config.Step{powershell("environment", `Set-Content -LiteralPath (Join-Path $env:HOME environment) -Value (($env:HOME, $env:USERPROFILE, $env:HOMESKILLET_HOME, $env:HOMESKILLET_REPO, (Get-Location).Path) -join '|')`)}
	repo, home := fixture(t, steps...)
	for _, yes := range []bool{true, false} {
		input := strings.NewReader("y\n")
		if err := Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: yes, Input: input}); err != nil {
			t.Fatalf("yes=%v: %v", yes, err)
		}
		data, err := os.ReadFile(filepath.Join(home, "environment"))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join([]string{home, home, home, repo, repo}, "|")
		if got := strings.TrimSpace(string(data)); got != want {
			t.Fatalf("yes=%v: step saw %q, want %q", yes, got, want)
		}
	}
}

func TestRunYesCancellationStopsTheTree(t *testing.T) {
	// The step starts a process of its own, which writes started at once
	// and late a few seconds later, then waits. Cancellation must stop
	// both, as the job object holds every process the step starts.
	child := `Set-Content -LiteralPath (Join-Path $env:HOME started) -Value 1; Start-Sleep 4; Set-Content -LiteralPath (Join-Path $env:HOME late) -Value 1`
	steps := []config.Step{powershell("tree", `Start-Process -WindowStyle Hidden powershell -ArgumentList '-NoProfile', '-Command', '`+child+`'; Start-Sleep 60`)}
	repo, home := fixture(t, steps...)
	ctx, cancel := context.WithCancel(context.Background())
	started := filepath.Join(home, "started")
	go func() {
		defer cancel()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				return
			}
		}
	}()
	if err := Run(ctx, prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true}); err == nil {
		t.Fatal("canceled step succeeded")
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("the step's own process never started, so the test proves nothing")
	}
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(filepath.Join(home, "late")); err == nil {
		t.Fatal("a process the canceled step started kept running")
	}
}

func TestRunYesGivesStepsNoInputWindows(t *testing.T) {
	steps := []config.Step{powershell("read", `Set-Content -LiteralPath (Join-Path $env:HOME read) -Value ([string]::IsNullOrEmpty([Console]::In.ReadLine()))`)}
	repo, home := fixture(t, steps...)
	if err := Run(context.Background(), prepare(t, steps, repo, home), Options{Repo: repo, Home: home, Yes: true, Input: strings.NewReader("secret\n")}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "read")); err != nil || strings.TrimSpace(string(data)) != "True" {
		t.Fatalf("step under --yes read input: %q, %v", data, err)
	}
}
