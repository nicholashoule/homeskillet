# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.6.0]

homeskillet 0.6.0 gets more Windows machines working without an administrator: a `C:\` that gives every signed-in user
Modify no longer stops every command, and `--allow-open-profile` lets you accept, run by run, a folder or file in your
profile that another account can change.

### Added

- On Windows, `--allow-open-profile` lets `status`, `link`, `unlink`, and `check` go on when another account can change
  a folder or file in your profile, for a machine whose owner accepts that risk. Every run names each folder or file it
  let through in a warning. It never applies outside your profile, to a file another account owns, to `setup`, or to
  installing homeskillet ([docs/windows.md](docs/windows.md#when-a-folder-is-writable-by-others)).

### Changed

- On Windows, a `C:\` that gives every signed-in user Modify on itself, as some machines come with, no longer stops
  every command, since that cannot rename or delete what is in it. `C:\` is still refused when another account can
  delete what is in it or change its permissions, such as `Everyone:(F)`
  ([docs/windows.md](docs/windows.md#when-a-folder-is-writable-by-others)).

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

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

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.6.0
