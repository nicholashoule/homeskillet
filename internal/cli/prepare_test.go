//go:build linux || darwin || windows

// Tests of homeskillet prepare: the steps it offers on Windows, and on Linux
// and macOS, whether Git is found and what it says when it is not.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
)

func TestWindowsSteps(t *testing.T) {
	steps := windowsSteps()
	byName := map[string]string{}
	for _, step := range steps {
		if len(step.Run) != 1 || step.Provides == "" || step.Inputs != nil {
			t.Errorf("%s: one command, a program it provides, and no inputs: %+v", step.Name, step)
		}
		command := step.Run[0]
		// Exactly one package, from winget's own source.
		if !slices.Equal(command[:3], []string{"winget", "install", "--id"}) || !slices.Contains(command, "--exact") ||
			!slices.Contains(command, "--source") || command[slices.Index(command, "--source")+1] != "winget" {
			t.Errorf("%s: %q", step.Name, command)
		}
		byName[step.Name] = command[3] + " " + step.Provides
	}
	if byName["git"] != "Git.Git git" || byName["powershell"] != "Microsoft.PowerShell pwsh" {
		t.Errorf("windows steps: %v", byName)
	}
}

func TestWingetSteps(t *testing.T) {
	// Each way to bring winget runs Windows PowerShell, from where it was
	// given, with no profile and no prompts, and is done once winget is
	// found: registering App Installer first, then Microsoft's repair, with
	// everything it installs in the user's scope.
	powershell := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	steps := wingetSteps(powershell)
	if len(steps) != 2 || steps[0].Name != "winget" || steps[1].Name != "winget-repair" {
		t.Fatalf("winget steps: %+v", steps)
	}
	var scripts []string
	for _, step := range steps {
		if step.Provides != "winget" {
			t.Errorf("%s provides %q", step.Name, step.Provides)
		}
		for _, command := range step.Run {
			if !slices.Equal(command[:5], []string{powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}) || len(command) != 6 {
				t.Errorf("%s: %q", step.Name, command)
			}
			scripts = append(scripts, command[5])
		}
	}
	want := []string{
		"Add-AppxPackage -RegisterByFamilyName -MainPackage Microsoft.DesktopAppInstaller_8wekyb3d8bbwe",
		"Install-PackageProvider -Name NuGet -Force -Scope CurrentUser",
		"Install-Module -Name Microsoft.WinGet.Client -Repository PSGallery -Force -Scope CurrentUser",
		"Repair-WinGetPackageManager",
	}
	if !slices.Equal(scripts, want) {
		t.Errorf("scripts %q, want %q", scripts, want)
	}
}

func TestOfficialDownloads(t *testing.T) {
	// Each tool prepare installs names where to get it without winget.
	for _, step := range windowsSteps() {
		if url := officialDownloads[step.Name]; !strings.HasPrefix(url, "https://") {
			t.Errorf("%s: no official download page: %q", step.Name, url)
		}
	}
}

func TestWaitFor(t *testing.T) {
	calls := 0
	if !waitFor(func() bool { calls++; return calls == 3 }, time.Second, time.Millisecond) || calls != 3 {
		t.Errorf("found on the third look: %d calls", calls)
	}
	start := time.Now()
	if waitFor(func() bool { return false }, 20*time.Millisecond, 5*time.Millisecond) || time.Since(start) < 20*time.Millisecond {
		t.Error("gave up before its time, or reported found")
	}
}

// hideGit makes prepare find no Git for the test, wherever it is installed;
// emptying PATH would not hide Homebrew's.
func hideGit(t *testing.T) {
	t.Helper()
	saved := findProgram
	findProgram = func(name, home string) string {
		if name == "git" {
			return ""
		}
		return saved(name, home)
	}
	t.Cleanup(func() { findProgram = saved })
}

func TestPrepareArguments(t *testing.T) {
	for _, args := range [][]string{{"prepare"}, {"prepare", "--dry-run"}, {"prepare", "--yes"}, {"prepare", "--home", "/tmp/h"}, {"prep"}, {"prep", "--dry-run"}} {
		if inv, err := parse(args, new(bytes.Buffer)); err != nil || inv.command != "prepare" {
			t.Errorf("%q: %+v, %v", args, inv, err)
		}
	}
	for args, want := range map[string]string{"prepare extra": "unexpected arguments", "prepare --repo .": "--repo applies only to", "prepare --force": "--force applies only to link",
		"prep --force": "--force applies only to link"} {
		if _, err := parse(strings.Fields(args), new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", args, err, want)
		}
	}
}

func TestPrepareGit(t *testing.T) {
	requireUnix(t)
	_, home := repository(t)
	prepare := func() (string, error) {
		var output bytes.Buffer
		err := Run(context.Background(), []string{"prepare", "--home", home}, strings.NewReader(""), &output, &output)
		return output.String(), err
	}

	// Without Git: says how to get it, offers nothing to run, and fails, so
	// a script can tell.
	restore := findProgram
	hideGit(t)
	output, err := prepare()
	findProgram = restore
	if err == nil || err.Error() != "not ready: missing git" || !strings.Contains(output, "missing\tgit\t"+gitAdvice(runtime.GOOS)) || strings.Contains(output, "[y/N]") {
		t.Errorf("without git: %q, %v", output, err)
	}

	// With Git on PATH: done, and ready.
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	output, err = prepare()
	if err != nil || output != "done\tgit\t"+filepath.Join(bin, "git")+"\nready\tcan clone and link a repository\n" {
		t.Errorf("with git: %q, %v", output, err)
	}
	// Linux and macOS have no Developer Mode to report.
	if strings.Contains(output, developerModeStep) {
		t.Errorf("developer mode reported off Windows: %q", output)
	}
}

func TestWaitForEnter(t *testing.T) {
	input := strings.NewReader("\ny\n")
	if err := waitForEnter(input); err != nil {
		t.Fatal(err)
	}
	// Only the first line is read; the next prompt's answer is still there.
	rest := make([]byte, 8)
	if n, _ := input.Read(rest); string(rest[:n]) != "y\n" {
		t.Errorf("after waitForEnter, %q is left", rest[:n])
	}
	if err := waitForEnter(strings.NewReader("")); err != nil {
		t.Error(err)
	}
	if err := waitForEnter(nil); err != nil {
		t.Error(err)
	}
}

func TestPrepareView(t *testing.T) {
	requireUnix(t)
	_, home := repository(t)
	platform := config.Platform{OS: "windows", Arch: "amd64"}
	items := []item{
		{id: developerModeStep, title: titles[developerModeStep], required: true, state: itemPending, detail: "turn it on in Settings, System, For developers"},
		{id: "winget", title: "winget", state: itemDone, detail: `C:\Users\you\AppData\Local\Microsoft\WindowsApps\winget.exe`},
		{id: "git", title: "Git", required: true, state: itemPending, detail: "winget install --id Git.Git"},
		{id: "powershell", title: "PowerShell 7", state: itemMissing, detail: officialDownloads["powershell"]},
	}
	var output bytes.Buffer
	out := prepareOutput{w: &output, person: true}
	if err := out.overview(items, home, platform, invocation{command: "prepare", dryRun: true}); err != nil {
		t.Fatal(err)
	}
	want := "homeskillet prepare\n  home:       " + home + "\n  platform:   windows/amd64\n  dry run:    nothing will change\n\n" +
		"Pending (2): shown in full and asked about before each runs\n" +
		"  Developer Mode  turn it on in Settings, System, For developers\n" +
		"  Git             winget install --id Git.Git\n\n" +
		"Missing (1): homeskillet cannot install these\n" +
		"  PowerShell 7  https://github.com/PowerShell/PowerShell/releases\n\n" +
		"Done (1)\n" +
		"  winget  C:\\Users\\you\\AppData\\Local\\Microsoft\\WindowsApps\\winget.exe\n\n"
	if output.String() != want {
		t.Errorf("overview:\n%s\nwant:\n%s", output.String(), want)
	}

	// No Git: not ready, said once, so main prints nothing more.
	restore := findProgram
	hideGit(t)
	output.Reset()
	err := out.closing("linux", home, items, true)
	if !Reported(err) || output.String() != "Dry run: nothing changed; prepare would offer 2 steps.\nNot ready: Git is still needed.\n" {
		t.Errorf("dry run closing: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := out.closing("linux", home, nil, false); !Reported(err) ||
		output.String() != "Not ready: Git is still needed. Run homeskillet prepare again once it is in place.\n" {
		t.Errorf("closing: %q, %v", output.String(), err)
	}

	// Git on PATH: ready.
	findProgram = restore
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	output.Reset()
	if err := out.closing("linux", home, nil, false); err != nil || output.String() != "Ready: homeskillet can clone and link a repository here.\n" {
		t.Errorf("ready closing: %q, %v", output.String(), err)
	}

	// Outcomes as they happen: aligned, by title, for a person, who gets no
	// line for what ran; by id, for a script, which gets every outcome.
	output.Reset()
	if err := out.outcome("skipped", "git", ""); err != nil || output.String() != "skipped  Git\n" {
		t.Errorf("person outcome: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := out.outcome("ran", "git", ""); err != nil || output.Len() != 0 {
		t.Errorf("a person got a line for what ran: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := (prepareOutput{w: &output}).outcome("ran", "git", ""); err != nil || output.String() != "ran\tgit\n" {
		t.Errorf("script outcome: %q, %v", output.String(), err)
	}
}
