# AGENTS.md

Agent instructions for AI coding assistants working in this repository. See [agents.md](https://agents.md) for the open
format specification.

<!-- repogov:context:begin -->
## Context

Prose language: American English (en-US). Write documentation (.md, .txt), code comments and commit messages in American English, with its spelling and conventions (American spelling: color, behavior, -ize); keep identifiers, code and quoted text as they are.

- Project overview: [README.md](README.md)
- Extended documentation: [docs/](docs/)
<!-- repogov:context:end -->

## Project

homeskillet is a dependency-free Go CLI that symlinks dotfiles from a separate configuration repository into a home
directory on Linux, macOS, and Windows. Priorities, in order: security, then safety, then speed. Linked files such as
`.bashrc` run as the user, so anyone who can change a linked file can run code as them.

Before changing behavior, read [docs/architecture.md](docs/architecture.md) (the execution flow and safety contract) and
[docs/development.md](docs/development.md) (checks and workflow). The repository keeps no separate status or handoff
file: the README, docs/, and this file describe the project, so keep them current instead.

## Layout

| Path | Role |
|---|---|
| `main.go` | Entry point only: calls `cli.Run` and exits on error |
| `main_unsupported.go` | Entry point on unsupported systems such as FreeBSD: says so and exits |
| `internal/cli` | Flags, root refusal, path canonicalization, output, help, shell completion, `init`'s starter manifest, `check`, and `update`'s release download |
| `internal/config` | Reading a repository: `bootstrap.json` and `dotfiles.json` decoding and merging, `home/` discovery, lexical validation |
| `internal/trust` | Ownership and permission rules (audit these in one place) |
| `internal/link` | Link planning, conflict detection, filesystem changes, and the record of links |
| `internal/setup` | Setup steps: state checks, review prompts, execution |
| `internal/install` | Versioned installs of homeskillet itself: `latest`, `previous`, `current` links (on Windows, a `current` file and a link to the chosen program) |
| `internal/safefile` | Size-limited reads and atomic writes; use it for every file homeskillet keeps |
| `internal/version` | Build details and semantic version ordering |
| `internal/host` | Detecting the OS and architecture homeskillet runs on (best effort), Windows known folders and registry, and whether it may make symlinks |
| `internal/wsl`, `internal/mirror` | `homeskillet wsl`: finding and running commands in a WSL distribution; copying a Windows clone into it, checked, whole |
| `Makefile` | Portable build, test, lint, and cross-build targets |
| `tools/xplat` | Cross-platform shim for Makefile recipes, vendored from `go-webapp` |
| `tools/buildtime` | Prints the build time the Makefile stamps into binaries |
| `tools/smoke` | The smoke test: the built binary links and unlinks this system's example in a scratch home, `make smoke` and CI |
| `tools/site` | The website check: the page and every file it references, `make site`, CI, and the Pages workflow |
| `tools/githooks` | The pre-commit hook and its installer, `make hooks` |
| `tools/release` | The release build both the release workflow and CI's dry run run (docs/release.md) |
| `examples/` | Starter configuration repositories, one per system; `TestExamples` runs `check` on each |
| `site/` | The website, one hand-written page GitHub Pages serves (docs/development.md) |
| `.github/` | CI, release, and Pages workflows, the bug report template, Dependabot, repogov line limits, and Copilot instructions |
| `CHANGELOG.md`, `SECURITY.md` | What changed for users, by release (the release notes come from it); how to report a vulnerability |

`tools/xplat` is a byte-identical copy of `github.com/nicholashoule/go-webapp/tools/xplat`. Do not edit it here: change
it upstream, bump its `xplat-vN` tag, and copy it again. golangci-lint skips it for the same reason.

`main.go` and every package under `internal/` except `internal/config`, `internal/host`, `internal/safefile`, and
`internal/version` build only on Linux, macOS, and Windows; elsewhere `go build ./...` builds `main_unsupported.go`. A
`_unix.go` file holds the Linux and macOS side of a rule and a `_windows.go` file its Windows side: change both
together, and vet and lint with `GOOS=windows` too. Any host cross-compiles for all three.

## Commands

Run these from the repository root; `make check` must pass before handing work back. `make` alone lists every target.

```sh
make fmt          # golangci-lint fmt ./...
make check        # go vet, golangci-lint run, go test -race, make smoke
make build        # bin/homeskillet for this machine (VERSION=v0.0.1 to stamp)
make install      # build, then install it as a new version under ~/.local
make cross        # bin/homeskillet-<os>-<arch> for Linux, macOS, and Windows
make integration  # link the sibling dotfiles-linux manifest into a temp home
make hooks        # pre-commit hook: check, repogov, demojify (docs/development.md)
```

CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) also runs repogov, which limits Markdown length (README.md to
300 lines), and demojify, which fails on emoji outside test files. When a file nears its limit, move detail to `docs/`
and link to it. Check both before handing back: `go run github.com/nicholashoule/repogov/cmd/repogov@v0.9.0 -agent
copilot all` and `go run github.com/nicholashoule/demojify-sanitize/cmd/demojify@v1.2.0 -root . -exts
.go,.md,.yml,.yaml,.json`.

Keep the Makefile portable across Linux, WSL, containers, and macOS. macOS ships GNU Make 3.81, so use only Make 3.81
features. Recipes call only `go`, `golangci-lint`, `env`, `$(XPLAT)` (`go run ./tools/xplat`), and the homeskillet
binary they just built. Use xplat for file operations instead of `mkdir -p`, `rm -rf`, `cp`, or `test -f`, and
homeskillet itself for symlinks instead of `ln`. [docs/development.md](docs/development.md) has the details.

## Supported Environments

Linux with bash (any distribution, including Docker containers), macOS with bash or zsh, and Windows 10 and 11 with
PowerShell, natively or through WSL Ubuntu. Platform values and detection live in `internal/config` (`ParsePlatform`)
and `internal/host`. [docs/windows.md](docs/windows.md) holds every way Windows differs; keep it current. Behavior must
not depend on the login shell, and nothing links across Windows and WSL. When a change touches environment-specific
behavior (root in containers, `/mnt` drives on WSL, case insensitive file systems on macOS and Windows), document it in
the README's Supported Environments table or docs/windows.md.

## Go Conventions

- Use the standard library only. Ask before adding a module dependency; prefer shelling out to a system tool (for
  example `git`) over importing a library.
- Keep the binary small ([docs/development.md](docs/development.md#binary-size)). Before importing a package that pulls
  in much code, such as `regexp`, `crypto/*`, `debug/*`, or `net/*`, check that a few lines of plain Go cannot do the
  job, and check `go list -deps .` afterward.
- Be nil safe. Exported functions accept nil writers (discarding output) and nil contexts, and refuse zero values whose
  empty paths would mean the current directory. Bound every read of a file homeskillet parses, and write files
  atomically: temporary name, flush, rename, flush the directory.
- Keep `main` thin. Code under `internal/` takes its output streams as `io.Writer`s and returns errors; it never prints
  to stdout or calls `os.Exit`. `forbidigo` enforces this.
- Handle every error, including writes and `Close`. Wrap errors with `%w` and compare them with `errors.Is` or
  `errors.As`.
- Put tests beside the code they cover. Prefer table-driven cases, build fixtures under `t.TempDir()`, and never touch
  the real home directory.
- Write godoc for exported identifiers. For non-obvious checks, comment the reasoning, meaning the attack or data loss
  the check prevents, not what the code does. Start every `.go` file, tests included, with a comment saying what it
  holds; the package comment does this for the file that carries it.
- A `//nolint` directive must name the linter and give a reason. Fix findings rather than excluding them where possible.

## Safety Rules

- Validate every entry before changing anything, so a failed check never leaves a partial installation.
- Never overwrite or delete a file homeskillet did not create. Report conflicts; resolve them only under `link --force`,
  which moves each file or symlink, never a directory, into the data directory's `backups/` (never deletes it, never
  overwrites a backup), undoes its moves if one fails, and moves back what linking did not replace. Only `backups purge`
  deletes a backup, after listing each and asking (default no) unless `--yes`.
- Do not weaken ownership and permission checks, path containment, the system-directory refusals in
  `trust.CheckLocation`, the refusal of junctions on Windows, or control-character rejection to make a test or a
  particular setup pass. On Windows, an access control entry the checks cannot evaluate must fail them. New code that
  reads or writes a home, repository, or prefix path must check it with `trust.CheckLocation` first.
- Anything that writes into a home directory needs a dry-run mode and tests showing that the dry run changes nothing.
- Setup steps are code from someone's repository. Never run one without showing it exactly and getting approval. Keep
  "no" as the default answer, and never add an implicit shell, environment expansion, or templating to `run`. The only
  change to a step's `PATH` is appending, each only after it passes the ownership rules, the home's `.local/bin` and, on
  Windows, the registry's system and user `PATH` and winget's links directory; never prepend to `PATH` or search
  relative entries.
- Installed versions of homeskillet are immutable, except `+dirty` builds. Never rewrite one in place, and change the
  command only by renaming a new link over `current`, `latest`, or `previous`, or on Windows over the command itself, so
  it always resolves to a complete binary. Never replace a `homeskillet` command that homeskillet did not create.
- Tests that run commands must keep them inside `t.TempDir()`. Steps run with `HOME` (and on Windows `USERPROFILE`) set
  to the test home, so a broken override can write into the real home directory.

## Working Rules

- The configuration repository is separate. Do not move configuration here, beyond the small generic starters in
  `examples/`, or add runtime dependencies on a sibling checkout.
- Keep everything generic to any user's machine. Never commit absolute personal paths, user names, or host-specific
  settings; use paths relative to the repository and variables that can be overridden, such as `INTEGRATION_REPO`.
- A manifest is optional. Changes must keep a repository that has only a `home/` directory working, and treat discovered
  entries exactly like listed ones.
- Never record how many files, entries, steps, or rules something has, in docs, prompts, or checks. Discover them
  instead: a count goes stale the moment someone adds a file.
- Do not commit, push, or open pull requests unless asked. Preserve concurrent user edits.
- Keep README.md, docs/, and this file in step with behavior changes, in the same change. Keep each fact in one document
  and link to it from the others; README.md summarizes and links to docs/ rather than repeating it.
- A change a user would notice gets a line under `Unreleased` in `CHANGELOG.md`, written as what they can now do;
  refactors, test fixes, and CI work stay out ([docs/release.md](docs/release.md#the-changelog)).
- Follow [.editorconfig](.editorconfig): UTF-8, LF, a final newline, and prose in Markdown and comments wrapped at 120
  columns; tables, code blocks, and URLs keep their lines. GitHub shows each newline in a pull request description or
  release notes as a line break, so write those without wrapping; the release build joins the changelog's lines.

## Nested Instructions

Place an `AGENTS.md` in any subdirectory to provide directory-scoped instructions. Agents load the nearest `AGENTS.md`
walking up to the repo root; more specific files take precedence over less specific ones.
