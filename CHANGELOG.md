# Changelog

What changed for people using homeskillet in the latest release, and what is
coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section
becomes its release notes on GitHub, so entries say what you can now do, not
what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.2.0] - 2026-10-27

homeskillet now runs natively on Windows, and configures WSL from Windows
too: one repository can hold both your Windows profile and your WSL home. A
manifest may be called `bootstrap.json`, or split across two files, and
`homeskillet check` lists every issue in a repository at once. Binaries for
Windows, on x64 and Arm64, join those for Linux and macOS
([docs/windows.md](docs/windows.md)).

### Added

- Run homeskillet natively on Windows 10 and 11, from PowerShell, on x64 and
  Arm64: link, unlink, status, setup, and self install work as on Linux and
  macOS. Linking needs Developer Mode.
- Configure WSL from a Windows repository: files in `home/wsl/` are linked
  into WSL's home and nowhere else, and `homeskillet wsl install`, `link`,
  `unlink`, `setup`, and `status` install the Linux build in a distribution
  and run there, on a checked copy of the Windows clone, or on a clone already
  inside WSL ([docs/windows.md](docs/windows.md#windows-and-wsl-together)).
- Use Windows and WSL side by side: `homeskillet wsl link` and the other `wsl`
  commands work from a WSL shell too, handed to the Windows build that
  installed homeskillet there; `homeskillet.exe` run from a WSL shell uses the
  Windows repository it linked before; and each side refuses the other's files
  with a message saying where to run instead.
- Name a manifest `bootstrap.json`, which `homeskillet init` now writes, or
  keep `dotfiles.json`. A repository can keep both, such as its files in one
  and the steps that install tools in the other, and they merge
  ([docs/manifest.md](docs/manifest.md#one-file-or-two)).
- `homeskillet check` lists every issue in a repository's manifests and files,
  for every platform, or says it is valid, without touching a home; in a
  script it prints one line per issue and exits nonzero.
- `homeskillet backups` lists the files `link --force` moved aside, and
  `backups purge` deletes them, all or those older than `--older-than 30d`,
  after showing each one and asking ([docs/usage.md](docs/usage.md#conflicts-and-backups)).
- Write a manifest's targets and `creates` as `~/.bashrc` or `$HOME/.bashrc`
  as well as `.bashrc`, on every system.
- A path given to homeskillet that starts with `~` means your home from any
  shell, including `cmd`.
- On Windows, setup steps find programs through `PATHEXT`, get `USERPROFILE`
  set to the selected home, and can run what an earlier step installed with
  winget, since the `PATH` installers record in the registry is appended.
- A step's `creates` can start with `{documents}/`, for files in your
  Documents folder such as PowerShell's profile, wherever OneDrive has moved
  it.
- A step that installs from a list, such as apt packages, a Brewfile, or
  winget's tools, can name the list in `inputs`: it is done once it succeeds,
  and offered again only when the list or its command changes, instead of on
  every run ([docs/manifest.md](docs/manifest.md#steps-that-install-from-a-list)).
- `homeskillet completion pwsh` completes commands, options, step names, and
  paths in PowerShell 7.3 and later.
- At a terminal, `link` and `status` end by saying how many setup steps are
  still pending and the command that offers them, so a freshly linked
  repository's setup is not missed.

### Changed

- An error in a manifest lists every issue found in it, not only the first.
- `provides` may not contain `\` or `:`, as it already may not contain `/`.
- `self remove` also refuses the version that is running at the moment.
- A step named after `setup` is offered even when it is done, so naming it
  runs it again; `setup` takes `--data-dir`, where steps with `inputs` are
  recorded.

### Security

- On Windows, the ownership rules read each file's owner and access control
  list, refuse junctions, and refuse system locations such as drive roots,
  `C:\Users`, and `C:\Windows`; an elevated terminal is refused, like root,
  without `--allow-root`.

## Earlier Releases

Each release keeps its notes on its GitHub Release; see
[all releases](https://github.com/nicholashoule/homeskillet/releases).

- [0.1.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1) - 2026-10-26: the first release, for Linux and macOS.

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.2.0
