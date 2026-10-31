# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.5.1]

homeskillet 0.5.1 gets Windows machines past where installs stopped: `install` puts its folder on your Path, so
`homeskillet` runs in every new terminal, and when permissions are too open, or another account owns a file, the error
says so in one line, on every system, with what to do next.

### Changed

- On Windows, `install` puts the folder it installs into, `.local\bin`, on your Path when it is not there, so
  `homeskillet` runs in every new terminal however you installed it, and prints the line that makes it run in the
  terminal you are in. It only appends that one folder, after the ownership checks, and only for your own profile
  ([docs/windows.md](docs/windows.md#putting-the-command-on-your-path)). `prepare` says to open a new terminal when
  the folder is on your Path but not yet in the one it runs in.
- The Windows install lines run `install` even when `prepare` still lists Git as missing, since installing does not
  need Git ([docs/windows.md](docs/windows.md#installing)).
- When a file or folder's permissions are too open, or another account owns it, the error says so in one line on every
  system: the path, what is wrong, and what to do, such as `run chmod go-w on it`. On Windows, for a directory above
  your profile, such as `C:\`, it says that moving files cannot help and that an administrator needs to repair the
  permissions by restoring what Windows sets ([docs/windows.md](docs/windows.md#when-a-folder-is-writable-by-others)).
- Help, errors, and the start guide name homeskillet's repository instead of pages of its documentation, and on
  Windows, the note about the Path says how to add a folder in Windows's own dialog.

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

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

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.5.1
