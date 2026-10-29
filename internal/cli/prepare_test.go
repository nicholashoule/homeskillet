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
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	prepare := func() (string, error) {
		var output bytes.Buffer
		err := Run(context.Background(), []string{"prepare", "--home", home}, strings.NewReader(""), &output, &output)
		return output.String(), err
	}

	// Without Git: says how to get it, offers nothing to run, and fails, so
	// a script can tell.
	output, err := prepare()
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
