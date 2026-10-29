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
	"slices"
	"strings"
	"time"

	"github.com/nicholashoule/homeskillet/internal/config"
	"github.com/nicholashoule/homeskillet/internal/host"
	"github.com/nicholashoule/homeskillet/internal/setup"
)

// findProgram is where prepare looks for a program, as a step's provides
// would (see setup.Provided): PATH, the directories installers add to it,
// and on macOS Homebrew's. Tests replace it, since emptying PATH does not
// hide a program Homebrew installed.
var findProgram = setup.Provided

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

// An item is one thing prepare checks: Developer Mode, winget, Git, or
// PowerShell 7. id names it in a script's lines, title for a person.
type item struct {
	id, title string
	// required items must be in place for prepare to report ready.
	required bool
	// state is done, pending, offered once the checks are shown, or
	// missing, which prepare cannot install.
	state  string
	detail string
}

// The states an item can be in, as a script reads them.
const (
	itemDone    = "done"
	itemPending = "pending"
	itemMissing = "missing"
)

// titles names the items for a person.
var titles = map[string]string{developerModeStep: "Developer Mode", "winget": "winget", "git": "Git", "powershell": "PowerShell 7"}

// assess checks each item for os, changing nothing.
func assess(os, home string) []item {
	found := func(id, program string, required bool, pending, missing string) item {
		it := item{id: id, title: titles[id], required: required}
		switch path := findProgram(program, home); {
		case path != "":
			it.state, it.detail = itemDone, displayPath(path)
		case pending != "":
			it.state, it.detail = itemPending, pending
		default:
			it.state, it.detail = itemMissing, missing
		}
		return it
	}
	if os != "windows" {
		return []item{found("git", "git", true, "", gitAdvice(os))}
	}
	developer := item{id: developerModeStep, title: titles[developerModeStep], required: true, state: itemDone, detail: "symlinks allowed"}
	if host.ProbeSymlinks() != nil {
		developer.state, developer.detail = itemPending, "turn it on in Settings, System, For developers"
	}
	steps := windowsSteps()
	return []item{
		developer,
		found("winget", "winget", false, "register it, or repair it from the PowerShell Gallery", ""),
		found("git", "git", true, setup.Quote(steps[0].Run[0]), ""),
		found("powershell", "pwsh", false, setup.Quote(steps[1].Run[0]), ""),
	}
}

// prepareOutput writes what prepare finds and does: for a person at a
// terminal, a header, groups, and a closing sentence, as the other commands'
// views do; for a script, one tab-separated line per item and outcome.
type prepareOutput struct {
	w      io.Writer
	person bool
}

// outcome writes what just happened to one item, by its id: "skipped  Git"
// for a person, "skipped\tgit" for a script, with detail after it when
// given. A person gets no line for an item that ran: its own output says so,
// and the closing sentence says whether everything is in place.
func (o prepareOutput) outcome(state, id, detail string) error {
	if o.person && state == "ran" {
		return nil
	}
	if o.person {
		line := fmt.Sprintf("%-8s %s", state, titles[id])
		if detail != "" {
			line += "  " + detail
		}
		_, err := fmt.Fprintln(o.w, line)
		return err
	}
	line := state + "\t" + id
	if detail != "" {
		line += "\t" + detail
	}
	_, err := fmt.Fprintln(o.w, line)
	return err
}

// report is a setup.Options.Report for prepare's steps, named by step,
// which is the item's id. A step's pending line, before it is shown and
// asked about, is for a person; a script has the item's line already.
func (o prepareOutput) report(outcome string, plan setup.Plan) error {
	switch {
	case outcome == string(setup.Pending) && !o.person:
		return nil
	case outcome == string(setup.Done):
		return o.outcome(itemDone, plan.Step.Name, setup.Detail(plan)+", by an earlier step")
	}
	return o.outcome(outcome, plan.Step.Name, "")
}

// overview writes the checks before anything is offered: grouped for a
// person, Pending first as setup's view orders them, or one line per item
// for a script.
func (o prepareOutput) overview(items []item, home string, platform config.Platform, inv invocation) error {
	if !o.person {
		for _, it := range items {
			if err := o.outcome(it.state, it.id, it.detail); err != nil {
				return err
			}
		}
		return nil
	}
	text := fmt.Sprintf("homeskillet prepare\n  %-11s %s\n  %-11s %s\n", "home:", displayPath(home), "platform:", platform)
	if inv.dryRun {
		text += fmt.Sprintf("  %-11s %s\n", "dry run:", "nothing will change")
	}
	if _, err := fmt.Fprintln(o.w, text); err != nil {
		return err
	}
	pendingHeading := "Pending (%d): shown in full and asked about before each runs"
	if inv.yes {
		pendingHeading = "Pending (%d): shown in full; --yes asks only about winget's repair, and does not open Settings"
	}
	v := &view{}
	for _, g := range []struct{ state, heading string }{
		{itemPending, pendingHeading},
		{itemMissing, "Missing (%d): homeskillet cannot install these"},
		{itemDone, "Done (%d)"},
	} {
		var entries [][2]string
		for _, it := range items {
			if it.state == g.state {
				entries = append(entries, [2]string{it.title, it.detail})
			}
		}
		if len(entries) > 0 {
			if err := v.writeGroup(o.w, group{heading: fmt.Sprintf(g.heading, len(entries)), entries: entries}); err != nil {
				return err
			}
		}
	}
	return nil
}

// runPrepare checks this machine, shows what it found, and offers what is
// pending, unless this is a dry run. It ends with ready, or with what is
// still needed, and fails then, so a script can use it as a check;
// PowerShell 7 is offered but not required.
func runPrepare(ctx context.Context, inv invocation, home string, input io.Reader, output, diagnostics io.Writer) error {
	platform, _ := host.Detect()
	out := prepareOutput{w: output, person: terminal(output)}
	items := assess(platform.OS, home)
	if err := out.overview(items, home, platform, inv); err != nil {
		return err
	}
	if platform.OS == "windows" && !inv.dryRun {
		if err := offerWindows(ctx, inv, home, items, input, out, diagnostics); err != nil {
			return err
		}
	}
	return out.closing(platform.OS, home, items, inv.dryRun)
}

// closing ends prepare's output. A person reads a sentence: what a dry run
// would offer, then whether the machine is ready, and if not, what is still
// needed. A script reads "ready", or an error naming what is missing.
func (o prepareOutput) closing(os, home string, items []item, dryRun bool) error {
	missing := stillMissing(os, home)
	if !o.person {
		if len(missing) > 0 {
			return fmt.Errorf("not ready: missing %s", strings.Join(missing, " and "))
		}
		_, err := fmt.Fprintln(o.w, "ready\tcan clone and link a repository")
		return err
	}
	var text strings.Builder
	if dryRun {
		offered := 0
		for _, it := range items {
			if it.state == itemPending {
				offered++
			}
		}
		if offered > 0 {
			fmt.Fprintf(&text, "Dry run: nothing changed; prepare would offer %s.\n", files(offered, "step"))
		}
	}
	if len(missing) == 0 {
		text.WriteString("Ready: homeskillet can clone and link a repository here.\n")
		_, err := io.WriteString(o.w, text.String())
		return err
	}
	named := make([]string, len(missing))
	for i, id := range missing {
		named[i] = titles[id]
	}
	verb, pronoun := "is", "it is"
	if len(missing) > 1 {
		verb, pronoun = "are", "they are"
	}
	fmt.Fprintf(&text, "Not ready: %s %s still needed.", list(named...), verb)
	if !dryRun {
		fmt.Fprintf(&text, " Run %s prepare again once %s in place.", selfName(), pronoun)
	}
	text.WriteString("\n")
	if _, err := io.WriteString(o.w, text.String()); err != nil {
		return err
	}
	// Explained above; main prints no more, but the status still fails.
	return reportedError{errors.New("not ready")}
}

// offerWindows offers, in order, what assess found pending on Windows:
// Developer Mode, then winget when it is missing, then the tools it
// installs. When winget cannot be had, each tool's official download page
// is named instead.
func offerWindows(ctx context.Context, inv invocation, home string, items []item, input io.Reader, out prepareOutput, diagnostics io.Writer) error {
	state := map[string]string{}
	for _, it := range items {
		state[it.id] = it.state
	}
	if state[developerModeStep] == itemPending && !inv.yes {
		if err := prepareDeveloperMode(ctx, input, out); err != nil {
			return err
		}
	}
	options := setup.Options{Home: home, Yes: inv.yes, Input: input, Output: out.w, Errors: diagnostics, Report: out.report}
	if state["winget"] == itemPending {
		if err := bringWinget(ctx, home, options); err != nil {
			return err
		}
	}
	steps := windowsSteps()
	if findProgram("winget", home) == "" {
		if err := out.outcome(itemMissing, "winget", wingetMissing); err != nil {
			return err
		}
		for i := range steps {
			if state[steps[i].Name] == itemPending {
				if err := out.outcome(itemMissing, steps[i].Name, officialDownloads[steps[i].Name]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	plans, err := setup.PrepareBuiltin(steps, home)
	if err != nil {
		return err
	}
	// Steps already done were shown in the overview; only the rest run.
	plans = slices.DeleteFunc(plans, func(plan setup.Plan) bool { return plan.State == setup.Done })
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
	found := func() bool { return findProgram("winget", home) != "" }
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
			return options.Report(outcome, plan)
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

// prepareDeveloperMode offers to open the Settings page that turns on
// Developer Mode, which Windows requires before a process may make
// symlinks. Turning it on takes an administrator, which homeskillet will not
// be, so the switch itself is the user's: once they say it is on, it is
// checked again.
func prepareDeveloperMode(ctx context.Context, input io.Reader, out prepareOutput) error {
	open, err := developerSettings()
	if err != nil {
		return err
	}
	if err := out.outcome(itemPending, developerModeStep, ""); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out.w, "  needed for links; switching it on takes an administrator, so Settings opens for you to do it\n  run: %s\n", setup.Quote(open)); err != nil {
		return err
	}
	approved, err := setup.Confirm(input, out.w, "Open Settings at For developers?")
	if err != nil || !approved {
		if err == nil {
			err = out.outcome("skipped", developerModeStep, "")
		}
		return err
	}
	if err := openSettings(ctx, open); err != nil {
		return err
	}
	if _, err := fmt.Fprint(out.w, "Turn on Developer Mode there, then press Enter. "); err != nil {
		return err
	}
	if err := waitForEnter(input); err != nil {
		return err
	}
	if host.ProbeSymlinks() != nil {
		return out.outcome("skipped", developerModeStep, "still off")
	}
	return out.outcome("ran", developerModeStep, "")
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

// stillMissing returns the ids of what os needs that is still missing,
// checked afresh: Developer Mode and Git on Windows, Git elsewhere.
func stillMissing(os, home string) []string {
	var missing []string
	if os == "windows" && host.ProbeSymlinks() != nil {
		missing = append(missing, developerModeStep)
	}
	if findProgram("git", home) == "" {
		missing = append(missing, "git")
	}
	return missing
}
