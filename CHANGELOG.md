# Changelog

What changed for people using homeskillet in the latest release, and what is
coming in the next. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). A version's section
becomes its release notes on GitHub, so entries say what you can now do, not
what was committed; earlier releases are linked at the bottom
([docs/release.md](docs/release.md#the-changelog)).

## [Unreleased]

## [0.1.1] - 2026-10-26

The first release. homeskillet links the files a configuration repository keeps
under `home/`, or lists in an optional `dotfiles.json`, into a home directory
on Linux and macOS, and runs the repository's setup steps once you approve
each one. Binaries for Linux and macOS, on amd64 and arm64, come with a
`SHA256SUMS` file to check them against
([downloading a release](docs/install.md#downloading-a-release)).

### Added

- Link one file at a time: `homeskillet link ~/dotfiles .bashrc` acts on that
  file alone. `link --force` moves a conflicting file into the data
  directory's `backups/` before linking; nothing is ever deleted.
- Platforms: a manifest, file, or setup step can name `linux`, `darwin`, or
  `windows`, with an optional architecture such as `darwin/arm64`, and
  `--platform` overrides what homeskillet detects.
- `homeskillet init` writes a starter `dotfiles.json` and never replaces one.
- Setup steps by name, `homeskillet setup uv`, and a `run` that lists several
  commands approved together. Done steps say what makes them done.
- `homeskillet completion bash|zsh` completes commands, options, platforms,
  step names, and installed versions, including on macOS's bash 3.2.
- `homeskillet help [command]` and `--help`, headed by the version, with
  options aligned and defaults inline.
- `homeskillet version` prints the version, commit, build time, Go release,
  and platform; `self list` shows them for every installed version.
- A version tag pushed to GitHub builds the release binaries and publishes
  them, so every release is built the same way.

### Changed

- At a terminal, output groups files and steps by what happens to them, with a
  header and a closing line; in a pipe, each is one tab-separated line.
  `--quiet` prints only errors.
- The record of links, backups, and installed versions live together in a data
  directory, `~/.local/share/homeskillet` unless `--data-dir` says otherwise,
  and `link` and `unlink` remove links whose source is gone.
- `setup --yes` runs steps with no terminal, so a step cannot prompt and `sudo`
  cannot reuse a cached password, and an interrupt stops the step and
  everything it started.

### Security

- Symlinks are followed one component at a time: every hop must stay inside
  the repository and every directory passed through must be private.
- The home directory and the directories links go into must be private, a
  link may not point into the repository or the data directory, and system
  directories are refused.
- Setup steps are checked again just before they run, and unattended steps get
  no input.
- Empty option values and arguments are refused, names with invalid UTF-8 or
  line separators are rejected, and paths in output are quoted, so a file
  name cannot rewrite the terminal. `status` and `link` point out a linked
  file that shadows a command on `PATH`.

## Earlier Releases

Each release keeps its notes on its GitHub Release. This is the first, so there
are none yet; see [all releases](https://github.com/nicholashoule/homeskillet/releases).

[Unreleased]: https://github.com/nicholashoule/homeskillet/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/nicholashoule/homeskillet/releases/tag/v0.1.1
