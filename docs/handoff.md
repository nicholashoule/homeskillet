# Agent Handoff

Updated: 2026-10-25.

## Locations

- Manager: this repository, `github.com/nicholashoule/homeskillet`.
- Configuration: any separate repository. The maintainer's own,
  `dotfiles-linux`, is conventionally checked out beside this one as
  `../dotfiles-linux`; `make integration` uses that path unless
  `INTEGRATION_REPO` names another.

These are separate Git repositories. Go code lives here, decoupled from
personal configuration. No Homesick dependency is wanted. Nothing committed to
either repository may contain machine-specific paths such as a user's home
directory; use paths relative to the repository instead.

## Current State

- The Go module, `main.go`, generic tests, and generated binaries were moved
  out of dotfiles-linux into homeskillet. The module path is
  `github.com/nicholashoule/homeskillet`.
- Code is split into `internal/cli`, `internal/config`, `internal/trust`,
  `internal/link`, `internal/setup`, `internal/install`, and
  `internal/version`; `main.go` only calls `cli.Run`.
- A portable Makefile drives fmt, check, build, cross, integration, and clean
  through `tools/xplat`, which is vendored byte-identical from `go-webapp`.
  `.golangci.yml`, `.gitattributes`, `.gitignore`, `.dockerignore`, and
  `AGENTS.md` are general Go configuration adapted for this repository.
  golangci-lint includes `modernize`, `usetesting`, and `forcetypeassert`, so
  outdated idioms and panicking type assertions fail `make check`.
- Target environments (user, 2026-10-24): Linux/bash including Docker and
  other Linux containers, macOS with bash or zsh, and Windows via WSL Ubuntu.
- Product direction (user, 2026-10-24): bootstrap anyone's configuration into
  those environments, mainly for shell customization such as oh-my-posh and
  for system setup.
- Decision (user, 2026-10-24): setup steps are declared in the manifest
  (option A), not built-in installers. Implemented as `homeskillet setup`:
  the whole repository tree must be private, each step is shown exactly and
  approved (default no, `--yes` for unattended builds), steps run as argument
  vectors from the repository root with `HOME` set to the selected home, and
  `creates`/`provides` mark steps done without running anything.
- Requirement (user, 2026-10-25): homeskillet must work without
  `dotfiles.json`. Implemented as the `home/` convention: with no manifest, or
  one that omits `files`, every file under `home/` links to the same relative
  path in the home directory. `"files": []` disables it. Discovered entries
  pass the same validation and ownership checks as listed ones. A repository
  with neither a manifest nor `home/` is refused as a likely wrong `--repo`.
- Requirement (user, 2026-10-25): versioned binaries with build info, for
  clean updates, and a symlink system "like latest and previous".
  Implemented as `homeskillet version` (version, commit, commit time,
  modified flag, Go version, platform, recorded by Go 1.24+ from Git or
  stamped with `make build VERSION=`) and `homeskillet self
  install|use|list|remove` with `make install`. Versions live under
  `~/.local/share/homeskillet/versions/<v>/`. `latest` and `previous` are
  derived from the installed set (highest and second-highest; a clean build
  outranks a `+dirty` one of the same version). `current` follows `latest` or
  pins one version, and `~/.local/bin/homeskillet` links through `current`.
  Installed versions are immutable except `+dirty`. No remote download
  exists: a future `self update` that fetches releases would need signed
  release artifacts first.
- `config.Load` reads through `os.Root`. The ownership check now also covers a
  symlinked manifest's destination and the directories leading to it.
- Still planned for bootstrap: `clone <url>` and `update` with a review of
  incoming changes, and environment selectors such as `wsl` and `container`
  beside `linux` and `darwin`. Also planned: a state file for orphaned links
  plus an `adopt` command.
- Configuration-specific checks live in dotfiles-linux as `tests/check.sh`.
  Generic manager tests do not read personal configuration.
- Changes are uncommitted; many files are untracked. Preserve concurrent edits.
  Another agent may be editing dotfiles-linux; do not change it without a
  request.
- No links or binaries were installed into the user's real home.

## Verified Before This Handoff

`make check` passed, including race-enabled tests and golangci-lint.
Deliberately breaking each new rule failed a test, apart from one
defense-in-depth check that is not reachable on its own; that includes 16
installer, version, and flag rules. In a scratch Git copy tagged v0.1.0 and
v0.2.0, Go stamped both versions and a `+dirty` build, and install, rollback
with `self use previous`, pinning across installs, `self use latest`, and
`self remove` behaved as documented in a scratch home. A scratch copy of
dotfiles-linux with its manifest removed discovered the same 11 entries the
manifest lists; they linked and unlinked in a temporary home, and its
`tests/check.sh` passed. Linux and macOS cross-builds succeed for amd64 and
arm64.

## Remaining Verification

Native macOS runtime testing has not been performed. Cross-compilation does not
establish runtime behavior. Use [development.md](development.md) for reproducible
checks; repeat them when changing code.

## Next Agent

Read this file, [architecture.md](architecture.md), and both READMEs, then inspect
both worktrees. Check for newer user changes rather than assuming the snapshot
above is unchanged. Linux tests and cross-builds are verified; do not claim
native macOS verification. No commit or release has been requested.
