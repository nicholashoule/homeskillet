# Architecture

## Repository Boundaries

`homeskillet` owns the Go CLI, module, generic tests, and generated binaries.
It is independent of any particular configuration repository. The module is
`github.com/nicholashoule/homeskillet`, requires Go 1.24 or newer, and uses
only the standard library.

Configuration repositories own the installable files, an optional
`dotfiles.json` manifest, and any configuration-specific checks. The interface
between the two is small and documented in the README:

- Files under the repository's `home/` directory mirror the home directory and
  are linked when the manifest is absent or omits `files`.
- A manifest's `files` list replaces that convention with explicit entries,
  and its `setup` list declares setup steps, which are never inferred.

Do not move Go code back into a configuration repository or add runtime
dependencies on the sibling checkout.

## Execution Flow

`main.go` only calls `cli.Run`. The packages under `internal/` divide the work:

| Package | Owns |
|---|---|
| `internal/cli` | Arguments, root refusal, path canonicalization, printed output |
| `internal/config` | Reading a repository: manifest decoding, `home/` discovery, lexical validation |
| `internal/trust` | Ownership and permission rules for the repository |
| `internal/link` | Link planning and changes: states, conflicts, containment |
| `internal/setup` | Setup steps: state checks, review prompts, execution |
| `internal/install` | Versioned installs of homeskillet itself: the `latest`, `previous`, and `current` links |
| `internal/version` | Build details (version, commit, toolchain) and semantic version ordering |

A command runs in this order:

1. `cli.Run` parses the command and flags. The default is `status`. A flag
   that does not apply to the command is an error rather than silently
   ignored. `version` prints `version.Current` and stops. Every other command
   refuses to run as root without `--allow-root`; `self` then continues as
   described under [Installing homeskillet](#installing-homeskillet).
2. `canonicalDirectory` resolves the repository and existing home directory.
   Packages link and trust compare path strings, which is only sound for
   canonical paths.
3. `config.Load` opens the repository as an `os.Root`, so no read can leave it.
   It decodes the manifest if there is one, rejecting unknown fields, trailing
   JSON values, escaping paths, and control or formatting characters. If there
   is no manifest, or it omits `files`, Load lists every file under `home/`
   instead, skipping `.DS_Store` and `.git`, and validates each discovered
   entry with the same rules. It fails if the repository has neither. `FilesFor`
   and `SetupFor` select the entries for the running OS.
4. For links, `link.Prepare` checks repository ownership (except for `unlink`)
   and validates every selected source, target parent, and target state. It
   changes nothing, so any failure leaves the home directory untouched.
   `cli.Run` prints the plans for status, dry runs, or blocked links. Otherwise
   `link.Create` makes missing symlinks or `link.Remove` deletes owned ones.
5. For setup, `setup.Prepare` returns at once when there are no steps for this
   OS. Otherwise it requires the whole repository tree to be private and marks
   steps done when their `creates` path exists or their `provides` command is
   on `PATH`, without running anything. Dry runs list the steps.
   Otherwise `setup.Run` shows each pending step, asks for approval (default
   no) unless `--yes` was given, and runs approved steps in order, stopping at
   the first failure.

The ownership and containment rules are isolated in `internal/trust` so they
can be audited in one place. Discovered and listed entries reach
`link.Prepare` in the same form, so every check after step 3 treats them alike.

`--repo` defaults to the current working directory, not the executable location.
`--home` defaults to the user's home. Options follow the command. Only Linux
and macOS (`darwin`) are supported; a build constraint enforces this because the
ownership checks read Unix file metadata.

## Installing homeskillet

`self` commands manage copies of the homeskillet binary itself under a prefix,
`~/.local` unless `--prefix` says otherwise. `install.New` resolves the
prefix's existing directories, then:

- `self install` copies the running program, found with `os.Executable`, as
  the version `version.Current` reports. The copy is named by version under
  `share/homeskillet/versions/`, and `latest` and `previous` are recomputed
  as the highest and second-highest installed versions. `current` is created
  following `latest` if it is missing, and `bin/homeskillet` is created
  linking to `share/homeskillet/current/homeskillet` if it is missing.
- `self use` points `current` at `latest` (follow) or at one version (pin).
  `previous` is resolved when the command runs, so a later install never
  changes a pin.
- `self remove` deletes one version that is not running, after moving
  `latest` and `previous` off it.
- `self list` reports versions and roles, reading each binary's build details
  with `debug/buildinfo` instead of running it.

Like `link.Prepare`, each command checks everything before changing anything,
and dry runs stop after the checks.

## Safety Contract

- Sources are regular files inside the selected configuration repository.
- Targets are relative to the selected home and cannot modify the repository.
- Overlapping selected targets and symlinked target parents are rejected.
- Manifest paths and names discovered under `home/` may not contain control or
  formatting characters, which could forge terminal output. Directory names
  are checked before the directory is read, so error messages are safe too.
- The manifest is optional. Without it, or when it omits `files`, every file
  under `home/` is an entry. An explicit `"files": []` links nothing. A
  manifest that exists but cannot be read is an error, never treated as absent.
- For `status` and `link`, the repository, the manifest if present, and every
  path to a source must be owned by the current user and not group- or
  world-writable. A symlinked manifest must resolve inside the repository, and
  the path to its destination is checked too. Ancestors must be owned by that
  user or root, and may be shared only if sticky.
- Running as root requires `--allow-root`.
- Existing files, directories, and foreign symlinks are conflicts, never replaced.
- Conflicts and missing sources block every change by `link`.
- New parent directories are created `0700`.
- `unlink` removes only listed links pointing to their expected source paths,
  including dangling owned links. It skips conflicts and ownership checks so
  cleanup is always possible.
- Dry runs do not create files or directories, and never run setup steps.
- Setup steps run only after the whole repository tree passes the ownership
  rules, and only once each step has been shown exactly and approved (or
  `--yes` was given). Unclear answers and end of input decline.
- Setup steps run as argument vectors from the repository root with `HOME` set
  to the selected home; no shell is added.
- Setup names and arguments may not contain control or formatting characters.
- `self install` copies only a regular file owned by the current user or root
  and writable by nobody else, checked on the open file so the check covers
  the bytes copied. It writes only into directories that pass the repository
  ownership rules, creating new ones `0700`.
- Installed versions are immutable. Reinstalling one with different contents
  is refused, except for `+dirty` builds. Version names must be semantic
  versions, so a version can never be a path such as `../x`.
- The `homeskillet` command in the prefix's `bin` is created only where
  nothing exists. Any other file there is a conflict, never replaced.
- New binaries are renamed into place and links are replaced by renaming a
  new link over the old one, so the command always resolves to a complete
  binary. `self remove` refuses the running version and deletes only files
  that install creates.
- Configuration must be unlinked before files under `home/` or manifest entries
  are removed or renamed, or the configuration clone is moved, because
  `unlink` only knows what the repository declares now.

Preflight is not a transaction or a concurrency guarantee, and two `self`
commands running at once can race. An application-time
I/O failure may leave partial changes. Do not change the manifest or filesystem
structure concurrently with a command. Setup safety rests on review, not
sandboxing: an approved step runs with the user's full privileges. No force
overwrite, automatic backup, Git wrapper, templating, or secret storage is
provided.