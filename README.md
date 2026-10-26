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

Without Go, download a release for your OS and architecture from the Releases
page, make it executable, and run its `self install`
([docs/install.md](docs/install.md#downloading-a-release)).

For tab completion in bash or zsh, see
[docs/install.md](docs/install.md#shell-completion).

`make build` alone leaves the binary in `bin/` without installing it. Both
produce a static, stripped binary
([docs/development.md](docs/development.md#binary-size)) that runs on any Linux
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
homeskillet setup ~/dotfiles                  # offer each setup step
homeskillet setup ~/dotfiles uv               # only the step named uv
```

Files after the repository limit `status`, `link`, and `unlink` to them: a
target (`.bashrc`), a source (`home/.bashrc`), or a directory of either
(`.bashrc.d`). The repository defaults to the current directory or, when that
is not one, to the one repository already linked into the home, so after the
first link, `homeskillet link` alone works anywhere. Options go anywhere after
the command, and `homeskillet help` (or `-h`) lists every command and option. Without a command, `status` runs. There, and after `setup`, where
other words name steps, the repository must plainly be a path (`.`, `..`, or
containing `/`), so a mistyped command is never read as a directory. `--home` selects another home, `--dry-run` changes nothing,
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

A repository needs no manifest: files under `home/` are linked as they are.
Add `dotfiles.json` at its root only for what `home/` cannot say: `platforms`,
the systems the repository is for; `setup`, commands that install what the
linked files use, run only when you approve them; or `files`, an explicit list
that replaces `home/`. `homeskillet init` writes a starter, and never replaces
an existing manifest:

```json
{
  "platforms": ["darwin"],
  "setup": [
    { "name": "homebrew", "run": ["bash", "setup/homebrew.sh"], "provides": "brew" },
    { "name": "brew-tools", "run": [["brew", "install", "ripgrep"], ["brew", "install", "jq"]],
      "provides": "rg" }
  ]
}
```

`homeskillet setup` runs each step only after you approve it.
[docs/manifest.md](docs/manifest.md) describes every field, platforms, and how
steps run, including running only the steps you name.

## Safety

homeskillet never overwrites or deletes a file it did not create: a conflict
makes `status` and `link` exit nonzero with nothing changed. It refuses to run
as root without `--allow-root`, and refuses system directories such as `/etc`
and `/usr`. Linked files such as `.bashrc` and setup steps run as you, so:

- **Nobody else can change what you link.** homeskillet refuses a repository,
  source file, or symlink hop that another user could modify, following
  OpenSSH's rules for `~/.ssh`. Only you, or root, can change what runs.
- **You decide what to trust.** Linking or running setup from a repository
  means running its code as you. Each setup step is shown exactly as it will
  run and runs only if you answer `y` or `yes`. Review someone else's dotfiles
  before linking them, and especially before `setup --yes`.

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

- [The Manifest](docs/manifest.md): what `dotfiles.json` is for, platforms, and setup steps.
- [Safety](docs/safety.md): every refusal, and the reasoning behind it.
- [Installing and Updating](docs/install.md): versions, rollback, and build details.
- [Architecture](docs/architecture.md): ownership, execution flow, and safety limits.
- [Development](docs/development.md): build, test, CI, and cross-repository checks.
- [Windows](docs/windows.md): what works for Windows today and what native support needs.
