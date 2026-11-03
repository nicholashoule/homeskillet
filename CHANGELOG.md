# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.6.1]

homeskillet 0.6.1 is a small hardening release, from code scanning findings: `init` now has the JSON encoder write its
whole starter manifest, so no value in it is put between quotes by hand.

### Changed

- The starter manifest `init` writes is laid out by the JSON encoder, which puts each platform in its `platforms` list
  on a line of its own ([docs/manifest.md](docs/manifest.md#creating-one)).

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

- [0.6.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.6.0): a `C:\` with Modify for signed-in users no
  longer stopping every command on Windows, and `--allow-open-profile`.
- [0.5.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.5.1): `install` putting its folder on the Path on
  Windows, and one-line errors when permissions are too open.
- [0.5.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.5.0): help for each command, `info` and `term`,
  removing several versions at once, one color standard, and a guided start.
- [0.4.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.4.0): `prepare`, `list` and `list-remote`, `update`
  following latest, and one-line failures with `--verbose`.
- [0.3.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.1): the note that a newer release is available,
  `install VERSION`, and checked install steps for Windows and Go.
- [0.3.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.0): `homeskillet update`, `self releases`, the
  `update`, `install`, and `use` commands, and the website.
- [0.2.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.2.0): native Windows and WSL support, `bootstrap.json`,
  `check`, and `backups`.
- [0.1.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1): the first release, for Linux and macOS.

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.6.1
