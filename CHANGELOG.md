# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.3.1]

A small follow-up to 0.3.0, so that `homeskillet update` has a newer release to move to, and says when one is.

### Added

- At a terminal, `status`, `link`, `unlink`, and `setup` end with one line when a newer release is available, checked
  at most once a day and shown at most once a day; `HOMESKILLET_UPDATE_CHECK=off` turns it off
  ([docs/install.md](docs/install.md#updating)).

### Changed

- The install guide says that pinning a version runs that version's own command, so one from before 0.3.0 has no
  `use`, and that `homeskillet self use latest` works from any version ([docs/install.md](docs/install.md#updating)).

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

- [0.3.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.0): `homeskillet update`, `self releases`, the
  `update`, `install`, and `use` commands, and the website.
- [0.2.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.2.0): native Windows and WSL support, `bootstrap.json`,
  `check`, and `backups`.
- [0.1.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1): the first release, for Linux and macOS.

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.1
