# Installing and Updating

How to install homeskillet, how it records its version, and how it keeps installed versions side by side.
[development.md](development.md) covers building it for development.

## Building and Installing

With Go 1.24 or newer, from a clone of this repository:

```sh
make install          # build bin/homeskillet, then install it as a new version
homeskillet version
```

`make install` runs the build's `self install`, which copies it into a directory of its own version in the [data
directory](#data-directory) and points the `homeskillet` command in `~/.local/bin` at it (see [Versions and
Updates](#versions-and-updates)). homeskillet says so when that directory is not on your `PATH`, as on macOS it usually
is not. `make install PREFIX=<dir>` installs under another prefix, and `make build` alone leaves the binary in `bin/`.
On Windows, see [windows.md](windows.md#installing); for WSL, `homeskillet wsl install` on Windows installs the Linux
build in a distribution ([windows.md](windows.md#the-wsl-command)).

## Downloading a Release

Each version tag publishes the static binaries `make cross` builds, for Linux, macOS, and Windows, on the repository's
Releases page: one `homeskillet-<os>-<arch>` per platform, `.exe` for Windows, with a `SHA256SUMS` file to check them
against. In the commands below, `OWNER` is the GitHub account that hosts homeskillet; on Windows, see
[windows.md](windows.md#installing).

```sh
gh release download --repo OWNER/homeskillet --pattern homeskillet-linux-amd64 --pattern SHA256SUMS   # the latest release
grep ' homeskillet-linux-amd64$' SHA256SUMS | sha256sum --check    # macOS: shasum -a 256 --check
gh attestation verify homeskillet-linux-amd64 --repo OWNER/homeskillet   # optional: that the release workflow built it
chmod +x homeskillet-linux-amd64
./homeskillet-linux-amd64 self install
```

`self install` installs the downloaded binary as `make install` installs a build (see [Versions and
Updates](#versions-and-updates)); delete the download afterward. From then on, [`homeskillet update`](#updating) does
the downloading and checking. Release builds are reproducible: building the tag with the Go release the workflow used
gives the same `SHA256SUMS` ([release.md](release.md#tagging)).

## Updating

```sh
homeskillet update                    # install the newest release, if it is not installed
homeskillet update --dry-run          # download and check it, but change nothing
homeskillet update 0.2.0              # install that release instead
homeskillet self releases             # the published releases, and which are installed
homeskillet use v0.2.0                # go back; update's closing note names the version
```

`update` asks GitHub which release is newest, downloads the release's `SHA256SUMS` and the binary for this machine into
a private directory under the [data directory](#data-directory), checks the binary against the listed SHA-256, checks
that it reports that version, and then runs its `self install` with the same `--home`, `--prefix`, and `--data-dir`,
so the new version records its own build details and the version you had stays installed (see [Versions and
Updates](#versions-and-updates)). When what runs has changed, `update` ends by naming the command that runs the old
version again; `use previous` does the same whenever the old version was the latest. The download comes only from this
project's releases on GitHub, over HTTPS, with the system's own `curl` and hashing tool at their fixed paths
([safety.md](safety.md#updating)). A version that is already installed is left as it is, so `update` is safe to run as
often as you like, and a `--dry-run` downloads and checks without running or installing anything.

The check against `SHA256SUMS` proves the download arrived intact, not who published it. Releases from the public
repository also carry signed build provenance, which `gh attestation verify` checks (see [Downloading a
Release](#downloading-a-release)); `update` does not check it yet.

`update`, `install`, and `use` are the same as `self update`, `self install`, and `self use`; versions may be written
with or without their leading `v`. A development build, whose version is a pseudo-version or `+dirty`, can update to a
release too; the release then ranks as `latest` only if its version is higher.

## Shell Completion

`homeskillet completion bash`, `zsh`, and `pwsh` print a completion script for commands, options, platforms, setup step
names, and installed versions. Load it from your shell's startup file, so it always matches the installed version; for
PowerShell, see [windows.md](windows.md#installing):

```sh
# ~/.bashrc
command -v homeskillet >/dev/null && eval "$(homeskillet completion bash)"
# ~/.zshrc, after compinit
command -v homeskillet >/dev/null && eval "$(homeskillet completion zsh)"
```

For zsh, the script may instead be saved as `_homeskillet` in a directory on `fpath`. The bash script works with bash
3.2, which macOS ships, and later.

The scripts are fixed text and ask homeskillet for each completion through a hidden `homeskillet __complete` command,
which only reads ([safety.md](safety.md#starter-manifests-checks-and-completion)).

## Version Information

`homeskillet version` (or `--version`) prints the version and the build details recorded in the binary:

```text
homeskillet v0.0.1
  commit:    0123456789ab
  built:     2026-10-25T12:00:00Z
  go:        go1.24.2
  platform:  linux/amd64
```

`commit` names the commit the binary was built from, abbreviated as Go's pseudo-versions abbreviate it, with
`(modified)` when the tree had uncommitted changes; the commit is the reference for everything else about the source,
including its date. `built` is when `make` built the binary, in UTC. Go records no build time, so the Makefile stamps it
(see [tools/buildtime](../tools/buildtime/main.go)); a plain `go build` or `go install` leaves the line out. For a
reproducible build, set `SOURCE_DATE_EPOCH` to fix the time, or `BUILT=` to leave it out.

`platform` is the platform the binary was built for, which homeskillet checks against the one it detects it is running
on. When they differ, as when macOS runs an `amd64` build on Apple silicon through Rosetta 2, `version` shows `built
for` and `host` instead, and `self install` points it out too, since a native `arm64` build is the one to install.

Go records the version and commit when it builds from a Git checkout: the tag, such as `v0.0.1`, or a pseudo-version
such as `v0.0.2-0.20261025120000-0123456789ab` for a commit after the latest tag, with `+dirty` when the tree had edits.
A checkout with no commits records no version, so its builds report `(devel)` and cannot be installed: commit, then tag
a release with `git tag v0.0.1`.

Where no Git metadata exists, such as a container build whose `.dockerignore` excludes `.git`, stamp a version with
`make install VERSION=v0.0.1` (or `make build VERSION=v0.0.1` to only build). In a checkout, tag rather than stamp: a
stamped `v0.0.1` differs from the build you later tag `v0.0.1`, and an installed version is never rewritten.

## Versions and Updates

Each install keeps its own copy in the [data directory](#data-directory), and symlinks choose which one runs. With the
default data directory, on Linux and macOS (Windows keeps `current` as a file instead; see
[windows.md](windows.md#installing-homeskillet)):

```text
~/.local/share/homeskillet/versions/v0.0.1/homeskillet
~/.local/share/homeskillet/versions/v0.0.2/homeskillet
~/.local/share/homeskillet/latest   -> versions/v0.0.2
~/.local/share/homeskillet/previous -> versions/v0.0.1
~/.local/share/homeskillet/current  -> latest
~/.local/bin/homeskillet            -> ~/.local/share/homeskillet/current/homeskillet
```

- `latest` and `previous` always name the highest and second-highest installed versions. Installing or removing a
  version recomputes both.
- `current` chooses what the `homeskillet` command runs. It follows `latest` unless you pin a version, so
  `homeskillet update`, or `git pull` then `make install`, changes what runs.
- Every change replaces a link in one atomic rename, and a new binary is written under a temporary name first, so the
  command never disappears or runs a half-written file.

```sh
homeskillet self list                 # versions, roles, and recorded build details
homeskillet self releases             # published releases, newest first, and which are installed
homeskillet self use previous         # roll back; pins that version
homeskillet self use v0.0.1           # pin any installed version
homeskillet self use latest           # follow the newest again
homeskillet self remove v0.0.1        # delete a version you no longer need
homeskillet self install --dry-run    # preview; the self commands all take it
```

A pinned version stays pinned when you install another; `self install` says so. If the pinned version's directory is
deleted by hand, the command dangles; `self list` says so, and `homeskillet self use latest` repairs it. `self remove`
refuses the version the command runs and the one running at the moment. An installed version is never rewritten:
installing the same version with different contents is refused, except for `+dirty` builds, which name no exact build.
Rebuilding a release from the same commit, with the same Go and platform, differs only in its build time, so it counts
as already installed and the installed binary stays. A clean build ranks above a `+dirty` build of the same version, so
`latest` keeps naming the release. If `~/.local/bin/homeskillet` already exists and is not homeskillet's link, such as a
binary from `go install`, install stops and asks you to move it aside.

## Data Directory

homeskillet keeps its own files in one data directory: the installed versions (see [Versions and
Updates](#versions-and-updates)), `links.json`, a record of the links it made, `setup.json`, a record of the setup steps
with `inputs` that ran ([manifest.md](manifest.md#steps-that-install-from-a-list)), and `backups/`, the files `link
--force` moved aside, each at its path in home with the Unix time appended. homeskillet deletes a backup only when you
purge it, after it shows each one and you approve ([usage.md](usage.md#conflicts-and-backups)). The default is
`~/.local/share/homeskillet`. Backups are moved there by renaming, so a data directory on another file system than home
makes `--force` fail, changing nothing. To keep it elsewhere, set `HOMESKILLET_DATA_DIR` in your shell startup file, or
pass `--data-dir` to a single command:

```sh
export HOMESKILLET_DATA_DIR='~/.config/homeskillet'   # quoted: homeskillet expands ~
homeskillet link /path/to/dotfiles --data-dir /srv/homeskillet
```

A path starting with `~/`, or a relative one, means a directory in the selected home, so the same setting also works for
a scratch home given with `--home`. An absolute path is used as is, for any home; the record keeps each home's links
apart. `--data-dir` wins over `self --prefix`, which wins over the variable. The data directory must pass the same
ownership and system-directory rules as everything else, and may not be inside the configuration repository, where the
record would end up in Git. Use the same setting for every command, or homeskillet will look for its record in the wrong
place.
