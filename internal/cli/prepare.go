//go:build linux || darwin || windows

// homeskillet prepare: checking that this machine has what homeskillet and a
// configuration repository need before one is cloned and linked, and helping
// with what is missing. On Windows that is Developer Mode, for links, Git, to
// clone, and PowerShell 7, which most Windows repositories' steps run; on
// Linux and macOS it is Git. Every fix is shown exactly as it would run and
// runs only once approved, with no as the default answer, through the same
// prompt and runner as a repository's setup steps (setup.RunBuiltin).

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/setup"
)

// developerModeStep names Developer Mode in prepare's output.
const developerModeStep = "developer-mode"

// wingetInstall returns the winget command that installs the package id: the
// form Microsoft documents for naming exactly one package, from its own
// source, accepting the source's and the package's agreements so that the
// install does not stop to ask after it was approved. The flags are shown in
// the review like everything else.
func wingetInstall(id string) []string {
	return []string{"winget", "install", "--id", id, "--exact", "--source", "winget", "--accept-source-agreements", "--accept-package-agreements"}
}

// windowsSteps are what prepare offers to install on Windows, with winget,
// each done once the program it provides is found.
func windowsSteps() []config.Step {
	return []config.Step{
		{Name: "git", Run: config.Commands{wingetInstall("Git.Git")}, Provides: "git"},
		{Name: "powershell", Run: config.Commands{wingetInstall("Microsoft.PowerShell")}, Provides: "pwsh"},
	}
}

// wingetMissing says where to get winget when registering and repairing it
// did not bring it: App Installer from the Microsoft Store, which Windows 10
// from 1809 and Windows 11 include, or winget's releases where there is no
// Store, as in Windows Sandbox.
const wingetMissing = "install App Installer: https://apps.microsoft.com/detail/9nblggh4nns1 " +
	"(no Store: https://github.com/microsoft/winget-cli/releases)"

// officialDownloads are where each tool windowsSteps installs comes from,
// for a machine without winget. homeskillet does not download them itself:
// winget verifies each installer against its manifest's SHA-256, and these
// projects publish no checksum file to check a download against.
var officialDownloads = map[string]string{
	"git":        "https://git-scm.com/downloads/win",
	"powershell": "https://github.com/PowerShell/PowerShell/releases",
}

// Registering winget finishes in the background, so after it runs, prepare
// looks for winget for up to wingetWait, every wingetPoll, before offering
// the next way.
const (
	wingetWait = 5 * time.Second
	wingetPoll = 250 * time.Millisecond
)

// registerWinget is the PowerShell command Microsoft gives for a winget not
// yet available after a first sign-in, when App Installer is present but not
// yet registered for the user; it needs no administrator.
const registerWinget = "Add-AppxPackage -RegisterByFamilyName -MainPackage Microsoft.DesktopAppInstaller_8wekyb3d8bbwe"

// wingetSteps returns the steps that bring winget, offered in turn until it
// is found, each run with Windows PowerShell from the Windows directory (see
// windowsPowerShell): registering App Installer, as after a first sign-in,
// then Microsoft's repair through its Microsoft.WinGet.Client module from the
// PowerShell Gallery, as where there is no Microsoft Store, such as Windows
// Sandbox. The module and the NuGet provider that Windows PowerShell needs to
// install it go in the user's scope, which takes no administrator.
func wingetSteps(powershell string) []config.Step {
	command := func(script string) []string {
		return []string{powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script}
	}
	return []config.Step{
		{Name: "winget", Provides: "winget", Run: config.Commands{command(registerWinget)}},
		{Name: "winget-repair", Provides: "winget", Run: config.Commands{
			command("Install-PackageProvider -Name NuGet -Force -Scope CurrentUser"),
			command("Install-Module -Name Microsoft.WinGet.Client -Repository PSGallery -Force -Scope CurrentUser"),
			command("Repair-WinGetPackageManager"),
		}},
	}
}

// gitAdvice says how to install Git on os, where homeskillet does not
// install it itself: on Linux that takes sudo and the distribution's package
// manager, and on macOS Apple's own installer.
func gitAdvice(os string) string {
	if os == "darwin" {
		return "install it: xcode-select --install, or brew install git"
	}
	return "install it: sudo apt install git, sudo dnf install git, or sudo pacman -S git"
}

// runPrepare checks this machine and offers what is missing. A dry run, or a
// script reading the output, gets one line per item: done, pending, or
// missing, with what shows it or how to get it. It fails when something
// required is still missing at the end, so a script can use it as a check;
// PowerShell 7 is offered but not required.
func runPrepare(ctx context.Context, inv invocation, home string, input io.Reader, output, diagnostics io.Writer) error {
	platform, _ := host.Detect()
	if platform.OS == "windows" {
		if err := prepareDeveloperMode(ctx, inv, input, output); err != nil {
			return err
		}
		if err := prepareWindowsTools(ctx, inv, home, input, output, diagnostics); err != nil {
			return err
		}
	} else if program := setup.Provided("git", home); program != "" {
		if _, err := fmt.Fprintf(output, "done\tgit\t%s\n", displayPath(program)); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(output, "missing\tgit\t%s\n", gitAdvice(platform.OS)); err != nil {
		return err
	}
	missing := stillMissing(platform.OS, home)
	if len(missing) > 0 {
		return fmt.Errorf("not ready: missing %s", strings.Join(missing, " and "))
	}
	_, err := fmt.Fprintln(output, "ready\tcan clone and link a repository")
	return err
}

// prepareWindowsTools offers the tools windowsSteps installs, through
// winget, or says how to get winget when it is missing rather than offer
// commands that would fail.
func prepareWindowsTools(ctx context.Context, inv invocation, home string, input io.Reader, output, diagnostics io.Writer) error {
	steps := windowsSteps()
	options := setup.Options{Home: home, Yes: inv.yes, Input: input, Output: output, Errors: diagnostics}
	// winget may be installed but not yet registered, as just after a first
	// sign-in, or need Microsoft's repair; each is offered in turn until
	// winget is found, and when it is, the tools are offered in the same run.
	if setup.Provided("winget", home) == "" && !inv.dryRun {
		if err := bringWinget(ctx, home, options); err != nil {
			return err
		}
	}
	if setup.Provided("winget", home) == "" {
		if _, err := fmt.Fprintf(output, "missing\twinget\t%s\n", wingetMissing); err != nil {
			return err
		}
		for i := range steps {
			line := "missing\t" + steps[i].Name + "\t" + officialDownloads[steps[i].Name] + "\n"
			if program := setup.Provided(steps[i].Provides, home); program != "" {
				line = "done\t" + steps[i].Name + "\t" + displayPath(program) + "\n"
			}
			if _, err := io.WriteString(output, line); err != nil {
				return err
			}
		}
		return nil
	}
	plans, err := setup.PrepareBuiltin(steps, home)
	if err != nil {
		return err
	}
	if inv.dryRun {
		for i := range plans {
			if err := setup.List(output, plans[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return setup.RunBuiltin(ctx, plans, options)
}

// bringWinget offers the ways to bring winget (see wingetSteps) in turn until
// it is found. A way that fails, as when App Installer is not there at all,
// is reported, and the next is offered. After the registration runs, winget
// is looked for a few seconds, since Windows finishes it in the background.
// The repair installs code from the PowerShell Gallery, so it is asked about
// even under --yes: unattended, with no one to answer, it is declined.
func bringWinget(ctx context.Context, home string, options setup.Options) error {
	powershell, err := windowsPowerShell()
	if err != nil {
		return err
	}
	found := func() bool { return setup.Provided("winget", home) != "" }
	ways := wingetSteps(powershell)
	for i := range ways {
		if found() {
			return nil
		}
		plans, err := setup.PrepareBuiltin(ways[i:i+1], home)
		if err != nil {
			return err
		}
		way := options
		if ways[i].Name == "winget-repair" {
			way.Yes = false
		}
		ran := false
		way.Report = func(outcome string, plan setup.Plan) error {
			ran = ran || outcome == "ran"
			if outcome == string(setup.Done) {
				return setup.List(options.Output, plan)
			}
			_, err := fmt.Fprintf(options.Output, "%s\t%s\n", outcome, plan.Step.Name)
			return err
		}
		if err := setup.RunBuiltin(ctx, plans, way); err != nil {
			if _, werr := fmt.Fprintf(options.Errors, "homeskillet: %v\n", err); werr != nil {
				return werr
			}
		} else if ran {
			waitFor(found, wingetWait, wingetPoll)
		}
	}
	return nil
}

// waitFor calls found every interval until it reports true or total has
// passed, and returns its last answer.
func waitFor(found func() bool, total, interval time.Duration) bool {
	deadline := time.Now().Add(total)
	for {
		if found() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// prepareDeveloperMode checks that this process may make symlinks, which
// Windows allows only in Developer Mode, and if not, offers to open the
// Settings page that turns it on. Turning it on takes an administrator,
// which homeskillet will not be, so the switch itself is the user's: once
// they say it is on, it is checked again. Unattended, under --yes, the page
// is not opened, since nobody is there to use it.
func prepareDeveloperMode(ctx context.Context, inv invocation, input io.Reader, output io.Writer) error {
	if host.ProbeSymlinks() == nil {
		_, err := fmt.Fprintf(output, "done\t%s\tsymlinks allowed\n", developerModeStep)
		return err
	}
	open, err := developerSettings()
	if err != nil {
		return err
	}
	if inv.dryRun || inv.yes {
		_, err := fmt.Fprintf(output, "pending\t%s\tturn it on in Settings, System, For developers\n", developerModeStep)
		return err
	}
	if _, err := fmt.Fprintf(output, "pending\t%s\n  needed for links; switching it on takes an administrator, so Settings opens for you to do it\n  run: %s\n",
		developerModeStep, setup.Quote(open)); err != nil {
		return err
	}
	approved, err := setup.Confirm(input, output, "Open Settings at For developers?")
	if err != nil || !approved {
		if err == nil {
			_, err = fmt.Fprintf(output, "skipped\t%s\n", developerModeStep)
		}
		return err
	}
	if err := openSettings(ctx, open); err != nil {
		return err
	}
	if _, err := fmt.Fprint(output, "Turn on Developer Mode there, then press Enter. "); err != nil {
		return err
	}
	if err := waitForEnter(input); err != nil {
		return err
	}
	outcome := "ran"
	if host.ProbeSymlinks() != nil {
		outcome = "skipped"
	}
	_, err = fmt.Fprintf(output, "%s\t%s\n", outcome, developerModeStep)
	return err
}

// waitForEnter reads input up to a newline or its end, a byte at a time, as
// setup.Confirm reads, so that nothing meant for the next prompt is read
// ahead and lost.
func waitForEnter(input io.Reader) error {
	if input == nil {
		return nil
	}
	buffer := make([]byte, 1)
	for {
		n, err := input.Read(buffer)
		if n == 1 && buffer[0] == '\n' || errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// stillMissing returns what os needs that is still missing, checked afresh:
// Developer Mode and Git on Windows, Git elsewhere.
func stillMissing(os, home string) []string {
	var missing []string
	if os == "windows" && host.ProbeSymlinks() != nil {
		missing = append(missing, developerModeStep)
	}
	if setup.Provided("git", home) == "" {
		missing = append(missing, "git")
	}
	return missing
}
