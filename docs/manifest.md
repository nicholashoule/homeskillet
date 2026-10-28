# Configuration Repositories

What a configuration repository holds: a `home/` directory, a manifest, or
both. [usage.md](usage.md) covers running homeskillet on one.

## The home/ Directory

The simplest repository needs no manifest. Put files under a `home/` directory
at its root, laid out as they belong in your home directory:

```text
dotfiles/
└── home/
    ├── .bashrc                          -> ~/.bashrc
    ├── .bashrc.d/aliases.sh             -> ~/.bashrc.d/aliases.sh
    └── .config/git/config               -> ~/.config/git/config
```

Every file under `home/` is linked on its own. Directories are created as
needed and never linked themselves, so other files in `~/.config` are left
alone. homeskillet skips `.DS_Store`, which macOS Finder creates, `Thumbs.db`
and `desktop.ini`, which Windows Explorer creates, and `.git`, which holds a
nested repository's metadata, whatever their case. `home/` may be a symlink to
another directory inside the repository, for example to choose between
per-machine layouts. A Windows repository may keep a WSL distribution's home in
`home/wsl/` ([A Home for WSL](#a-home-for-wsl)).

Each shell reads only its own startup files, as zsh, the macOS default, reads
`.zshrc` and `.zprofile` and not the bash files, so a repository for both keeps
both.

## The Manifest

A manifest at the root of a configuration repository, `bootstrap.json`,
`dotfiles.json`, or both, is optional. A repository with only a `home/`
directory works without one: every file under `home/` is linked to the same
path in your home directory. A manifest adds what `home/` alone cannot say:

| Field | What it is for |
|---|---|
| `platforms` | The operating systems, and optionally architectures, the repository is for. Files and steps for other platforms are left alone. |
| `setup` | Commands that install what the linked files use, such as a prompt or a font. homeskillet shows each one and runs it only when you approve it. |
| `files` | An explicit list of files to link, in place of everything under `home/`: for a target that differs from the source's layout, or a file meant for only some platforms. |

Any field may be left out. A manifest that omits `files` keeps linking
everything under `home/`, so most repositories use the manifest only for
`platforms` and `setup`. A repository with neither `home/` nor a manifest is
refused, because that usually means the repository path names the wrong
directory.

## One File or Two

`bootstrap.json` is the name `homeskillet init` gives a manifest, because a
manifest does more than list dotfiles: its setup steps install the tools they
use. `dotfiles.json`, the name it first had, is read the same way, so existing
repositories keep working. Both take the same fields.

A repository may keep both, to keep each short and its purpose plain, such as
the files to link and the steps that create files in `dotfiles.json`, and the
steps that install tools in `bootstrap.json`. homeskillet reads
`bootstrap.json`, then `dotfiles.json`, and merges them:

- `platforms`: the values in either apply to everything in both.
- `files`: the entries of both. When neither lists `files`, files are found
  under `home/`; an empty list in either turns that off.
- `setup`: `bootstrap.json`'s steps, then `dotfiles.json`'s, in that order.
- An entry or step in both, the same in every field, counts once. Two
  different steps with the same name are an error, rather than one silently
  replacing the other: steps are chosen by name, and the one left out could be
  the one you just edited.

`bootstrap.json`:

```json
{
  "platforms": ["windows"],
  "setup": [
    { "name": "winget-tools", "run": ["pwsh", "-NoProfile", "-File", "setup/install-tools.ps1"] }
  ]
}
```

`dotfiles.json`, whose files are still found under `home/`, as it lists none:

```json
{
  "setup": [
    { "name": "profile", "run": ["pwsh", "-NoProfile", "-File", "setup/init-profile.ps1"],
      "creates": "{documents}/PowerShell/profile.ps1" }
  ]
}
```

## Creating One

`homeskillet init` writes a starter `bootstrap.json` into a repository: this
machine's operating system as the default platform, and an empty `setup` list.
It leaves out `files`, so files keep being found under `home/`, which it
creates when missing:

```sh
homeskillet init ~/dotfiles                       # write ~/dotfiles/bootstrap.json
homeskillet init ~/dotfiles --dry-run             # print it instead
homeskillet init ~/dotfiles --platform darwin     # a starter for a macOS repository
```

```json
{
  "platforms": ["linux"],
  "setup": []
}
```

`init` writes nothing into a repository that has a manifest under either name
([safety.md](safety.md#starter-manifests-checks-and-completion)). Remove `platforms`
to apply everywhere.

## Checking One

`homeskillet check` reads a repository as the other commands do, for every
platform at once, and lists every issue rather than stopping at the first:
JSON that does not parse, unknown fields, paths that leave the repository or
the home, unknown platforms, invalid step names, two manifests that disagree,
names Windows cannot create, files listed but missing, and a repository or
manifest that others could change. It changes nothing and never looks at a
home directory, so it suits a pre-commit hook or CI. `status` then says what
linking would change in a home.

```text
homeskillet check
  repository: /home/you/dotfiles
  manifests:  bootstrap.json, dotfiles.json

Issues (2)
  bootstrap.json setup step 2: name must be lowercase letters, digits, '.', '_', or '-', starting with a letter or digit
  bootstrap.json and dotfiles.json: setup step tools is listed twice, differently; keep one

Not valid: link, status, and setup refuse this repository until these are fixed.
```

A valid repository ends with what it holds, such as `Valid: 3 files and 2
setup steps, for linux.` In a pipe or script, `check` prints `valid` and the
repository, or one `issue` line per issue, tab-separated, and exits nonzero
when there are issues.

## Files

When the manifest lists `files`, exactly those are linked and `home/` gets no
special treatment:

```json
{
  "files": [
    { "source": "home/.bashrc", "target": ".bashrc" },
    {
      "source": "home/.config/example/config.toml",
      "target": ".config/example/config.toml",
      "platforms": ["linux"]
    }
  ]
}
```

Sources are relative to the configuration repository. Targets are relative to
the selected home directory. All sources must be regular files; no particular
source directory layout is required. Files not listed are ignored, so a file
added under `home/` later needs an entry too. `"files": []` links nothing, for
a repository that only declares setup steps.

## Paths in the Home

A target, and a step's `creates`, is a path in the home, written the same way
on every system with `/`: `.config/git/config` is `~/.config/git/config` on
Linux and macOS and `%USERPROFILE%\.config\git\config` on Windows. It may also
start with `~/`, `$HOME/`, or `${HOME}/`, as a shell would write it; when the
manifest is read, each becomes the plain path, so `"~/.bashrc"` and
`"$HOME/.bashrc"` are both `".bashrc"`. These are fixed spellings, not
variables: homeskillet expands no variables, so any other start, such as
`$XDG_CONFIG_HOME/`, `%USERPROFILE%\`, or `~user/`, is refused rather than
taken as a folder of that name, and a source, relative to the repository, may
start with none of them. `run` is never changed: a step gets `HOME` set, and
reads it itself.

## A Home for WSL

A repository may keep the files of a WSL distribution's home in `home/wsl/`,
beside the Windows profile's in the rest of `home/`. When it has `home/wsl/`,
those files apply to Linux alone, whatever `platforms` says, and are never
linked from `home/` as well; `home/`'s other files apply to the manifest's
other systems. Without `home/wsl/`, `home/` is the home on every system the
manifest covers, as in a Linux repository with only `home/`.
[windows.md](windows.md#windows-and-wsl-together) shows both ways of
configuring WSL, and how `homeskillet wsl` links `home/wsl/` there.

## Platforms

A repository for one operating system says so once, at the top of its
manifest, and that applies to every file and setup step, including files
found under `home/`:

```json
{
  "platforms": ["darwin"],
  "setup": [
    { "name": "homebrew", "run": ["bash", "setup/homebrew.sh"], "provides": "brew" },
    { "name": "rosetta", "run": ["softwareupdate", "--install-rosetta", "--agree-to-license"],
      "platforms": ["darwin/arm64"] }
  ]
}
```

A platform is an operating system, `linux`, `darwin`, or `windows`, optionally
with an architecture, `amd64` or `arm64`, as in `darwin/arm64`. Without an
architecture it covers both. Files and steps meant for Windows must use names
Windows can create, which is checked on every system (see
[windows.md](windows.md#layout)). An entry or step that
sets its own `platforms` replaces the manifest's default rather than narrowing
it; to apply everywhere despite a narrower default, list both operating
systems. Omit `platforms` entirely to apply everywhere. Unknown values are
rejected, so a typo such as `macos` or `darwin/arm` fails instead of silently
matching nothing.

homeskillet detects the platform it runs on, its operating system and
architecture (see `homeskillet version`). If the repository has files or steps
but none for that platform, `status`, `link`, and `setup` fail and say so,
rather than succeeding without doing anything; `unlink` still runs, so links
made before can be removed. Detection is best effort, so `--platform`
overrides it:

```sh
homeskillet setup . --platform darwin/amd64           # Intel steps on Apple silicon, under Rosetta
homeskillet link . --home "$(mktemp -d)" --platform darwin   # check a macOS repository from Linux
homeskillet setup . --platform darwin --dry-run       # list another OS's steps
```

Setup steps for another operating system can only be listed, never run, so
`setup` with a foreign `--platform` requires `--dry-run`.

## Setup Steps

Setup steps are declared only in a manifest; homeskillet never infers a
command. This manifest declares setup steps and, because it omits `files`,
still links everything under `home/`:

```json
{
  "setup": [
    {
      "name": "uv",
      "run": ["sh", "setup/uv.sh"],
      "platforms": ["linux", "darwin"],
      "creates": ".local/bin/uv"
    },
    {
      "name": "brew-tools",
      "run": [
        ["brew", "install", "ripgrep", "jq"],
        ["brew", "install", "--cask", "font-meslo-lg-nerd-font"]
      ],
      "platforms": ["darwin"],
      "provides": "rg"
    }
  ]
}
```

One repository serves both systems: on Linux `setup` offers uv, and on macOS
uv and then the Homebrew tools.

| Field | Meaning |
|---|---|
| `name` | Required. Lowercase letters, digits, `.`, `_`, or `-`, starting with a letter or digit; unique. |
| `run` | Required. One command, `["brew", "install", "jq"]`, or a list of commands, `[["brew", "update"], ["brew", "install", "jq"]]`. Each runs directly, not through a shell; use `["sh", "-c", "..."]` when a command needs pipes or other shell features. |
| `platforms` | Optional. Replaces the manifest's default `platforms` for this step (see [Platforms](#platforms)). |
| `creates` | Optional path in the home ([Paths in the Home](#paths-in-the-home)), or starting with `{documents}/`, relative to your Documents folder, where PowerShell keeps its profile ([windows.md](windows.md#the-documents-folder)). If it exists, the step is done and not offered. |
| `provides` | Optional command name, without `/`, `\`, or `:`. If it is on `PATH`, in the home's `.local/bin`, or in one of Homebrew's standard `bin` directories (see below), the step is done and not offered. |
| `inputs` | Optional list of files in the repository the step works from, such as a list of packages. The step is done once it has run and succeeded with them as they are, and is offered again when one changes ([Steps That Install From a List](#steps-that-install-from-a-list)). |

A list of commands groups work that belongs together into one step: they are
shown together, approved once, and run in order, and the first that fails
stops the step and setup.

### Steps That Install From a List

A step that installs whatever a file lists, such as apt packages, a Brewfile,
or winget's tools, has nothing for `creates` or `provides` to find: a marker
file it left would stay when a package is added to the list, and the new
package would never be installed. List the files it works from as `inputs`:

```json
{
  "name": "apt-packages",
  "run": ["bash", "setup/install-apt-packages.sh", "lists/apt-packages"],
  "platforms": ["linux"],
  "inputs": ["lists/apt-packages", "setup/install-apt-packages.sh"]
}
```

When the step runs and succeeds, homeskillet records a fingerprint of its
commands and of each input's contents in its data directory, as `setup.json`,
for that home and repository. The step is then done until a command or an
input changes, and the listing says `inputs unchanged since it ran` and when.
Contents decide, not times, so a fresh clone, or the copy `homeskillet wsl`
keeps, is unchanged. With `creates` or `provides` as well, both must say the
step is done. Each input must be a file in the repository, as `check` makes
sure; a step that fails is not recorded, so it is offered again. Deciding still
runs nothing, and a recorded step can only go unoffered, never run.

### Running Steps

```sh
homeskillet setup ~/dotfiles                  # offer every pending step
homeskillet setup ~/dotfiles uv brew-tools    # only these steps
homeskillet setup uv                          # one step of the default repository
homeskillet setup ~/dotfiles --dry-run        # list every step and its state
```

Names after `setup` select steps, which still run in manifest order, since a
later step may rely on an earlier one, and are offered even when done, so
naming a step runs it again, as after uninstalling a package by hand. A name
matching no step for this platform fails before anything runs. A repository given before them must
plainly be a path (`.`, `..`, or containing `/`, or on Windows `\`), as a step
name never is, so write `./dotfiles` rather than `dotfiles`.

Every step is listed. A step that is done says what shows it, so you can tell
why it was not offered:

```text
Pending (1): shown in full and asked about before each runs
  dotnet-sdk  bash setup/install-dotnet.sh

Done (1)
  gitconfig   /home/user/.gitconfig

Dry run: nothing changed; setup would offer 1 step.
```

That is a dry run at a terminal. In a pipe or script, each step's state is one
tab-separated line: `done`, `pending`, `ran`, or `skipped`, the name, and for
a done step the same detail. When a step is about to run, it is shown in full
first, every command on its own line with the working directory, and nothing
runs until you answer `y`:

```text
pending  dotnet-sdk
  run: bash setup/install-dotnet.sh
  in:  /home/user/dotfiles
Run this step? [y/N]
```

Steps run from the repository root, so repository scripts such as
`setup/uv.sh` work. `HOME` and `HOMESKILLET_HOME` are set to the selected
home, and on Windows `USERPROFILE` too, and `HOMESKILLET_REPO` to the
repository. A step without `creates`, `provides`, or `inputs` is offered on
every run, so make it safe to repeat. A failing step
stops setup; declined steps are skipped and the rest are still offered.
`setup --yes` runs every pending step without asking, for unattended builds
such as a Dockerfile; [safety.md](safety.md#setup-steps) explains what that
does and does not allow.

### PATH

Many installers, such as uv's and pipx's, put programs in `~/.local/bin`, which
a fresh shell often does not have on `PATH` until the next login. So that a
step, or a later command in the same step, can run a program installed just
before it, commands run with the selected home's `.local/bin` appended to
`PATH`, and `provides` and bare command names are looked up the same way. It is
appended, so it cannot shadow a system command such as `bash` or `curl`, and
only when it passes the ownership rules, so a directory others can write never
joins `PATH`. Relative `PATH` entries are never searched. On Windows, commands
are found through `PATHEXT`, and the `PATH` installers record in the registry
and winget's links directory are appended as well
([windows.md](windows.md#setup-steps)).

`provides` also looks in Homebrew's `bin` directories, which shells add to
`PATH` only after `brew shellenv`, so a program Homebrew just installed is not
offered again. Commands do not get them on `PATH`, since Homebrew usually lets
the `admin` group write them; call `brew` by its full path right after
installing it.
