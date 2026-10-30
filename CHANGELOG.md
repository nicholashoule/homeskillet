# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.5.0]

homeskillet's output reads better at a terminal: `help` is short, with each command's own help one command away,
version lists line up, `update`, `use`, and `remove` say what happened in a sentence, `init` asks before it creates
anything, `info` gathers what a bug report needs, and one color standard shows what is done and what still needs doing.

### Added

- `homeskillet remove VERSION...` deletes installed versions, one or several at once, as `self remove` does. It checks
  every one before deleting any, and refuses the one that runs.
- `homeskillet info` shows what to paste into a bug report: which homeskillet runs and from where, the OS release and
  kernel, and the terminal. It writes your home folder as `~` and shows nothing else from your environment. A new issue
  template asks for it.
- `homeskillet term` shows the terminal alone, with what each finding decides, such as whether output is a terminal
  and whether colors are on and why.
- At a terminal, every command and its help use one color standard: bold titles and headings, green for what is done
  or ready, yellow for what still needs doing or did not happen, red for failures, and in help, cyan for what to type.
  `NO_COLOR` or `TERM=dumb` turns them off, and pipes and scripts never get them
  ([docs/usage.md](docs/usage.md#output)).

### Changed

- `homeskillet init` shows what it would create and asks before creating it; scripts pass `--yes`. It refuses your home
  directory itself, and as before never replaces a file or directory
  ([docs/manifest.md](docs/manifest.md#creating-one)).
- At a terminal, `homeskillet list` groups releases apart from development builds, newest first, in columns that line up
  however long a development version is, and ends with which version runs; `list-remote` lines up too and says what the
  newest release is to this machine. `prepare` is a single line and a list when everything is in place. `update`, `use`,
  and `remove` say what happened in a sentence, such as `v0.4.0 runs now, following latest`, with how to go back, or
  `Removed v0.3.0 and v0.3.1.`; `backups` names each file by its place in the home and says how to restore one; and
  `check` says plainly when a directory is no configuration repository at all. Scripts get the same tab-separated lines
  as before.
- A command run without what it needs, or with a subcommand it does not take, says so in one line naming the command as
  you typed it, such as `'use' needs a version: latest, previous, or one 'homeskillet list' shows`.
- `homeskillet help` is short: the commands, each in a line, and the options most of them take.
  `homeskillet help COMMAND`, `homeskillet COMMAND --help`, or `homeskillet self help` shows that command's usage, its
  arguments, and only the options it takes. A mistyped option points to that command's help.
- The note that a newer release is available reads as a sentence: `Note: v0.5.0 is available; homeskillet update
  installs it`.
- `homeskillet help` and `homeskillet version` name the project's page, https://github.com/nicholashoule/homeskillet.

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

- [0.4.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.4.0): `prepare`, `list` and `list-remote`, `update`
  following latest, and one-line failures with `--verbose`.
- [0.3.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.1): the note that a newer release is available,
  `install VERSION`, and checked install steps for Windows and Go.
- [0.3.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.0): `homeskillet update`, `self releases`, the
  `update`, `install`, and `use` commands, and the website.
- [0.2.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.2.0): native Windows and WSL support, `bootstrap.json`,
  `check`, and `backups`.
- [0.1.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1): the first release, for Linux and macOS.

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.5.0
