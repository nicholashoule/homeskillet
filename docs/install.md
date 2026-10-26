# Installing and Updating

How homeskillet records its version and keeps installed versions side by side.
[README.md](../README.md#build-and-install) covers building and the first
install.

## Downloading a Release

Each version tag publishes the static binaries `make cross` builds, for Linux
and macOS, in two places. Both hold plain binaries, not container images. In
the commands below, `OWNER` is the GitHub account that hosts homeskillet. On the
repository's Releases page, each is `homeskillet-<os>-<arch>`, with a
`SHA256SUMS` file to check it against:

```sh
gh release download v0.1.1 --repo OWNER/homeskillet --pattern homeskillet-linux-amd64 --pattern SHA256SUMS
grep ' homeskillet-linux-amd64$' SHA256SUMS | sha256sum --check    # macOS: shasum -a 256 --check
chmod +x homeskillet-linux-amd64
./homeskillet-linux-amd64 self install
```

On ghcr.io, which the repository's Packages page lists,
`oras pull --platform` fetches only the binary for one OS and architecture, as
`homeskillet`, and checks it against its digest. `v0.1.1-linux-arm64` and the
like name each platform's artifact directly. While the package is private, run
`oras login ghcr.io` first with a token that can read packages.

```sh
oras pull --platform linux/arm64 ghcr.io/OWNER/homeskillet:v0.1.1
chmod +x homeskillet
./homeskillet self install
```

`self install` installs the downloaded binary as `make install` installs a
build (see [Versions and Updates](#versions-and-updates)); delete the download
afterward. Release builds are reproducible: building the tag with the Go
release the workflow used gives the same `SHA256SUMS`
([docs/development.md](development.md#versions-and-releases)).

## Shell Completion

`homeskillet completion bash` and `homeskillet completion zsh` print a
completion script for commands, options, platforms, setup step names, and
installed versions. Load it from your shell's startup file, so it always
matches the installed version:

```sh
# ~/.bashrc
command -v homeskillet >/dev/null && eval "$(homeskillet completion bash)"
# ~/.zshrc, after compinit
command -v homeskillet >/dev/null && eval "$(homeskillet completion zsh)"
```

For zsh, the script may instead be saved as `_homeskillet` in a directory on
`fpath`. The bash script works with bash 3.2, which macOS ships, and later.

The scripts are fixed text and ask homeskillet for each completion through a
hidden `homeskillet __complete` command. Working out candidates only reads,
as `status` and `self list` do: it never runs a setup step or changes a file,
and running as root it reads nothing without `--allow-root`. Candidates with
control characters are never offered, so a file name cannot rewrite the
terminal.

## Version Information

`homeskillet version` (or `--version`) prints the version and the build
details recorded in the binary:

```text
homeskillet v0.0.1
  commit:    0123456789ab
  built:     2026-10-25T12:00:00Z
  go:        go1.24.2
  platform:  linux/amd64
```

`commit` names the commit the binary was built from, abbreviated as Go's
pseudo-versions abbreviate it, with `(modified)` when the tree had
uncommitted changes; the commit is the reference for everything else about the
source, including its date. `built` is when `make` built the binary, in UTC.
Go records no build time, so the Makefile stamps it (see
[tools/buildtime](../tools/buildtime/main.go)); a plain `go build` or
`go install` leaves the line out. For a reproducible build, set
`SOURCE_DATE_EPOCH` to fix the time, or `BUILT=` to leave it out.

`platform` is the platform the binary was built for, which homeskillet checks
against the one it detects it is running on. When they differ, as when macOS
runs an `amd64` build on Apple silicon through Rosetta 2, `version` shows
`built for` and `host` instead, and `self install` points it out too, since a
native `arm64` build is the one to install.

Go records the version and commit when it builds from a Git checkout: the
tag, such as `v0.0.1`, or a pseudo-version such as
`v0.0.2-0.20261025120000-0123456789ab` for a commit after the latest tag, with
`+dirty` when the tree had edits. A checkout with no commits records no
version, so its builds report `(devel)` and cannot be installed: commit, then
tag a release with `git tag v0.0.1`.

Where no Git metadata exists, such as a container build whose
`.dockerignore` excludes `.git`, stamp a version with
`make install VERSION=v0.0.1` (or `make build VERSION=v0.0.1` to only build).
In a checkout, tag rather than stamp: a stamped `v0.0.1` differs from the
build you later tag `v0.0.1`, and an installed version is never rewritten.

## Versions and Updates

Each install keeps its own copy in the
[data directory](../README.md#data-directory), and symlinks choose which one
runs. With the default data directory:

```text
~/.local/share/homeskillet/versions/v0.0.1/homeskillet
~/.local/share/homeskillet/versions/v0.0.2/homeskillet
~/.local/share/homeskillet/latest   -> versions/v0.0.2
~/.local/share/homeskillet/previous -> versions/v0.0.1
~/.local/share/homeskillet/current  -> latest
~/.local/bin/homeskillet            -> ~/.local/share/homeskillet/current/homeskillet
```

- `latest` and `previous` always name the highest and second-highest installed
  versions. Installing or removing a version recomputes both.
- `current` chooses what the `homeskillet` command runs. It follows `latest`
  unless you pin a version, so updating is `git pull` then `make install`.
- Every change replaces a link in one atomic rename, and a new binary is
  written under a temporary name first, so the command never disappears or
  runs a half-written file.

```sh
homeskillet self list                 # versions, roles, and recorded build details
homeskillet self use previous         # roll back; pins that version
homeskillet self use v0.0.1           # pin any installed version
homeskillet self use latest           # follow the newest again
homeskillet self remove v0.0.1        # delete a version you no longer need
homeskillet self install --dry-run    # preview; the self commands all take it
```

A pinned version stays pinned when you install another; `self install` says
so. `self remove` refuses the version that runs. An installed version is never
rewritten: installing the same version with different contents is refused,
except for `+dirty` builds, which name no exact build. Rebuilding a release
from the same commit, with the same Go and platform, differs only in its build
time, so it counts as already installed and the installed binary stays. A
clean build ranks above a `+dirty` build of the same version, so `latest`
keeps naming the
release. If `~/.local/bin/homeskillet` already exists and is not
homeskillet's link, such as a binary from `go install`, install stops and asks
you to move it aside.

## Data Directory

homeskillet keeps its own files in one data directory: the installed versions
(see [Versions and Updates](#versions-and-updates)), `links.json`, a record of
the links it made, and `backups/`, the files `link --force` moved aside, each
at its path in home with the Unix time appended. homeskillet never deletes a
backup; remove one once you no longer need it. The default is
`~/.local/share/homeskillet`. Backups are moved there by renaming, so a data
directory on another file system than home makes `--force` fail, changing
nothing. To keep it
elsewhere, set `HOMESKILLET_DATA_DIR` in your shell startup file, or pass
`--data-dir` to a single command:

```sh
export HOMESKILLET_DATA_DIR='~/.config/homeskillet'   # quoted: homeskillet expands ~
homeskillet link /path/to/dotfiles --data-dir /srv/homeskillet
```

A path starting with `~/`, or a relative one, means a directory in the selected
home, so the same setting also works for a scratch home given with `--home`.
An absolute path is used as is, for any home; the record keeps each home's
links apart. `--data-dir` wins over `self --prefix`, which wins over the
variable. The data directory must pass the same ownership and system-directory
rules as everything else, and may not be inside the configuration repository,
where the record would end up in Git. Use the same setting for every command,
or homeskillet will look for its record in the wrong place.
