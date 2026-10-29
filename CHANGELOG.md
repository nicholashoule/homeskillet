# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.4.0]

homeskillet now gets a fresh machine ready and manages its own versions like a version manager: `homeskillet prepare`
checks for what it needs, such as Developer Mode and Git on Windows, and offers what is missing, each only once you
approve; `list` and `list-remote` show the versions installed and published; and `update` always leaves the newest
release running. Failures are one line and a hint, with `--verbose` for the whole error.

### Added

- `homeskillet prepare` gets a machine ready before a repository is cloned: on Windows it checks Developer Mode, Git,
  and PowerShell 7, offers to open Settings for Developer Mode, to register or repair winget when it is missing, and to
  install Git and PowerShell with winget, each shown exactly and run only once you answer `y`; on Linux and macOS it
  checks for Git and says how to install it. `--dry-run` only checks, and it exits nonzero while something needed is
  missing; `homeskillet prep` is short for it ([docs/windows.md](docs/windows.md#preparing-windows)).
- A failure is one line and a hint when homeskillet recognizes the cause, such as a setup step that failed, a program
  not on `PATH`, a permission it lacks, or a full disk; `--verbose` shows the whole error
  ([docs/usage.md](docs/usage.md#output)).
- `homeskillet list` lists the installed versions and which one runs, and `homeskillet list-remote` the versions
  published on GitHub and which of them are installed, the same as `self list` and `self releases`
  ([docs/install.md](docs/install.md#versions-and-updates)).

### Changed

- `homeskillet update` always leaves the newest release running: when you had pinned a version with `use`, it follows
  `latest` again and names the version that ran before. `install VERSION` and `update VERSION` still install without
  changing which version runs ([docs/install.md](docs/install.md#updating)).
- Messages name the short commands, as in `homeskillet use latest` and `homeskillet list`, and asking for a version
  that is not published says so and points to `homeskillet list-remote`.
- An unknown command gets one line, such as `'lnk' is not a command; did you mean 'link'?`, and help describes each
  command in a line, with a link to the full docs. At a terminal, `prepare` reports in groups with a closing sentence,
  as `setup` does, and neither adds a `ran` line after a step's own output; scripts still get one line per item and
  outcome.

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

- [0.3.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.1): the note that a newer release is available,
  `install VERSION`, and checked install steps for Windows and Go.
- [0.3.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.0): `homeskillet update`, `self releases`, the
  `update`, `install`, and `use` commands, and the website.
- [0.2.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.2.0): native Windows and WSL support, `bootstrap.json`,
  `check`, and `backups`.
- [0.1.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1): the first release, for Linux and macOS.

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.4.0
