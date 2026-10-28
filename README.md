# homeskillet

[![CI](https://github.com/nicholashoule/homeskillet/actions/workflows/ci.yml/badge.svg)](https://github.com/nicholashoule/homeskillet/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/nicholashoule/homeskillet.svg)](https://pkg.go.dev/github.com/nicholashoule/homeskillet)
[![Go Version](https://img.shields.io/github/go-mod/go-version/nicholashoule/homeskillet)](go.mod)
[![License](https://img.shields.io/github/license/nicholashoule/homeskillet)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen)](go.mod)

A small, dependency-free Go CLI for bootstrapping anyone's dotfiles on Linux,
macOS, or Windows, natively and in WSL. homeskillet links the files a
configuration repository keeps under `home/` into your home directory, and
runs the setup steps its optional manifest declares, such as installing a
prompt or language tools, each only after you approve it. Configuration lives
in your own repository; homeskillet ships and assumes none.

## Supported Environments

homeskillet makes links and runs steps directly, never through your shell, so
it works the same whatever your login shell is.

| Environment | Notes |
|---|---|
| Linux | Any distribution. Tested. |
| macOS | bash or zsh. Cross-compiled, not yet tested natively. |
| Windows 10 and 11 | Native, x64 and Arm64, from PowerShell. Linking needs Developer Mode ([docs/windows.md](docs/windows.md)). |
| WSL | The Linux build inside the distribution, with the repository on its Linux file system, not `/mnt/c`. From Windows, or a WSL shell, `homeskillet wsl` links a repository's `home/wsl/` there ([docs/windows.md](docs/windows.md#windows-and-wsl-together)). |
| Containers | Running as root needs `--allow-root` and a repository root owns, so a bind mount from the host is refused; or run as a regular user ([docs/safety.md](docs/safety.md#ownership)). |

## Install

With Go 1.24 or newer, from this repository:

```sh
make install          # build, then install as a new version under ~/.local
homeskillet version
```

Without Go, download a release and run its `self install`
([docs/install.md](docs/install.md#downloading-a-release); on Windows,
[docs/windows.md](docs/windows.md#installing)). Updating is `git pull` then
`make install`, and rolling back is `homeskillet self use previous`
([docs/install.md](docs/install.md)).

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

After the first `link`, the repository may be left out anywhere. At a terminal
the output is grouped for reading; in a script it is one tab-separated line
per file. [docs/usage.md](docs/usage.md) covers every command and option, and
`homeskillet help` lists them.

## Configuring a Repository

The simplest repository is a `home/` directory laid out as your home:

```text
dotfiles/
└── home/
    ├── .bashrc                -> ~/.bashrc
    └── .config/git/config     -> ~/.config/git/config
```

An optional manifest at the root, `bootstrap.json` or `dotfiles.json`, or both
merged, adds what `home/` cannot say: the `platforms` the repository is for,
`setup` steps, and an explicit list of `files`. `homeskillet init` writes a
starter:

```json
{
  "platforms": ["darwin"],
  "setup": [
    { "name": "homebrew", "run": ["bash", "setup/homebrew.sh"], "provides": "brew" }
  ]
}
```

To start from a working one, copy the example for
[Linux](examples/linux), [macOS](examples/macos), or
[Windows](examples/windows): a few dotfiles, a setup step, and the commands
to link them. [docs/manifest.md](docs/manifest.md) describes every field,
platforms, setup steps, and `home/wsl/` for WSL.

## Safety

homeskillet never overwrites or deletes a file it did not create, short of
backups you ask it to purge after it lists them, and refuses
to run as root, or elevated on Windows, without `--allow-root`. Linked files
such as `.bashrc` and setup steps run as you, so it refuses a repository,
source, or symlink another user could change, following OpenSSH's rules for
`~/.ssh`, and shows each setup step exactly before you approve it. Review
someone else's dotfiles before linking them, and especially before
`setup --yes`. [docs/safety.md](docs/safety.md) has every rule.

## Development

```sh
make check            # go vet, golangci-lint, and go test -race
```

Tests build their own repositories and never touch your home.
[docs/development.md](docs/development.md) covers the Makefile, CI, and
cross builds, and [docs/release.md](docs/release.md) releasing.

## Documentation

- [Usage](docs/usage.md): commands, options, choosing the repository and files, and output.
- [Configuration Repositories](docs/manifest.md): `home/`, `bootstrap.json` and `dotfiles.json`, platforms, setup steps, and `check`.
- [Windows](docs/windows.md): what is supported, setting up Windows, laying out a repository for it, and WSL.
- [Installing and Updating](docs/install.md): installing, versions, rollback, completion, and the data directory.
- [Safety](docs/safety.md): every refusal, and the reasoning behind it.
- [Architecture](docs/architecture.md): ownership, execution flow, and safety limits.
- [Development](docs/development.md) and [Releasing](docs/release.md): checks, CI, and what a pushed tag publishes.
- [CHANGELOG.md](CHANGELOG.md): what changed in each release; [SECURITY.md](SECURITY.md): how to report a vulnerability.
