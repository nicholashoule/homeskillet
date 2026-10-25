# AGENTS.md

Agent instructions for AI coding assistants working in this repository.
See [agents.md](https://agents.md) for the open format specification.

<!-- repogov:context:begin -->
## Context

Prose language: American English (en-US). Write documentation (.md, .txt), code comments and commit messages in American English, with its spelling and conventions (American spelling: color, behavior, -ize); keep identifiers, code and quoted text as they are.

- Project overview: [README.md](README.md)
- Extended documentation: [docs/](docs/)
<!-- repogov:context:end -->

## Project

homeskillet is a dependency-free Go CLI that symlinks dotfiles from a separate
configuration repository into a home directory on Linux and macOS. Priorities,
in order: security, then safety, then speed. Linked files such as `.bashrc`
run as the user, so anyone who can change a linked file can run code as them.

Before changing behavior, read [docs/architecture.md](docs/architecture.md)
(the safety contract) and [docs/handoff.md](docs/handoff.md) (current state and
planned work).

## Layout

| Path | Role |
|---|---|
| `main.go` | Entry point only: calls `cli.Run` and exits on error |
| `internal/cli` | Flags, root refusal, path canonicalization, output |
| `internal/config` | Reading a repository: `dotfiles.json` decoding, `home/` discovery, lexical validation |
| `internal/trust` | Ownership and permission rules (audit these in one place) |
| `internal/link` | Link planning, conflict detection, and filesystem changes |
| `internal/setup` | Setup steps: state checks, review prompts, execution |
| `internal/install` | Versioned installs of homeskillet itself: `latest`, `previous`, `current` links |
| `internal/version` | Build details and semantic version ordering |
| `Makefile` | Portable build, test, lint, and cross-build targets |
| `tools/xplat` | Cross-platform shim for Makefile recipes, vendored from `go-webapp` |

`tools/xplat` is a byte-identical copy of
`github.com/nicholashoule/go-webapp/tools/xplat`. Do not edit it here: change it
upstream, bump its `xplat-vN` tag, and copy it again. golangci-lint skips it
for the same reason.

`main.go` and every package under `internal/` except `internal/config` and
`internal/version` build only on Linux and macOS.
On other systems `go build ./...` and `go test ./...` skip them and still cover
`internal/config`, `internal/version`, and `tools/xplat`. Cross-compile release binaries with
`GOOS=linux` or `GOOS=darwin` from any host.

## Commands

Run these from the repository root; `make check` must pass before handing work
back. `make` alone lists every target.

```sh
make fmt          # golangci-lint fmt ./...
make check        # go vet, golangci-lint run, go test -race
make build        # bin/homeskillet for this machine (VERSION=v0.0.1 to stamp)
make install      # build, then install it as a new version under ~/.local
make cross        # bin/homeskillet-<os>-<arch> for Linux and macOS
make integration  # link the sibling dotfiles-linux manifest into a temp home
```

Keep the Makefile portable across Linux, WSL, containers, and macOS. macOS
ships GNU Make 3.81, so use only Make 3.81 features. Recipes call only `go`,
`golangci-lint`, `env`, `$(XPLAT)` (`go run ./tools/xplat`), and the
homeskillet binary they just built. Use xplat for file operations instead of
`mkdir -p`, `rm -rf`, `cp`, or `test -f`, and homeskillet itself for symlinks
instead of `ln`.
[docs/development.md](docs/development.md) has the details.

## Supported Environments

Linux with bash (any distribution, including Docker containers), macOS with
bash or zsh, and Windows through WSL Ubuntu. Native Windows is not a target.
Behavior must not depend on the login shell. When a change touches
environment-specific behavior (root in containers, `/mnt` drives on WSL, case
insensitive file systems on macOS), document it in the README's Supported
Environments table.

## Go Conventions

- Use the standard library only. Ask before adding a module dependency; prefer
  shelling out to a system tool (for example `git`) over importing a library.
- Keep `main` thin. Code under `internal/` takes its output streams as
  `io.Writer`s and returns errors; it never prints to stdout or calls
  `os.Exit`. `forbidigo` enforces this.
- Handle every error, including writes and `Close`. Wrap errors with `%w` and
  compare them with `errors.Is` or `errors.As`.
- Put tests beside the code they cover. Prefer table-driven cases, build
  fixtures under `t.TempDir()`, and never touch the real home directory.
- Write godoc for exported identifiers. For non-obvious checks, comment the
  reasoning, meaning the attack or data loss the check prevents, not what the
  code does.
- A `//nolint` directive must name the linter and give a reason. Fix findings
  rather than excluding them where possible.

## Safety Rules

- Validate every entry before changing anything, so a failed check never
  leaves a partial installation.
- Never overwrite or delete a file homeskillet did not create. Report
  conflicts; do not resolve them.
- Do not weaken ownership and permission checks, path containment, or
  control-character rejection to make a test or a particular setup pass.
- Anything that writes into a home directory needs a dry-run mode and tests
  showing that the dry run changes nothing.
- Setup steps are code from someone's repository. Never run one without
  showing it exactly and getting approval. Keep "no" as the default answer,
  and never add an implicit shell, environment expansion, or templating to
  `run`.
- Installed versions of homeskillet are immutable, except `+dirty` builds.
  Never rewrite one in place, and change the command only by renaming a new
  link over `current`, `latest`, or `previous`, so it always resolves to a
  complete binary. Never replace a `homeskillet` command that homeskillet did
  not create.
- Tests that run commands must keep them inside `t.TempDir()`. Steps run with
  `HOME` set to the test home, so a broken override can write into the real
  home directory.

## Working Rules

- The configuration repository is separate. Do not move configuration here or
  add runtime dependencies on a sibling checkout.
- Keep everything generic to any user's machine. Never commit absolute
  personal paths, user names, or host-specific settings; use paths relative
  to the repository and variables that can be overridden, such as
  `INTEGRATION_REPO`.
- A manifest is optional. Changes must keep a repository that has only a
  `home/` directory working, and treat discovered entries exactly like listed
  ones.
- Do not commit, push, or open pull requests unless asked. Preserve
  concurrent user edits.
- Keep README.md and docs/ in step with behavior changes, and update
  docs/handoff.md when handing work to another agent.

## Nested Instructions

Place an `AGENTS.md` in any subdirectory to provide directory-scoped instructions. Agents load the nearest `AGENTS.md` walking up to the repo root; more specific files take precedence over less specific ones.
