# Windows

homeskillet runs natively on Windows 10 and 11, on x64 and Arm64, from PowerShell or any other shell. Inside WSL it is a
Linux program, as on any other Linux. You choose what it manages: only your PowerShell profile and other Windows
dotfiles; only your WSL home; or both, from one repository, with `home/` for Windows and `home/wsl/` for the
distribution, each side with its own install and record of links. This page covers everything that differs on
Windows: what is supported, how to set Windows up, how to lay out a repository for it, and how Windows and WSL work
side by side. The rest of the documentation applies as written.

## What Is Supported

| Where | How homeskillet runs there |
|---|---|
| Windows 10 and 11, x64 or Arm64 | `homeskillet.exe`, the Windows build, from PowerShell 7, Windows PowerShell, or `cmd`. Linking needs Developer Mode. Tab completion needs PowerShell 7.3 or later. |
| WSL, with Ubuntu or another distribution | The Linux build, inside the distribution, with the Linux home, exactly as on any Linux: run there, or from Windows with `homeskillet wsl`. |
| Windows and WSL on one machine | Both, each with its own install, home, and record of links: one repository with `home/wsl/` for WSL, or a second repository cloned inside WSL ([Windows and WSL Together](#windows-and-wsl-together)). |
| A Windows repository, checked from Linux or macOS | `check`, and `--platform windows` with a scratch home ([Checking a Repository](#checking-a-repository)). |

Not supported, and refused with a message saying why: an elevated terminal without `--allow-root`; linking without
Developer Mode; a repository, home, or data directory in a system location, behind a junction, or in a WSL distribution
reached from Windows; and, inside WSL, a repository on a Windows drive under `/mnt`.

## Before You Start

`homeskillet prepare` checks all of this, and offers to fix what it can, each fix shown exactly and run only once you
answer `y` (see [Preparing Windows](#preparing-windows)). The install steps below run it first.

- **Turn on Developer Mode** in Settings, System, For developers, which `start ms-settings:developers` opens. Windows
  lets a program make symlinks only in Developer Mode, or when it runs elevated, which homeskillet refuses by default.
  Without it, `link`, `install`, `update`, and `use` stop before changing anything and say so. `status`, `unlink`,
  `setup`, `check`, `list`, and `list-remote` work either way.
- **Run from a terminal that is not elevated.** An elevated (administrator) process is Windows's counterpart of root,
  and every command refuses it unless `--allow-root` is given.
- **Keep the repository under your profile directory**, for example `%USERPROFILE%\projects\dotfiles`, and outside
  OneDrive. A folder created at the root of a drive, such as `C:\src`, lets every signed-in user change what is in it,
  so homeskillet refuses it (see [Ownership and Permissions](#ownership-and-permissions)), and OneDrive keeps files as
  placeholders it fetches on demand, which homeskillet does not trust.

## Preparing Windows

```powershell
homeskillet prepare              # check, and offer what is missing; prep is short for it
homeskillet prepare --dry-run    # check only; exits nonzero while something needed is missing
```

`prepare` needs no repository, so it can run on a fresh machine, from the downloaded build before homeskillet is even
installed. It checks, in order:

| Item | Needed for | When it is missing |
|---|---|---|
| Developer Mode | Links, and installing homeskillet itself | Offers to open Settings at For developers, waits while you switch it on, then checks again. Turning it on takes an administrator, so the switch is yours. |
| winget | The two installs below | Offers, in turn until winget is found: registering it with Microsoft's `Add-AppxPackage -RegisterByFamilyName -MainPackage Microsoft.DesktopAppInstaller_8wekyb3d8bbwe`, as needed after a first sign-in, then waits a few seconds for Windows to finish; then Microsoft's repair, which installs the NuGet provider and the `Microsoft.WinGet.Client` module from the PowerShell Gallery for your account only and runs `Repair-WinGetPackageManager`, asked about even under `--yes`. Failing both, it names App Installer in the Microsoft Store, winget's GitHub releases, and each tool's official download page. |
| Git | Cloning a repository | Offers `winget install --id Git.Git --exact --source winget` with the two `--accept-…-agreements` flags. winget checks the installer against its manifest's SHA-256, which is why homeskillet does not download installers itself. |
| PowerShell 7 | Most Windows repositories' steps, and tab completion | Offers `winget install --id Microsoft.PowerShell` the same way. Not required: `prepare` is ready without it. |

Each offer is a step like a repository's setup steps: shown exactly, with the folder it runs in, then `Run this step?
[y/N]`, where only `y` or `yes` runs it. The commands are homeskillet's own, so there is no repository to check, and
they run in your profile folder, which must pass the [ownership rules](#ownership-and-permissions). Installers that need
an administrator ask through User Account Control; homeskillet itself never runs elevated. Under `--yes`, the installs
run without asking, except winget's repair, which downloads code from the PowerShell Gallery, and Settings is not
opened, since nobody is there to switch Developer Mode on. `prepare` ends with `ready` when Developer Mode and Git are
in place, and otherwise names what is still missing and exits nonzero. On Linux and macOS it checks for Git and says how
to install it, since that takes `sudo` or Apple's installer.

## Installing

In Windows PowerShell or PowerShell 7, from a folder of your own such as Downloads, fetch the build for your machine and
the release's `SHA256SUMS`, and only if `SHA256SUMS` lists its SHA-256, run the build's `prepare`, then its `install`:

```powershell
$base = 'https://github.com/nicholashoule/homeskillet/releases/latest/download'
$exe = 'homeskillet-windows-amd64.exe'   # homeskillet-windows-arm64.exe on Arm
curl.exe -fsSLO --proto =https "$base/$exe"
curl.exe -fsSLO --proto =https "$base/SHA256SUMS"
$sum = (Get-FileHash $exe -Algorithm SHA256).Hash
if ((Get-Content SHA256SUMS) -contains "$sum  $exe") { & ".\$exe" prepare; if ($LASTEXITCODE -eq 0) { & ".\$exe" install } } else { Write-Error "$exe does not match SHA256SUMS; it was not run" }
```

Write `curl.exe`, not `curl`, which Windows PowerShell takes for its own `Invoke-WebRequest`. The check and the run are
one statement, so that even pasted line by line, a build that fails the check is never run. For a particular release,
write `download/v0.3.1` in place of `latest/download`. Delete the two files afterward. The rest is homeskillet's own:
`prepare` gets Developer Mode on and offers Git, and `install` runs only once `prepare` reports `ready`; both refuse an
elevated terminal.

With Go 1.24 or newer, on a machine without homeskillet,
`go run github.com/nicholashoule/homeskillet@latest install` builds the release from source and installs it, with
nothing to check by hand ([install.md](install.md#building-and-installing)). From a clone of this repository:
`go build -o bin\homeskillet.exe .`, then `bin\homeskillet.exe install`, or `make install` where GNU Make is
installed ([development.md](development.md#on-windows)). Every way, the command lands in
`%USERPROFILE%\.local\bin\homeskillet.exe`, where uv and other installers put programs too. homeskillet says so if that
directory is not on your Path; add it to your user Path in Settings (search for environment variables), and open a new
terminal. After that, homeskillet manages its own versions: `homeskillet update` installs the newest release, and
`homeskillet install 0.3.0` a particular one, with Windows's own `curl.exe` and `certutil.exe` to download and check
it. [install.md](install.md) covers updating, versions, pinning, and removal, which work as on Linux and macOS.

For tab completion in PowerShell 7.3 or later, add this to your profile:

```powershell
if (Get-Command homeskillet -ErrorAction SilentlyContinue) {
    homeskillet completion pwsh | Out-String | Invoke-Expression
}
```

Earlier versions of PowerShell drop the empty argument a completion passes for the word after a space. Where homeskillet
offers nothing, PowerShell completes file names, as it does for any program.

## Configuring a Repository for Windows

### Layout

`home/` mirrors your profile directory, `%USERPROFILE%`, as it mirrors `~` on other systems: `home/AppData/Local/x`
links to `%USERPROFILE%\AppData\Local\x`. Every file is linked on its own; folders are created as needed and never
linked, so what else is in `AppData` or `.config` is left alone.

```text
dotfiles-windows/
├── bootstrap.json         setup steps that install tools
├── dotfiles.json          platforms, and steps that create files
├── home/
│   ├── .config/powershell/profile.ps1
│   ├── .config/oh-my-posh/themes/mine.omp.json
│   ├── AppData/Local/Microsoft/Windows Terminal/Fragments/dotfiles/profiles.json
│   └── wsl/               optional: the WSL home, linked only there
│       ├── .bashrc
│       └── .config/git/config
└── setup/                 scripts the steps run
    ├── init-profile.ps1
    └── install-tools.ps1
```

`home/wsl/`, when a repository has it, mirrors the home of a WSL distribution, `~` there: `home/wsl/.bashrc` is linked
to `~/.bashrc` inside WSL, never into `%USERPROFILE%`. Its files are for Linux, whatever the manifest's `platforms` say,
and the rest of `home/` is for Windows ([Windows and WSL Together](#windows-and-wsl-together)).

Where Windows programs read their configuration decides what goes under `home/`:

| Configuration | Where it goes |
|---|---|
| Tools that read `%USERPROFILE%\.config`, such as Git, Oh My Posh's themes, and anything following the XDG layout | `home/.config/...`, linked |
| Programs that read `AppData\Local` or `AppData\Roaming` | `home/AppData/Local/...` or `home/AppData/Roaming/...`, linked |
| Windows Terminal | A fragment, `home/AppData/Local/Microsoft/Windows Terminal/Fragments/<name>/<file>.json`, which Terminal only reads. Terminal rewrites its own `settings.json`, which would turn a link into a copy. |
| PowerShell's profile | Not linked. PowerShell reads it only from your Documents folder, which OneDrive may move and sync, so keep the profile at `home/.config/powershell/profile.ps1` and let a setup step write a one-line stub in Documents that loads it ([The Documents Folder](#the-documents-folder)). |

Names must be ones Windows can create: no device names such as `CON` or `nul.txt`, no names ending in a dot or a space,
none of `<>:"|?*\`, and no two that differ only in case. `check` enforces this on every system. Discovery skips
`Thumbs.db` and `desktop.ini`, which Explorer creates on its own.

### Manifests

A repository for Windows alone says so once, with `"platforms": ["windows"]` in either manifest, and that covers every
file and step, including the files found under `home/` ([manifest.md](manifest.md#one-file-or-two) explains how the two
manifests merge). Steps on Windows follow a few rules of their own:

- Run PowerShell scripts with `-File`, as in `["pwsh", "-NoProfile", "-File", "setup/install-tools.ps1"]`. Its arguments
  reach the script as written, so the review shows exactly what runs; a `-Command` string is parsed again by PowerShell.
- `pwsh` is PowerShell 7. A first step can install it with winget, using `"provides": "pwsh"`, so that later steps can
  run it; Windows PowerShell, `powershell`, is always present.
- Commands are found through `PATHEXT`, so `winget` finds `winget.exe`, and `provides` names a command without its
  extension. Programs that winget, or any installer, adds to the `PATH` in the registry are found by the steps after it
  ([Setup Steps](#setup-steps)).
- `creates` is relative to `%USERPROFILE%`, or starts with `{documents}/` for your Documents folder, wherever OneDrive
  has put it.

`bootstrap.json`:

```json
{
  "platforms": ["windows"],
  "setup": [
    { "name": "powershell",
      "run": ["winget", "install", "--id", "Microsoft.PowerShell", "--exact",
              "--accept-source-agreements", "--accept-package-agreements"],
      "provides": "pwsh" },
    { "name": "tools", "run": ["pwsh", "-NoProfile", "-File", "setup/install-tools.ps1"] }
  ]
}
```

`dotfiles.json`:

```json
{
  "setup": [
    { "name": "profile", "run": ["pwsh", "-NoProfile", "-File", "setup/init-profile.ps1"],
      "creates": "{documents}/PowerShell/profile.ps1" }
  ]
}
```

A step without `creates` or `provides`, like `tools` here, is offered on every run, so its script must be safe to
repeat: install only what is missing. Give it `"inputs": ["home/.config/winget/tools.psd1"]`, the list it installs from,
and it is offered only until it succeeds, then again when the list changes
([manifest.md](manifest.md#steps-that-install-from-a-list)).

### Checking a Repository

`homeskillet check` lists every issue in a repository, for every platform, without looking at a home
([manifest.md](manifest.md#checking-one)). On Windows, run it on the clone itself. From Linux, macOS, or WSL, clone the
repository onto that system's own file system, then:

```sh
homeskillet check ~/dotfiles-windows
homeskillet status ~/dotfiles-windows --platform windows --home "$(mktemp -d)"
homeskillet setup ~/dotfiles-windows --platform windows --dry-run
```

`status` with `--platform windows` links into nothing: it shows that every file is found and every target is valid.
Setup steps for another operating system can only be listed, with `--dry-run`. A repository's `home/wsl/`, and its Linux
steps, are checked the same way with `--platform linux`, with no WSL needed, as in CI; on Linux, `link` with a scratch
`--home` links them for real.

## Use

Commands are the same as elsewhere, with Windows paths:

```powershell
homeskillet check $HOME\projects\dotfiles-windows
homeskillet link $HOME\projects\dotfiles-windows
homeskillet setup $HOME\projects\dotfiles-windows
```

In place of a command, and after `setup`, a repository must plainly be a path: `.`, `..`, or containing `/`, `\`, or a
drive, such as `.\dotfiles` or `C:dotfiles`. A path may start with `~\` as well as `~/`, in `cmd` too, where no shell
expands it. Paths and file names are compared without regard to case, as Windows compares them. [usage.md](usage.md)
covers the rest.

## How Windows Differs

### Ownership and Permissions

Windows has no owner IDs or mode bits; each file has a security descriptor naming its owner and an access control list
saying who may do what. The rules stay those of the other systems, in Windows terms:

- Trusted are you, `SYSTEM`, `Administrators`, and `TrustedInstaller`, as you and root are elsewhere. Windows itself
  gives the profile directory, and some others, to one of the last three.
- A repository, its files, the home, the data directory, and every directory a link goes into must have a trusted owner
  and grant nobody else any right to change them, their entries, or who may access them.
- A directory above them may also let others add files and folders, which is what a drive's root lets every user do, but
  not delete or rename what is there, as a sticky directory such as `/tmp` allows elsewhere.
- Rules that only deny, and rules that apply only to files created later, are ignored; an entry of a kind homeskillet
  cannot evaluate fails the check.

The error names the account that may write, such as `BUILTIN\Users`.

### Junctions and Other Reparse Points

A junction redirects every path through it, as a symlink does, but Windows software, Go included, often does not treat
one as a link. homeskillet refuses a junction, and any other reparse point that is not a directory, wherever it checks
ownership: in the repository, the home, the data directory, and the directories above them and on the way to a target. A
path through one would reach files that no check saw. OneDrive folders are directories and pass; OneDrive files are
placeholders and do not.

### System Locations

Besides `/` and the like on other systems, homeskillet refuses the root of any drive or network share; the folder
holding every profile, usually `C:\Users`; the Windows directory; Program Files, both kinds; ProgramData; the Public and
Default profiles; each drive's `$Recycle.Bin` and `System Volume Information`; device paths starting with `\\?\` or
`\\.\`; and a WSL distribution's files under `\\wsl.localhost` or `\\wsl$`, for which Windows reports no owner. It asks
Windows where each is rather than assuming `C:`.

### Setup Steps

- Commands are found through `PATHEXT`, so `winget` finds `winget.exe`, and a name with one of its extensions is used as
  written. As elsewhere, relative `PATH` entries are skipped, and so is the current directory, which Windows's own
  search tries first.
- Steps get `USERPROFILE` as well as `HOME` set to the selected home.
- Installers such as winget record the directories they add to `PATH` in the registry, where only programs started later
  read them. So before each step, homeskillet appends the system's and your `PATH` as the registry holds them, then the
  home's `.local\bin` and winget's `AppData\Local\Microsoft\WinGet\Links`, each only when it is not listed already and
  passes the ownership rules. A step can then run what the step before it installed, such as `pwsh` right after winget
  installs PowerShell. Nothing is ever put first.
- Under `--yes`, a step gets no input and runs in a process group and a job object of its own. Unlike on Linux and macOS
  it keeps the console: Windows's `sudo` asks through a User Account Control dialog each time, and never uses a password
  typed for something else. Ctrl-C is passed on to the step as Ctrl-Break; whatever the step started that still runs
  five seconds later is stopped through the job, and so is everything when setup is canceled.
- A Windows step that runs something inside WSL itself, through `wsl.exe`, keeps that console too, so Linux `sudo` there
  can still ask for a password under `--yes`. Its standard input is not a terminal, though, so a script can test `[ -t 0
  ]` and fail with a message instead. Steps for Linux, run by `homeskillet wsl setup`, have no such gap: the Linux build
  runs them as on any Linux ([Windows and WSL Together](#windows-and-wsl-together)).

### The Documents Folder

PowerShell reads profiles only from your Documents folder, which OneDrive moves into itself when it backs up Documents,
as to `%USERPROFILE%\OneDrive\Documents`. A step's `creates` may therefore start with `{documents}/`
([manifest.md](manifest.md#setup-steps)), as the `profile` step above does.

For your own profile, homeskillet asks Windows where Documents is. For any other home, such as a scratch one given with
`--home`, and on Linux and macOS, it is the `Documents` folder in that home, so a trial run never looks at your real
one. homeskillet only checks whether the file exists; the step writes it. A Documents folder moved outside the home must
pass the ownership rules too. Link targets are not mapped: a file under `home/Documents` is linked into
`%USERPROFILE%\Documents`.

### Installing homeskillet

Windows will not rename one link over another that leads to a directory, so the [layout](install.md) differs. The
command, `.local\bin\homeskillet.exe`, links straight to the chosen version's `homeskillet.exe`, and choosing another
version renames a new link over it, which Windows allows even while the old version runs. `current` is a small file
naming `latest` or a version, and `latest` and `previous` are worked out from the installed versions rather than kept as
links.

## Windows and WSL Together

Windows and a WSL distribution are two systems, each with its own home, and homeskillet treats them that way: the
Windows build links your Windows profile, the Linux build inside the distribution links its Linux home, and nothing is
ever linked from one into the other.

| | Windows | WSL |
|---|---|---|
| Build | `homeskillet.exe`, Windows | `homeskillet`, Linux, for the distribution's architecture |
| Command | `%USERPROFILE%\.local\bin\homeskillet.exe` | `~/.local/bin/homeskillet` |
| Home | `C:\Users\you` | `/home/you` |
| Data directory, record, versions | `%USERPROFILE%\.local\share\homeskillet` | `~/.local/share/homeskillet` |

### Two Ways to Configure WSL

**One repository for both.** A Windows repository keeps WSL's files in `home/wsl/` ([Layout](#layout)) and WSL's setup
steps beside its others, with `"platforms": ["linux"]`, run from the repository's root as anywhere else:

```json
{ "name": "go", "platforms": ["linux"], "run": ["bash", "home/wsl/.local/share/helpers/install-go.sh"],
  "creates": "~/.local/share/go/bin/go" }
```

You keep one clone, on Windows, and drive WSL from it:

```powershell
homeskillet wsl install              # the Linux build, once per version
homeskillet wsl link .               # link home/wsl/ into the WSL home
homeskillet wsl setup .              # offer the Linux steps there
```

**A repository of its own.** A Linux repository, with only `home/`, such as `dotfiles-wsl` or `dotfiles-linux`, is
cloned inside the distribution, and the Windows repository has no `home/wsl/`. Run homeskillet there as on any Linux, or
from Windows, naming the clone as a path in the distribution:

```powershell
homeskillet wsl link '~/dotfiles-wsl'
homeskillet wsl setup /home/you/dotfiles-wsl
```

Write `~` in quotes from PowerShell, which can turn `~/dotfiles-wsl` into a Windows path first; if that path is not on
Windows, the error says so.

### The wsl Command

`homeskillet wsl install|status|link|unlink|setup` runs on Windows, or in a WSL shell ([below](#from-a-wsl-shell)), and
acts in a distribution, WSL's default unless `--distribution NAME` names another:

- **install** puts homeskillet's Linux build, of the same version, in the distribution. A release downloads
  `homeskillet-linux-<arch>` with Windows's `curl`, for the distribution's architecture, and checks it against the
  release's `SHA256SUMS` inside the distribution; a development build installs the one `make cross` built, given with
  `--from bin\homeskillet-linux-amd64`. The build is copied into a private temporary directory there, checked to have
  arrived whole and to run as this version, and installs itself with `self install`, as on any Linux. Then it makes the
  command there run it, as the other `wsl` commands need: following `latest` when it is the newest, which also ends an
  earlier pin, and pinned, with a note saying so, when another build ranks above it, as a clean build ranks above a
  `+dirty` one of the same version ([install.md](install.md#versions-and-updates)). What `wsl.exe` starts reads no shell
  startup file, so install cannot tell whether your shells there put `~/.local/bin` on `PATH`; its note says what to add
  if a new shell cannot run `homeskillet`. Last, it records itself there, in `windows-build.json` in the data directory,
  as the Windows build `wsl` in that distribution hands over to: your installed `homeskillet.exe` when it is the one
  running, so the record follows the version you choose on Windows, or else the program that ran, such as a development
  build.
- **status, link, unlink, and setup** run the Linux build in the distribution, which must be this version: the one in
  `--home`'s `.local/bin`, if a home is given and has one, as `wsl install --home` puts it there, or else the one in
  your own home there. A repository written `/` or `~` first is a clone there and is used as it is. With none named, it
  is the current directory or, as for the other commands, the Windows repository linked before, so `homeskillet wsl
  link` alone works from a WSL shell. Any other is a Windows clone: after every check the other commands make, it is
  copied into a private directory in the distribution's data directory, `mirrors/`, and the command runs on the copy.
  WSL cannot link from the Windows clone itself, since Windows drives under `/mnt` show every file as writable by
  everyone.
- **The copy** holds every file and folder of the clone but `.git`, each checked before it is sent, with the executable
  bit Git records. `link`, `unlink`, and `setup` update it first, building the new copy beside the old and swapping it
  in only once all of it has arrived, and say so on standard error every time: copied, updated with how many files were
  added, changed, and removed, or up to date, so a missing line means no update. `status` and `--dry-run` change
  nothing: they use the copy as it is and say when it differs from the clone, or a temporary copy when there is none
  yet. Links in WSL lead into the copy, so edit the Windows clone, and `homeskillet wsl link` brings the change across.
  Keep shell scripts with LF line endings, as a `.gitattributes` with `*.sh text eol=lf` does, since the copy has the
  clone's bytes.
- **Options** other than `--distribution` and `--from` go to homeskillet in the distribution and mean what they mean
  there: `--home` and `--data-dir` name Linux directories, and files and step names after the repository are as for the
  command itself. `--data-dir` also says where the copy is, so give the same one to every `wsl` command; `setup`, which
  keeps nothing there, uses it only to find the copy.

`wsl` is built not to fail on WSL's own hiccups. It reads the distributions from the registry, where WSL records them,
rather than from `wsl.exe`'s output; starts the distribution with a command that changes nothing, given two minutes and
retried twice, as WSL can be slow to answer from cold, and retries nothing else, so no command ever runs twice; and runs
each command with `--exec`, directly, so no shell in the distribution reads its arguments.

### From a WSL Shell

`homeskillet wsl` works from a WSL shell as from Windows: the Linux build hands it to the Windows build `wsl install`
recorded (see install above), with the same arguments and `--distribution` set to this distribution, and says `running
the Windows build, C:\...\homeskillet.exe` on standard error, unless `--quiet`. It runs only that program, never a
`homeskillet.exe` found on `PATH`, where WSL puts Windows's directories in an order you do not see. A repository or
`--from` given as a path on a Windows drive, such as `/mnt/c/...` or `.` there, goes as Windows names it, by WSL's
`wslpath`; a repository elsewhere is a clone in the distribution. With no Windows build recorded, or WSL's interop with
Windows turned off in `/etc/wsl.conf`, it fails and says to run `homeskillet wsl` from Windows; it never falls back to
something else. On Linux elsewhere and on macOS, `wsl` is refused.

Run without `wsl`, `homeskillet` is the Linux build, acting on the distribution as on any Linux. It acts on the copy as
on any repository, but never updates it: `homeskillet link` in WSL, with the copy as the repository it linked before,
relinks the copy as it is. At a terminal its header says so, with a `copy of:` line naming the Windows clone and when
the copy was last updated. To bring over changes made since, run `homeskillet wsl link`.

As WSL adds Windows's `PATH` by default, `homeskillet.exe` runs the Windows build directly, acting on your Windows home.
Started in a WSL directory, which it will not act on, it uses the repository it linked before. WSL passes standard input
on to Windows programs, so in a script give `homeskillet.exe`, or `homeskillet wsl`, `</dev/null`, or it reads the
script's own input.

## Limits

- A scratch home moves `HOME` and `USERPROFILE`, but programs that ask Windows where folders such as AppData are, or
  read `%APPDATA%`, still find your real ones, and a Windows step that calls into WSL itself uses the real WSL home. Try
  steps from someone else's repository in a virtual machine or Windows Sandbox instead.
- The copy in WSL holds only files and folders; a repository with a symlink is refused there, with the symlink named.
- An editor or installer that replaces a file rather than writing it turns a link into a copy, as on other systems;
  manage such files with a setup step.

`dotfiles-windows` is the configuration repository this support is tested with, natively, from WSL, and from Linux.
