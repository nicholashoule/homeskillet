# Development

## Local Checks

Run from the homeskillet repository root. `make` alone lists every target.

```sh
make fmt              # golangci-lint fmt ./...
make check            # go vet ./..., golangci-lint run ./..., go test -race ./...
make build            # static, stripped bin/homeskillet (see Binary Size)
bin/homeskillet --help
make install          # build, then bin/homeskillet self install
make hooks            # run check, repogov, and demojify before each commit
```

`make test` uses the race detector only when cgo is available. On Linux the
race detector needs cgo, and Go turns cgo off when no C compiler is installed,
as on a fresh WSL Ubuntu or a slim container image. Install a compiler (for
example `build-essential`) to get race checking there. Pass `RACE=` on
musl-based images such as Alpine, where the race detector is unsupported.

The [Makefile](../Makefile) must run on Linux (including WSL and containers)
and on macOS, which ships GNU Make 3.81. Keep it to Make 3.81 features. Make
runs recipes with `/bin/sh` whatever your login shell is, so bash and zsh
behave the same. Recipes call only `go`, `golangci-lint`, `env`, and
`tools/xplat`. `tools/xplat` handles file operations (`mkdir`, `rmrf`, `cp`,
`require`) and renders `make help`, which avoids flags that differ between GNU,
BSD, and BusyBox tools. It is a byte-identical copy of
`github.com/nicholashoule/go-webapp/tools/xplat`. Change it upstream, not
here; golangci-lint skips it for that reason.

[.golangci.yml](../.golangci.yml) explains each enabled linter and exclusion.

`bin/` is ignored. Tests live beside each package under `internal/`. They create
their own temporary repositories, with and without manifests; no sibling
checkout is required. `internal/config` has no build constraint, so its tests
skip cases that need symlinks or control characters in file names where the
system forbids them, as on Windows.
Fixtures canonicalize temporary paths because macOS can expose its temporary
directory through a symlink.

`TestRefusesRoot` covers the refusal of root, and of an elevated process on
Windows, by replacing the check that detects it; to see the real one by hand,
`unshare -r bin/homeskillet status` runs the binary as root inside a user
namespace. Tests cannot exercise files owned by another user without root.

### On Windows

The Makefile targets Linux and macOS, but it also runs on Windows, from
PowerShell or Git Bash, where GNU Make and Git for Windows's `sh` and `env` are
on PATH; builds there get `.exe` (`go env GOEXE`). Otherwise run the checks
directly, from PowerShell:

```powershell
go vet ./...; $env:GOOS = 'linux'; go vet ./...; Remove-Item Env:GOOS
golangci-lint run ./...
go test ./...
```

Tests that make symlinks skip, saying why, unless Developer Mode is on
([windows.md](windows.md#before-you-start)); CI's Windows runners run them
elevated, which allows symlinks. Lint the Linux build too
(`$env:GOOS = 'linux'` before `golangci-lint run`), since files for one system
are invisible to a lint of the other.

`homeskillet wsl` needs a real WSL distribution, which CI does not have, so
try it by hand. A development build has no release to download, so build the
Linux one from the same tree, which `wsl install` requires, and keep
everything in a scratch home in the distribution:

```powershell
make install; make cross-linux-amd64
wsl.exe --exec mkdir -p -m 700 /tmp/scratch/home
homeskillet wsl install --from bin\homeskillet-linux-amd64 --home /tmp/scratch/home
homeskillet wsl link C:\path\to\repository --home /tmp/scratch/home
```

## Git Hooks

`make hooks` installs [tools/githooks/pre-commit](../tools/githooks/pre-commit)
as this clone's pre-commit hook. Each commit then runs `make check`, repogov,
and demojify, the checks CI runs, and stops when any fails:

- The hook only reports. It never rewrites or stages files, so changes you
  left unstaged stay out of the commit. `make fmt` fixes formatting.
- It checks the working tree, which can differ from what is staged. CI checks
  what was committed.
- The installed hook is a copy. Run `make hooks` again after the hook changes.
- `make hooks` never replaces a hook it did not install, such as one written by
  hand or a symlink, and refuses when
  `core.hooksPath` is set, since that directory may serve every repository.
- The first commit downloads the pinned repogov and demojify, so it needs the
  network. `git commit --no-verify` skips the hook once.

## Continuous Integration

[.github/workflows/ci.yml](../.github/workflows/ci.yml) runs on every push to
`main` and every pull request:

- `make vet` and `make test` on Linux and macOS, with every Go release from the
  `go.mod` floor to the latest. The macOS runner also proves the Makefile works
  with its GNU Make 3.81.
- On Windows, `go vet` and `go test`, which run elevated there, so the tests
  that make symlinks run too, and a built homeskillet linking, reporting, and
  unlinking a repository in a scratch home.
- The shell completion tests load the bash script into every bash they find,
  under `set -u`, including macOS's `/bin/bash`, which is bash 3.2, load
  the zsh script where zsh is installed, and complete through the PowerShell
  script where pwsh is, as on the Windows runners. Locally, a machine without
  zsh skips the zsh test, so the macOS runners are where it runs.
- `make cross`, once, on Linux with the latest Go.
- golangci-lint for the Linux and Windows builds, which also checks
  formatting; repogov, which checks the
  Markdown line limits in [.github/repogov-config.json](../.github/repogov-config.json);
  and demojify, which fails on emoji in Go, Markdown, YAML, and JSON files.
  demojify only reports, and skips `_test.go` files, which may hold emoji as
  test input. Each is pinned; use the same versions locally so results match:

```sh
go run github.com/nicholashoule/repogov/cmd/repogov@v0.9.0 -agent copilot all
go run github.com/nicholashoule/demojify-sanitize/cmd/demojify@v1.2.0 -root . -exts .go,.md,.yml,.yaml,.json
```

Actions are pinned by commit SHA. [Dependabot](../.github/dependabot.yml)
proposes updates to them weekly, and to Go modules should one ever be added.
It cannot update the golangci-lint, repogov, and demojify versions; bump those
by hand.
Write each action's exact release in the comment after its SHA, such as
`# v4.3.1` rather than `# v4`. Dependabot rewrites the comment only while it
names a tag on the pinned commit, and a major tag such as `v4` moves on to
later releases, which leaves the comment behind.

## Binary Size

A build is about 2.9 MiB. Builds pass `-trimpath` and `-ldflags "-s -w"`, which drops the symbol table
and DWARF data (about a third of the binary) while keeping function names and
lines in panics, and set `CGO_ENABLED=0` for a static binary. Code avoids
packages that cost more than they do here: `regexp` (about 260 KiB) gave way
to small validators, SHA-256 (about 150 KiB, with Go's FIPS module) to a byte
comparison, and `debug/buildinfo` (about 370 KiB, with its executable-format
readers) to the `build.json` record install writes. To check what a change
links in:

```sh
go list -deps . | grep -E '^(regexp|crypto|debug/|net)'   # expect no output
make build && ls -l bin/homeskillet
```

UPX and similar packers are not used: they break macOS code signatures, slow
startup, and trigger antivirus heuristics.

## Versions and Releases

A release is a tag on `main` with a section in [CHANGELOG.md](../CHANGELOG.md);
[release.md](release.md) covers how builds are versioned, what goes in the
changelog, and what pushing the tag publishes.

## Configuration Integration

If the sibling `dotfiles-linux` checkout is present, run from homeskillet:

```sh
make integration      # builds, then runs ../dotfiles-linux/tests/check.sh
```

Set `INTEGRATION_REPO=<path>` if the checkout is elsewhere, or to check another
configuration repository whose `tests/check.sh` takes a manager binary, such as
`../dotfiles-windows`, which acts for Windows with `--platform windows`. The target needs
bash because the script is a bash script. Given the binary, the script links,
relinks, checks, and unlinks the real manifest in a temporary home and lists
the setup steps with `setup --dry-run`; it never touches the real home or
runs a step. The configuration repository owns that script and its other
checks, such as ShellCheck, so run those as its README describes rather than
from a command copied here.

## Cross Builds

```sh
make cross                  # every release platform: linux, darwin, and windows, each amd64 and arm64
make cross-darwin-arm64     # one platform
```

Each target writes `bin/homeskillet-<os>-<arch>`, with `.exe` for Windows, with
`CGO_ENABLED=0` and `-trimpath`, so binaries are static and contain no local
filesystem paths. A release builds the same way ([release.md](release.md#tagging)).

Cross-compilation verifies builds, not native macOS execution. Native checks on
a Mac are still required before claiming runtime verification there.

## Working With Both Repositories

Inspect both worktrees before editing. Configuration and manager changes have
separate Git histories. Do not revert unrelated or concurrent user edits, and
do not commit either repository without a request.