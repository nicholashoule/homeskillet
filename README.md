# homeskillet

[![CI](https://github.com/nicholashoule/homeskillet/actions/workflows/ci.yml/badge.svg)](https://github.com/nicholashoule/homeskillet/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/nicholashoule/homeskillet.svg)](https://pkg.go.dev/github.com/nicholashoule/homeskillet)
[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen)](go.mod)

A small, dependency-free Go CLI for bootstrapping anyone's dotfiles on Linux, macOS, or Windows, natively and in WSL.
homeskillet links the files a configuration repository keeps under `home/` into your home directory, and runs the setup
steps its optional manifest declares, such as installing a prompt or language tools, each only after you approve it.
Configuration lives in your own repository; homeskillet ships and assumes none.

## Supported Environments

homeskillet makes links and runs steps directly, never through your shell, so it works the same whatever your login
shell is.

| Environment | Notes |
|---|---|
| Linux | Any distribution. Tested. |
| macOS | bash or zsh. Tested on macOS in CI. |
| Windows 10 and 11 | Native, x64 and Arm64, from PowerShell. `homeskillet prepare` gets a fresh machine ready: Developer Mode, Git, and PowerShell 7, each only once you approve ([docs/windows.md](docs/windows.md#preparing-windows)). |
| WSL | The Linux build inside the distribution, with the repository on its Linux file system, not `/mnt/c`. From Windows, or a WSL shell, `homeskillet wsl` links a repository's `home/wsl/` there ([docs/windows.md](docs/windows.md#windows-and-wsl-together)). |
| Containers | Running as root needs `--allow-root` and a repository root owns, so a bind mount from the host is refused; or run as a regular user ([docs/safety.md](docs/safety.md#ownership)). |

On Windows, choose what homeskillet manages: your PowerShell profile and other Windows dotfiles, natively; your WSL
home, from inside the distribution or from Windows with `homeskillet wsl`; or both from one repository, with `home/`
for Windows and `home/wsl/` for the distribution ([docs/windows.md](docs/windows.md#what-is-supported)).

## Install

Pick your system for its install commands. Each downloads the latest release, checks it against the release's
`SHA256SUMS`, runs it only if it matches, and lets it install itself as a versioned command under `.local` in your home:

- [Linux](docs/install.md#linux), including WSL distributions and containers
- [macOS](docs/install.md#macos)
- [Windows](docs/windows.md#installing), in PowerShell

From then on, homeskillet manages its own versions, on every system:

```sh
homeskillet update            # install the newest release and run it, keeping the one you have
homeskillet list              # installed versions, and which one runs
homeskillet list-remote       # published versions, and which are installed
homeskillet install 0.3.1     # install that release, without changing which one runs
homeskillet use 0.3.1         # run that version; use latest follows the newest again
homeskillet remove 0.3.0      # delete a version you no longer need, unless it runs
```

Building from source is for development or bringing homeskillet to another system
([docs/install.md](docs/install.md#building-from-source)).

## Use

```sh
homeskillet ~/dotfiles                        # status: what link would change
homeskillet link ~/dotfiles --dry-run         # preview; nothing changes
homeskillet link ~/dotfiles                   # link every file
homeskillet link ~/dotfiles .bashrc --force   # replace ~/.bashrc, keeping a backup
homeskillet backups purge --older-than 30d    # delete old backups, after asking
homeskillet setup ~/dotfiles                  # offer each setup step
homeskillet check ~/dotfiles                  # list every issue, or say it is valid
homeskillet unlink ~/dotfiles
```

After the first `link`, the repository may be left out anywhere. At a terminal the output is grouped for reading, in
color where the terminal takes it (`NO_COLOR` turns that off, and `homeskillet term` says what it detects); in a script
it is one tab-separated line per file. [docs/usage.md](docs/usage.md) covers every command and option, `homeskillet
help` lists the commands, and `homeskillet help link` shows one in full.

## Configuring a Repository

The simplest repository is a `home/` directory laid out as your home:

```text
dotfiles/
└── home/
    ├── .bashrc                -> ~/.bashrc
    └── .config/git/config     -> ~/.config/git/config
```

An optional manifest at the root, `bootstrap.json` or `dotfiles.json`, or both merged, adds what `home/` cannot say: the
`platforms` the repository is for, `setup` steps, and an explicit list of `files`. `homeskillet init` writes a starter:

```json
{
  "platforms": ["darwin"],
  "setup": [
    { "name": "homebrew", "run": ["bash", "setup/homebrew.sh"], "provides": "brew" }
  ]
}
```

To start from a working one, copy the example for [Linux](examples/linux), [macOS](examples/macos), or
[Windows](examples/windows): a few dotfiles, a setup step, and the commands to link them.
[docs/manifest.md](docs/manifest.md) describes every field, platforms, setup steps, and `home/wsl/` for WSL.

## Safety

homeskillet never overwrites or deletes a file it did not create, short of backups you ask it to purge after it lists
them, and refuses to run as root, or elevated on Windows, without `--allow-root`. Linked files such as `.bashrc` and
setup steps run as you, so it refuses a repository, source, or symlink another user could change, following OpenSSH's
rules for `~/.ssh`, and shows each setup step exactly before you approve it. Review someone else's dotfiles before
linking them, and especially before `setup --yes`. [docs/safety.md](docs/safety.md) has every rule.

## Development

```sh
make check            # go vet, golangci-lint, go test -race, and the smoke test
```

Tests build their own repositories and never touch your home. [docs/development.md](docs/development.md) covers the
Makefile, CI, and cross builds, and [docs/release.md](docs/release.md) releasing.

## Documentation

- [Usage](docs/usage.md): commands, options, choosing the repository and files, and output.
- [Configuration Repositories](docs/manifest.md): `home/`, `bootstrap.json` and `dotfiles.json`, platforms, setup steps,
  and `check`.
- [Windows](docs/windows.md): what is supported, setting up Windows, laying out a repository for it, and WSL.
- [Installing and Updating](docs/install.md): installing, versions, rollback, completion, and the data directory.
- [Safety](docs/safety.md): every refusal, and the reasoning behind it.
- [Architecture](docs/architecture.md): ownership, execution flow, and safety limits.
- [Development](docs/development.md) and [Releasing](docs/release.md): checks, CI, and what a pushed tag publishes.
- [CHANGELOG.md](CHANGELOG.md): what changed in each release; [SECURITY.md](SECURITY.md): how to report a vulnerability.
