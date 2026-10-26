# The Manifest

`dotfiles.json`, at the root of a configuration repository, is optional. A
repository with only a `home/` directory works without one: every file under
`home/` is linked to the same path in your home directory. A manifest adds what
`home/` alone cannot say:

| Field | What it is for |
|---|---|
| `platforms` | The operating systems, and optionally architectures, the repository is for. Files and steps for other platforms are left alone. |
| `setup` | Commands that install what the linked files use, such as a prompt or a font. homeskillet shows each one and runs it only when you approve it. |
| `files` | An explicit list of files to link, in place of everything under `home/`: for a target that differs from the source's layout, or a file meant for only some platforms. |

Any field may be left out. A manifest that omits `files` keeps linking
everything under `home/`, so most repositories use the manifest only for
`platforms` and `setup`. A repository with neither `home/` nor a manifest is
refused, because that usually means the repository path names the wrong
directory.

## Creating One

`homeskillet init` writes a starter manifest into a repository: this machine's
operating system as the default platform, and an empty `setup` list. It leaves
out `files`, so files keep being found under `home/`, which it creates when
missing:

```sh
homeskillet init ~/dotfiles                       # write ~/dotfiles/dotfiles.json
homeskillet init ~/dotfiles --dry-run             # print it instead
homeskillet init ~/dotfiles --platform darwin     # a starter for a macOS repository
```

```json
{
  "platforms": ["linux"],
  "setup": []
}
```

`init` never replaces an existing `dotfiles.json`, and refuses a directory that
another user could write to, as the manifest decides what runs. Remove
`platforms` to apply everywhere.

## Files

When the manifest lists `files`, exactly those are linked and `home/` gets no
special treatment:

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
the selected home directory. All sources must be regular files; no particular
source directory layout is required. Files not listed are ignored, so a file
added under `home/` later needs an entry too. `"files": []` links nothing, for
a repository that only declares setup steps.

## Platforms

A repository for one operating system says so once, at the top of its
manifest, and that applies to every file and setup step, including files
found under `home/`:

```json
{
  "platforms": ["darwin"],
  "setup": [
    { "name": "homebrew", "run": ["bash", "setup/homebrew.sh"], "provides": "brew" },
    { "name": "rosetta", "run": ["softwareupdate", "--install-rosetta", "--agree-to-license"],
      "platforms": ["darwin/arm64"] }
  ]
}
```

A platform is an operating system, `linux`, `darwin`, or `windows`, optionally
with an architecture, `amd64` or `arm64`, as in `darwin/arm64`. Without an
architecture it covers both. homeskillet does not run on Windows yet, but a
repository may target it already; files and steps meant for Windows must use
names Windows can create (see [windows.md](windows.md)). An entry or step that
sets its own `platforms` replaces the manifest's default rather than narrowing
it; to apply everywhere despite a narrower default, list both operating
systems. Omit `platforms` entirely to apply everywhere. Unknown values are
rejected, so a typo such as `macos` or `darwin/arm` fails instead of silently
matching nothing.

homeskillet detects the platform it runs on, its operating system and
architecture (see `homeskillet version`). If the repository has files or steps
but none for that platform, `status`, `link`, and `setup` fail and say so,
rather than succeeding without doing anything; `unlink` still runs, so links
made before can be removed. Detection is best effort, so `--platform`
overrides it:

```sh
homeskillet setup . --platform darwin/amd64           # Intel steps on Apple silicon, under Rosetta
homeskillet link . --home "$(mktemp -d)" --platform darwin   # check a macOS repository from Linux
homeskillet setup . --platform darwin --dry-run       # list another OS's steps
```

Setup steps for another operating system can only be listed, never run, so
`setup` with a foreign `--platform` requires `--dry-run`.

## Setup Steps

Setup steps are declared only in the manifest; homeskillet never infers a
command. This manifest declares setup steps and, because it omits `files`,
still links everything under `home/`:

```json
{
  "setup": [
    {
      "name": "uv",
      "run": ["sh", "setup/uv.sh"],
      "platforms": ["linux", "darwin"],
      "creates": ".local/bin/uv"
    },
    {
      "name": "brew-tools",
      "run": [
        ["brew", "install", "ripgrep", "jq"],
        ["brew", "install", "--cask", "font-meslo-lg-nerd-font"]
      ],
      "platforms": ["darwin"],
      "provides": "rg"
    }
  ]
}
```

One repository serves both systems: on Linux `setup` offers uv, and on macOS
uv and then the Homebrew tools.

| Field | Meaning |
|---|---|
| `name` | Required. Lowercase letters, digits, `.`, `_`, or `-`, starting with a letter or digit; unique. |
| `run` | Required. One command, `["brew", "install", "jq"]`, or a list of commands, `[["brew", "update"], ["brew", "install", "jq"]]`. Each runs directly, not through a shell; use `["sh", "-c", "..."]` when a command needs pipes or other shell features. |
| `platforms` | Optional. Replaces the manifest's default `platforms` for this step (see [Platforms](#platforms)). |
| `creates` | Optional path relative to home. If it exists, the step is done and not offered. |
| `provides` | Optional command name. If it is on `PATH`, in the home's `.local/bin`, or in one of Homebrew's standard `bin` directories (see below), the step is done and not offered. |

A list of commands groups work that belongs together into one step: they are
shown together, approved once, and run in order, and the first that fails
stops the step and setup.

### Running Steps

```sh
homeskillet setup ~/dotfiles                  # offer every pending step
homeskillet setup ~/dotfiles uv brew-tools    # only these steps
homeskillet setup uv                          # one step of the default repository
homeskillet setup ~/dotfiles --dry-run        # list every step and its state
```

Names after `setup` select steps, which still run in manifest order, since a
later step may rely on an earlier one. A name matching no step for this
platform fails before anything runs. A repository given before them must
plainly be a path (`.`, `..`, or containing `/`), as a step name never is, so
write `./dotfiles` rather than `dotfiles`.

Every step is listed. A step that is done says what shows it, so you can tell
why it was not offered:

```text
done	gitconfig
  found: /home/user/.gitconfig
pending	dotnet-sdk
  run: bash setup/install-dotnet.sh
  in:  /home/user/dotfiles
Run this step? [y/N]
```

Steps run from the repository root, so repository scripts such as
`setup/uv.sh` work. `HOME` and `HOMESKILLET_HOME` are set to the selected
home, and `HOMESKILLET_REPO` to the repository. A step without `creates` or
`provides` is offered on every run, so make it safe to repeat. A failing step
stops setup; declined steps are skipped and the rest are still offered.
`setup --yes` runs every pending step without asking, for unattended builds
such as a Dockerfile; [safety.md](safety.md#setup-steps) explains what that
does and does not allow.

### PATH

Many installers, such as uv's and pipx's, put programs in `~/.local/bin`, which
a fresh shell often does not have on `PATH` until the next login. So that a
step, or a later command in the same step, can run a program installed just
before it, commands run with the selected home's `.local/bin` appended to
`PATH`, and `provides` and bare command names are looked up the same way. It is
appended, so it cannot shadow a system command such as `bash` or `curl`, and
only when it passes the ownership rules, so a directory others can write never
joins `PATH`. Relative `PATH` entries are never searched.

`provides` also looks in Homebrew's `bin` directories, which shells add to
`PATH` only after `brew shellenv`, so a program Homebrew just installed is not
offered again. Commands do not get them on `PATH`, since Homebrew usually lets
the `admin` group write them; call `brew` by its full path right after
installing it.
