# homeskillet

A small, dependency-free Go CLI for bootstrapping anyone's dotfiles on Linux
and macOS. Configuration belongs in separate repositories. homeskillet links
the files a repository keeps under `home/`, or the files its optional
`dotfiles.json` manifest lists. After you approve each one, it also runs the
setup steps the manifest declares, such as installing oh-my-posh. It does not
ship or assume any particular dotfiles.

## Supported Environments

homeskillet creates symlinks and runs setup steps directly rather than through
your shell, so it works the same whatever your login shell is.

| Environment | Notes |
|---|---|
| Linux with bash | Any distribution. Tested. |
| macOS with bash or zsh | zsh is the macOS default and reads `.zshrc`/`.zprofile`, not the bash files. Each shell reads only its own startup files, so a manifest can list both. Cross-compiled but not yet tested natively. |
| Windows via WSL (Ubuntu) | Run homeskillet inside WSL. Clone the configuration repository into the Linux filesystem (for example under `~/`), not under `/mnt/c`. Windows drives show every file as world-writable and ignore `chmod` by default, so homeskillet refuses them. |
| Docker and other Linux containers | Containers often run as root, which needs `--allow-root` and a repository owned by root inside the container. A repository bind-mounted from the host belongs to another user and is refused for root. Alternatively, run as a regular user that owns the clone and has a real home directory. Builds that exclude `.git` record no version; pass `make build VERSION=v0.0.1`. |

Native Windows (cmd.exe or PowerShell) is not supported.

## Build and Install

With Go 1.24 or newer, from this repository:

```sh
make install          # build, then install as a new version under ~/.local
homeskillet version
```

`make install` builds `bin/homeskillet` and runs its `self install`, which
copies the binary into a directory of its own version and points the
`homeskillet` command at it (see [Versions and Updates](#versions-and-updates)).
The command lands in `~/.local/bin`; homeskillet says so if that directory is
not on your PATH, which on macOS it usually is not. `make install PREFIX=<dir>`
installs under another prefix.

`make build` alone leaves the binary in `bin/` without installing it. Run `make`
to list every target. The Makefile works with the GNU Make 3.81 that ships with
macOS and with any Linux distribution, WSL, or container. Its recipes use only
Go tools, the bundled `tools/xplat` helper, and the homeskillet binary they
build.

### Version Information

`homeskillet version` (or `--version`) prints the version and the build
details recorded in the binary:

```text
homeskillet v0.0.1
  commit:    0123456789abcdef0123456789abcdef01234567
  committed: 2026-10-25T12:00:00Z
  go:        go1.24.2
  platform:  linux/amd64
```

Go records these when it builds from a Git checkout: the tag, such as
`v0.0.1`, or a pseudo-version such as `v0.0.2-0.20261025120000-0123456789ab`
for a commit after the latest tag, with `+dirty` and "(uncommitted changes)"
when the tree had edits. A checkout with no commits records no version, so
its builds report `(devel)` and cannot be installed: commit, then tag a
release with `git tag v0.0.1`.

Where no Git metadata exists, such as a container build whose
`.dockerignore` excludes `.git`, stamp a version with
`make install VERSION=v0.0.1` (or `make build VERSION=v0.0.1` to only build).
In a checkout, tag rather than stamp: a stamped `v0.0.1` differs from the
build you later tag `v0.0.1`, and an installed version is never rewritten.

### Versions and Updates

Each install keeps its own copy, and symlinks choose which one runs:

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
homeskillet self list                 # versions, roles, and build details
homeskillet self use previous         # roll back; pins that version
homeskillet self use v0.0.1           # pin any installed version
homeskillet self use latest           # follow the newest again
homeskillet self remove v0.0.1        # delete a version you no longer need
homeskillet self install --dry-run    # preview; the self commands all take it
```

A pinned version stays pinned when you install another; `self install` says
so. `self remove` refuses the version that runs. An installed version is never
rewritten: installing the same version with different contents is refused,
except for `+dirty` builds, which name no exact build. A clean build ranks
above a `+dirty` build of the same version, so `latest` keeps naming the
release. If `~/.local/bin/homeskillet` already exists and is not
homeskillet's link, such as a binary from `go install`, install stops and asks
you to move it aside.

## Use

Pass the path to a separate configuration repository:

```sh
homeskillet status --repo /path/to/dotfiles
homeskillet link --repo /path/to/dotfiles --dry-run
homeskillet link --repo /path/to/dotfiles
homeskillet unlink --repo /path/to/dotfiles --dry-run
homeskillet unlink --repo /path/to/dotfiles
homeskillet setup --repo /path/to/dotfiles --dry-run
homeskillet setup --repo /path/to/dotfiles
```

`bin/homeskillet` works the same before you install it. Options follow the
command. `--repo` defaults to the current working directory, not the
executable's directory. The default command is `status`, which never changes
files. `--home` selects an existing alternative home directory; otherwise
the current user's home is used. `--dry-run` previews without creating anything
or running any setup step. `setup --yes` runs every pending step without asking;
use it only for unattended builds such as a Dockerfile, where you already chose
the repository and its revision.

Keep the configuration clone in a permanent location. Symlinks point to absolute
paths inside that clone, not to the manager repository. The manager can be moved
or rebuilt without changing installed links.

## Choosing Files

### The `home/` Directory

The simplest configuration repository needs no manifest. Put files under a
`home/` directory at its root, laid out as they belong in your home directory:

```text
dotfiles/
└── home/
    ├── .bashrc                          -> ~/.bashrc
    ├── .bashrc.d/aliases.sh             -> ~/.bashrc.d/aliases.sh
    └── .config/oh-my-posh/theme.json    -> ~/.config/oh-my-posh/theme.json
```

Every file under `home/` is linked on its own, on both operating systems.
Directories are created as needed and never linked themselves, so other files
in `~/.config` are left alone. homeskillet skips `.DS_Store`, which macOS
Finder creates, and `.git`, which holds a nested repository's metadata. `home/`
may be a symlink to another directory inside the repository, for example to
choose between per-machine layouts.

A repository with neither `home/` nor a manifest is refused, because that
usually means `--repo` names the wrong directory.

### The Manifest

For more control, place `dotfiles.json` at the repository root. When it lists
`files`, exactly those are linked and `home/` gets no special treatment:

```json
{
  "files": [
    { "source": "home/.bashrc", "target": ".bashrc" },
    {
      "source": "home/.config/example/config.toml",
      "target": ".config/example/config.toml",
      "platforms": ["linux"]
    }
  ]
}
```

Sources are relative to the configuration repository. Targets are relative to
the selected home directory. Omit `platforms` for both operating systems, or use
`linux` and/or `darwin`. All sources must be regular files; no particular source
directory layout is required. Files not listed are ignored.

A manifest that omits `files` keeps linking everything under `home/`, which
lets a repository use the manifest only for setup steps. `"files": []` links
nothing.

### Setup Steps

Setup steps are declared only in the manifest; homeskillet never infers a
command. This manifest adds two steps and, because it omits `files`, still
links everything under `home/`:

```json
{
  "setup": [
    {
      "name": "oh-my-posh",
      "run": ["bash", "setup/oh-my-posh.sh"],
      "platforms": ["linux"],
      "creates": ".local/bin/oh-my-posh"
    },
    {
      "name": "oh-my-posh-macos",
      "run": ["brew", "install", "jandedobbeleer/oh-my-posh/oh-my-posh"],
      "platforms": ["darwin"],
      "provides": "oh-my-posh"
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `name` | Required. Lowercase letters, digits, `.`, `_`, or `-`; unique. |
| `run` | Required. The command and its arguments, run directly, not through a shell. Use `["sh", "-c", "..."]` when a step needs pipes or other shell features. |
| `platforms` | Optional, as for files. |
| `creates` | Optional path relative to home. If it exists, the step is done and not offered. |
| `provides` | Optional command name. If it is on `PATH`, the step is done and not offered. |

Steps run in manifest order from the repository root, so repository scripts
such as `setup/oh-my-posh.sh` work. `HOME` and `HOMESKILLET_HOME` are set to
the selected home, and `HOMESKILLET_REPO` to the repository. A step without
`creates` or `provides` is offered on every run, so make it safe to repeat. A
failing step stops setup; declined steps are skipped and the rest are still
offered.

## Safety

`status` reports `missing`, `linked`, `conflict`, or `no-source` (the source
file is absent) for each entry. Existing files, directories, and unrelated
symlinks are never overwritten, even if their contents match a source. Back them
up manually before linking. Conflicts and missing sources make `status` and
`link` exit nonzero, and `link` then changes nothing. Escaping paths, overlapping
targets, source symlinks resolving outside the repository, symlinked target
parents, and paths or file names containing control or formatting characters
are rejected.

Linked files such as `.bashrc` run as you, so `status` and `link` refuse a
repository that another user could change, following OpenSSH's rules for
`~/.ssh`:

- The repository, its manifest if it has one, and every directory and file
  leading to a source must be owned by you and not writable by group or
  others. Files found under `home/` are checked exactly like listed ones.
- The manifest may be a symlink only to a file inside the repository, and the
  directories leading to that file are checked too.
- Each directory above the repository must be owned by you or root, and be
  writable only by its owner unless it is sticky, like `/tmp`.
- Running as root is refused unless `--allow-root` is passed, and root must
  then own the repository. This stops `sudo homeskillet link` from pointing
  root's shell files at files your account can edit.

Parent directories that `link` creates are private (`0700`); existing ones are
left unchanged.

`unlink` removes only listed symlinks pointing to the selected configuration
repository, including dangling links. Anything else at a listed target is
reported as `skipped` and left alone, and the other entries are still unlinked.
`unlink` skips the ownership checks, so it can always clean up. It does not
remove sources or parent directories, or restore backups. `unlink` only knows
the files the repository declares now, so run it before removing or renaming
a file under `home/` or a manifest entry, and before moving or deleting a
configuration clone.

A setup step is code from the repository, which may be someone else's, so
`setup` relies on your review:

- Every file and directory in the repository must pass the ownership rules
  above, not just the manifest, because a step can run or source any of them.
- Each pending step is shown exactly as it will run, with its arguments quoted
  and its working directory, before you are asked. Only `y` or `yes` runs it.
  Anything else, including closed or redirected input, declines.
- Commands run without a shell unless the step names one, so you see any
  pipeline or substitution in the review.
- Setup names and arguments cannot contain control or formatting characters,
  which could hide part of a command from the review.
- `HOME` is set to the selected home, so `--home` pointed at a scratch
  directory keeps installers that honor `HOME` out of your real one.

Review a step before approving it. homeskillet cannot tell what an installer
will do once it runs.

The installed `homeskillet` command runs as you too, so `self install` applies
the same ownership rules to what it copies and where it copies it:

- The binary being installed must be a regular file owned by you or root and
  writable by nobody else. Under `sudo --allow-root`, that refuses a binary
  your own account could have changed.
- The installation directories, and every directory above them, follow the
  repository rules. New directories are private (`0700`), and installed
  binaries are executable only by you.
- `self remove` deletes only the binary and temporary files that install
  creates, never anything else found in a version directory.

Operations are idempotent but not transactional. An I/O failure can leave a
partial installation; resolve it and rerun. Do not modify the manifest or
directory structure concurrently with the CLI. Cloning from git, templating, and
secret management remain outside the manager's scope.

## Checks

```sh
make check            # go vet, golangci-lint, and go test -race
```

Tests create their own temporary repositories, with and without manifests.
They require no particular configuration repository and do not change your
home directory.

## Documentation

- [Architecture](docs/architecture.md): ownership, execution flow, and safety limits.
- [Development](docs/development.md): build, test, and cross-repository checks.
- [Agent handoff](docs/handoff.md): current state, decisions, and remaining work.