# homeskillet

[![CI](https://github.com/nicholashoule/homeskillet/actions/workflows/ci.yml/badge.svg)](https://github.com/nicholashoule/homeskillet/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/nicholashoule/homeskillet.svg)](https://pkg.go.dev/github.com/nicholashoule/homeskillet)
[![Go Version](https://img.shields.io/github/go-mod/go-version/nicholashoule/homeskillet)](go.mod)
[![License](https://img.shields.io/github/license/nicholashoule/homeskillet)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen)](go.mod)

A small, dependency-free Go CLI for bootstrapping anyone's dotfiles on Linux
and macOS. Configuration belongs in separate repositories. homeskillet links
the files a repository keeps under `home/`, or the files its optional
`dotfiles.json` manifest lists. After you approve each one, it also runs the
setup steps the manifest declares, such as installing a prompt, fonts, or
language tools. It does not ship or assume any particular dotfiles or tools.

## Supported Environments

homeskillet creates symlinks and runs setup steps directly rather than through
your shell, so it works the same whatever your login shell is.

| Environment | Notes |
|---|---|
| Linux with bash | Any distribution. Tested. |
| macOS with bash or zsh | zsh is the macOS default and reads `.zshrc`/`.zprofile`, not the bash files. Each shell reads only its own startup files, so a manifest can list both. Cross-compiled but not yet tested natively. |
| Windows via WSL (Ubuntu) | Run homeskillet inside WSL. Clone the configuration repository into the Linux filesystem (for example under `~/`), not under `/mnt/c`. Windows drives show every file as world-writable and ignore `chmod` by default, so homeskillet refuses them. |
| Docker and other Linux containers | Containers often run as root, which needs `--allow-root` and a repository owned by root inside the container. A repository bind-mounted from the host belongs to another user and is refused for root. Alternatively, run as a regular user that owns the clone and has a real home directory. Builds that exclude `.git` record no version; pass `make build VERSION=v0.0.1`. |

Native Windows (cmd.exe or PowerShell) is not supported at this time: a
Windows build only prints a message saying so and suggesting WSL. A
configuration repository can already target Windows and be checked from Linux
or macOS with `--platform windows`; [docs/windows.md](docs/windows.md) has
what works and what native support still needs.

## Build and Install

With Go 1.24 or newer, from this repository:

```sh
make install          # build, then install as a new version under ~/.local
homeskillet version
```

`make install` builds `bin/homeskillet` and runs its `self install`, which
copies the binary into a directory of its own version and points the
`homeskillet` command at it (see [Versions and Updates](#versions-and-updates)).
The command lands in `~/.local/bin`; homeskillet says so if that directory is
not on your PATH, which on macOS it usually is not. `make install PREFIX=<dir>`
installs under another prefix.

`make build` alone leaves the binary in `bin/` without installing it. Both
produce a static, stripped binary of about 2.5 MiB that runs on any Linux
distribution or macOS release for its architecture. Run `make` to list every
target; the Makefile works with macOS's GNU Make 3.81 and on any Linux, WSL,
or container ([docs/development.md](docs/development.md)).

### Versions and Updates

Each install keeps its own copy in the [data directory](#data-directory), and
`latest`, `previous`, and `current` links choose which one runs, so updating is
`git pull` then `make install`, and rolling back is
`homeskillet self use previous`. `homeskillet version` prints the version and
build details, and `homeskillet self list` the installed versions.
[docs/install.md](docs/install.md) covers version stamping, pinning, removal,
and the rules for replacing an installed version.

## Use

Pass a configuration repository, and optionally files in it:

```sh
homeskillet ~/dotfiles                        # status: what link would change
homeskillet link ~/dotfiles --dry-run         # preview; nothing changes
homeskillet link ~/dotfiles                   # link every file
homeskillet link ~/dotfiles .bashrc           # link only ~/.bashrc
homeskillet link ~/dotfiles .bashrc --force   # replace ~/.bashrc, keeping a backup
homeskillet unlink ~/dotfiles
homeskillet setup ~/dotfiles
```

Files after the repository limit `status`, `link`, and `unlink` to them: a
target (`.bashrc`), a source (`home/.bashrc`), or a directory of either
(`.bashrc.d`). The repository defaults to the current directory or, when that
is not one, to the one repository already linked into the home, so after the
first link, `homeskillet link` alone works anywhere. Options go anywhere after
the command. Without a command, `status` runs, and the repository must plainly
be a path (`.`, `..`, or containing `/`), so a mistyped command is never read
as a directory. `--home` selects another home, `--dry-run` changes nothing,
`--quiet` prints nothing but errors (not for `setup`, whose steps must be
shown), and `setup --yes` runs every pending step without asking, for
unattended builds such as a Dockerfile.

`link` never replaces a file it did not create. With `--force` it moves each
conflicting file (never a directory) into the data directory's `backups/`, at
its path with the Unix time appended (`backups/.bashrc.1761393600`), then
links; move a backup back to restore it. In a terminal, homeskillet says what
it is running and where, groups files by what happens to them, and ends with
what changed or what to do; in a pipe or script it prints one tab-separated
line per file, a stable format to parse. Keep the clone in a permanent place:
links point into it.

### Data Directory

homeskillet keeps its installed versions, `links.json` (the record of links it
made), and `--force` backups in `~/.local/share/homeskillet`. Set
`HOMESKILLET_DATA_DIR` or pass `--data-dir` to keep them elsewhere;
[docs/install.md](docs/install.md#data-directory) has the rules.

## Choosing Files

### The `home/` Directory

The simplest configuration repository needs no manifest. Put files under a
`home/` directory at its root, laid out as they belong in your home directory:

```text
dotfiles/
└── home/
    ├── .bashrc                          -> ~/.bashrc
    ├── .bashrc.d/aliases.sh             -> ~/.bashrc.d/aliases.sh
    └── .config/git/config               -> ~/.config/git/config
```

Every file under `home/` is linked on its own, on both operating systems.
Directories are created as needed and never linked themselves, so other files
in `~/.config` are left alone. homeskillet skips `.DS_Store`, which macOS
Finder creates, `Thumbs.db` and `desktop.ini`, which Windows Explorer creates,
and `.git`, which holds a nested repository's metadata, whatever their case. `home/`
may be a symlink to another directory inside the repository, for example to
choose between per-machine layouts.

A repository with neither `home/` nor a manifest is refused, because that
usually means the repository path names the wrong directory.

### The Manifest

For more control, place `dotfiles.json` at the repository root. When it lists
`files`, exactly those are linked and `home/` gets no special treatment:

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
source directory layout is required. Files not listed are ignored.

### Platforms

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
with an architecture, `amd64` or `arm64`, as in `darwin/arm64`. homeskillet
does not run on Windows yet, but a repository may target it already; files
and steps meant for Windows must use names Windows can create (see
[docs/windows.md](docs/windows.md)). Without an
architecture it covers both. An entry or step that sets its own `platforms`
replaces the manifest's default rather than narrowing it; to apply everywhere
despite a narrower default, list both operating systems. Omit `platforms`
entirely to apply everywhere. Unknown values are rejected, so a typo such as
`macos` or `darwin/arm` fails instead of silently matching nothing.

homeskillet detects the platform it runs on, its operating system and
architecture (see `homeskillet version`). If nothing in the repository applies
to that platform, `status`, `link`, and `setup` fail and say so, rather than
succeeding without doing anything; `unlink` still runs, so links made before
can be removed. Detection is best effort, so `--platform` overrides it:

```sh
homeskillet setup . --platform darwin/amd64           # Intel steps on Apple silicon, under Rosetta
homeskillet link . --home "$(mktemp -d)" --platform darwin   # check a macOS repository from Linux
homeskillet setup . --platform darwin --dry-run       # list another OS's steps
```

Setup steps for another operating system can only be listed, never run, so
`setup` with a foreign `--platform` requires `--dry-run`.

A manifest that omits `files` keeps linking everything under `home/`, which
lets a repository use the manifest only for setup steps. `"files": []` links
nothing.

### Setup Steps

Setup steps are declared only in the manifest; homeskillet never infers a
command. This manifest declares setup steps and, because it omits `files`,
still links everything under `home/`:

```json
{
  "setup": [
    {
      "name": "uv",
      "run": ["sh", "setup/uv.sh"],
      "platforms": ["linux"],
      "creates": ".local/bin/uv"
    },
    {
      "name": "ripgrep",
      "run": ["brew", "install", "ripgrep"],
      "platforms": ["darwin"],
      "provides": "rg"
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `name` | Required. Lowercase letters, digits, `.`, `_`, or `-`; unique. |
| `run` | Required. The command and its arguments, run directly, not through a shell. Use `["sh", "-c", "..."]` when a step needs pipes or other shell features. |
| `platforms` | Optional. Replaces the manifest's default `platforms` for this step (see [Platforms](#platforms)). |
| `creates` | Optional path relative to home. If it exists, the step is done and not offered. |
| `provides` | Optional command name. If it is on `PATH`, in the home's `.local/bin`, or in one of Homebrew's standard `bin` directories (see below), the step is done and not offered. |

Steps run in manifest order from the repository root, so repository scripts
such as `setup/uv.sh` work. `HOME` and `HOMESKILLET_HOME` are set to
the selected home, and `HOMESKILLET_REPO` to the repository. A step without
`creates` or `provides` is offered on every run, so make it safe to repeat. A
failing step stops setup; declined steps are skipped and the rest are still
offered.

Many installers, such as uv's and pipx's, put programs in `~/.local/bin`, which
a fresh shell often does not have on `PATH` until the next login. So that a
step can run a program an earlier step installed, such as a step that
configures a tool the step before it installed, steps run with the selected
home's `.local/bin`
appended to `PATH`, and `provides` and bare command names are looked up the
same way. It is appended, so it cannot shadow a system command such as `bash`
or `curl`, and only when it passes the ownership rules, so a directory others
can write never joins `PATH`. Relative `PATH` entries are never searched.

`provides` also looks in Homebrew's `bin` directories, which shells add to
`PATH` only after `brew shellenv`, so a program Homebrew just installed is not
offered again. Steps do not get them on `PATH`, since Homebrew usually lets the
`admin` group write them; call `brew` by its full path right after installing it.

## Safety

homeskillet never overwrites or deletes a file it did not create: `status`
reports each entry as `missing`, `linked`, `conflict`, `no-source`, or
`orphaned`, and a conflict makes `status` and `link` exit nonzero with nothing
changed. Linked files such as `.bashrc` run as you, so it refuses a repository,
source, or home directory that another user could change, following OpenSSH's
rules for `~/.ssh`; refuses to run as root without `--allow-root`; and refuses
system directories such as `/etc` and `/usr`. A setup step runs only after you
review it exactly as it will run and answer `y` or `yes`, and only if no
symlink in the repository leads outside it.
[docs/safety.md](docs/safety.md) has every rule, including unlinking, orphaned
links, and installing homeskillet itself.

## Checks

```sh
make check            # go vet, golangci-lint, and go test -race
```

Tests create their own temporary repositories, with and without manifests.
They require no particular configuration repository and do not change your
home directory.

## Documentation

- [Safety](docs/safety.md): every refusal, and the reasoning behind it.
- [Installing and Updating](docs/install.md): versions, rollback, and build details.
- [Architecture](docs/architecture.md): ownership, execution flow, and safety limits.
- [Development](docs/development.md): build, test, CI, and cross-repository checks.
- [Windows](docs/windows.md): what works for Windows today and what native support needs.
