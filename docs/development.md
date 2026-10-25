# Development

## Local Checks

Run from the homeskillet repository root. `make` alone lists every target.

```sh
make fmt              # golangci-lint fmt ./...
make check            # go vet ./..., golangci-lint run ./..., go test -race ./...
make build            # go build -trimpath -o bin/homeskillet .
bin/homeskillet --help
make install          # build, then bin/homeskillet self install
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

Tests cannot exercise the root refusal or files owned by another user without
root. To check the refusal by hand, `unshare -r bin/homeskillet status` runs the
binary as root inside a user namespace.

## Versions and Releases

Go 1.24 and newer stamp each build from a Git checkout with the version
(the tag, or a pseudo-version after it), the commit, its time, and whether the
tree was modified, which adds `+dirty`. `homeskillet version` prints them, and
`self list` reads them from each installed binary. A release is therefore just
a tag:

```sh
git tag v0.0.1
make install          # installs v0.0.1 and makes it latest; the old latest becomes previous
```

Until the repository has a commit, builds report `(devel)`, which `self install`
refuses. `make install VERSION=v0.0.1` (or `make build VERSION=v0.0.1`) stamps a
version with `-ldflags -X` for builds without Git metadata, such as containers
that exclude `.git`; it overrides the version only, not the commit details. In
a checkout, tag instead: a stamped build differs from the tagged one, and
installed versions are never rewritten.

To try installs without touching your own `~/.local`, point them elsewhere:

```sh
bin/homeskillet self install --prefix "$(mktemp -d)" --dry-run
bin/homeskillet self install --home /path/to/scratch/home
```

## Configuration Integration

If the sibling `dotfiles-linux` checkout is present, run from homeskillet:

```sh
make integration      # builds, then runs ../dotfiles-linux/tests/check.sh
shellcheck --shell=bash --exclude=SC1090,SC1091 \
  ../dotfiles-linux/home/.bashrc \
  ../dotfiles-linux/home/.bash_profile \
  ../dotfiles-linux/tests/check.sh
```

Set `INTEGRATION_REPO=<path>` if the checkout is elsewhere. The target needs
bash because the script is a bash script. The script checks optional Go handling, PATH idempotence, missing GNOME schemas,
and installation of the actual manifest into a temporary home. It also verifies
preview behavior, repeated linking, and unlinking. It never installs into the
real home. Without a binary argument, it runs only shell regressions and needs
neither Go nor homeskillet.

SC1090 and SC1091 cover deliberately dynamic/external startup sources. Do not
suppress behavioral warnings simply to make ShellCheck pass.

## Cross Builds

```sh
make cross                  # all of linux/darwin x amd64/arm64
make cross-darwin-arm64     # one platform
```

Each target writes `bin/homeskillet-<os>-<arch>` with `CGO_ENABLED=0` and
`-trimpath`, so binaries are static and contain no local filesystem paths.

Cross-compilation verifies builds, not native macOS execution. Native checks on
a Mac are still required before claiming runtime verification there.

## Working With Both Repositories

Inspect both worktrees before editing. Configuration and manager changes have
separate Git histories. Do not revert unrelated or concurrent user edits, and
do not commit either repository without a request. Keep project state in
[handoff.md](handoff.md) current when transferring work to another agent.