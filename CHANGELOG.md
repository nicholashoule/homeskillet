# Changelog

What changed for people using homeskillet in the latest release, and what is coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), without dates, as each release's tag records when it was made,
and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section becomes its release
notes on GitHub, so entries say what you can now do, not what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.3.0]

homeskillet now updates itself: `homeskillet update` fetches the newest release from GitHub, checks it against its
`SHA256SUMS`, and installs it beside the version you have, so going back is one command. `update`, `install`, and `use`
are commands of their own, and `self releases` shows what is published ([docs/install.md](docs/install.md#updating)).

### Added

- `homeskillet update` installs the newest release from GitHub, or `update 0.2.0` the one named: the binary for your
  machine and the release's `SHA256SUMS` are downloaded with the system's `curl`, checked, and then it installs itself,
  so the version you had stays installed and the closing note says how to run it again. `--dry-run` downloads and
  checks without running or installing anything ([docs/install.md](docs/install.md#updating)).
- `homeskillet self releases` lists the published releases, newest first, and which of them are installed.
- `update`, `install`, and `use` work as commands of their own, the same as `self update`, `self install`, and
  `self use`, and a version may be written without its leading `v`, as `homeskillet use 0.2.0`.

### Changed

- After `link` and `status`, the closing note about setup steps says how many are pending and to run `setup` to review
  and approve them, in place of the earlier "Next:" line.

### Security

- Releases built from the public repository carry signed build provenance, so `gh attestation verify` can confirm
  that the release workflow built a downloaded binary from its tag ([docs/install.md](docs/install.md#downloading-a-release)).

## Earlier Releases

Each release keeps its notes on its GitHub Release; see [all
releases](https://github.com/nicholashoule/homeskillet/releases).

- [0.2.0](https://github.com/nicholashoule/homeskillet/releases/tag/v0.2.0): native Windows and WSL support, `bootstrap.json`,
  `check`, and `backups`.
- [0.1.1](https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1): the first release, for Linux and macOS.

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.3.0
