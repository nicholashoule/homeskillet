# Architecture

## Repository Boundaries

`homeskillet` owns the Go CLI, module, generic tests, and generated binaries. It is independent of any particular
configuration repository. The module is `github.com/nicholashoule/homeskillet`, requires Go 1.24 or newer, and uses only
the standard library.

Configuration repositories own the installable files, an optional manifest, `bootstrap.json`, `dotfiles.json`, or both,
and any configuration-specific checks. The interface between the two is small and documented in the README and
[manifest.md](manifest.md):

- Files under the repository's `home/` directory mirror the home directory and are linked when no manifest lists
  `files`; `home/wsl/`, when present, mirrors a WSL distribution's home.
- A manifest's `files` list replaces that convention with explicit entries, and its `setup` list declares setup steps,
  which are never inferred. Two manifests merge into one. `homeskillet init` writes a starter `bootstrap.json` where no
  manifest is.

Do not move Go code back into a configuration repository or add runtime dependencies on the sibling checkout.

## Execution Flow

`main.go` only calls `cli.Run`. The packages under `internal/` divide the work:

| Package | Owns |
|---|---|
| `internal/cli` | Arguments, root refusal, path canonicalization, printed output, help, shell completion, `init`, and `check` |
| `internal/config` | Reading a repository: manifest decoding, `home/` discovery, lexical validation |
| `internal/trust` | Ownership and permission rules for the repository |
| `internal/link` | Link planning and changes: states, conflicts, containment, the record of links and orphans |
| `internal/setup` | Setup steps: state checks, review prompts, execution |
| `internal/install` | Versioned installs of homeskillet itself: the `latest`, `previous`, and `current` links |
| `internal/safefile` | Size-limited reads and atomic, flushed writes of the files homeskillet keeps |
| `internal/version` | Build details (version, commit, toolchain) and semantic version ordering |
| `internal/host` | Detecting the operating system and architecture homeskillet runs on, including Rosetta 2; Windows known folders and the registry |
| `internal/wsl` | For `homeskillet wsl` on Windows: finding a distribution in the registry, starting it, and running commands there with `--exec` |
| `internal/mirror` | Copying a Windows clone into a distribution: sending what Windows checked, and receiving it into a private directory, whole or not at all |

A command runs in this order:

1. `cli.Run` parses the command and flags. The default is `status`. A flag that does not apply to the command is an
   error rather than silently ignored, and so is an empty option value or argument
   ([safety.md](safety.md#links-and-conflicts)). `help` and `--help` print the help text to standard output, `version`
   prints `version.Current`, `completion` prints a shell script, and the hidden `__complete` command those scripts call
   works out candidates (see completion.go); each then stops. Every other command refuses to run as root, or elevated on
   Windows (`privileged`), without `--allow-root`. `init` then writes its starter manifest (see init.go), and `check`
   reports on the repository and stops (see check.go): the steps below through `config.Load`, then `link.CheckSource`
   for every entry, for every platform, with each issue collected rather than the first returned. `wsl` (see
   wsl_windows.go) passes the command on to the Linux build in a distribution, after copying a Windows clone there
   through the hidden `__mirror` command (see wsl.go and package mirror), which that build runs on the copy it receives;
   `wsl install` records itself there through the hidden `__windows-build` command, and the Linux build's `wsl` hands
   over to that recorded program (see wsl_unix.go). The rest refuse a home directory that fails `trust.CheckLocation`;
   `self` then continues as described under [Installing homeskillet](#installing-homeskillet), and `backups` lists, or
   purges once approved, the data directory's backups (see backups.go).
2. `canonicalDirectory` resolves the repository and existing home directory, with a leading `~` as the user's home,
   which no shell expands in `cmd`. Packages link and trust compare path strings, which is only sound for canonical
   paths. The resolved repository must pass `trust.CheckLocation`. A current directory it refuses, when no repository is
   named, counts as no repository, as one without a manifest does (see `openRepository`).
3. `config.Load` opens the repository as an `os.Root`, so no read can leave it. It decodes each manifest there is,
   `bootstrap.json` and then `dotfiles.json`, rejecting unknown fields, trailing JSON values, escaping paths, and
   control or formatting characters, and merges them (`Manifest.merge`). If no manifest lists `files`, Load lists every
   file under `home/` instead, skipping `.DS_Store`, `Thumbs.db`, `desktop.ini`, and `.git`, and validates each
   discovered entry with the same rules. When `home/wsl/` exists, its files are Linux's and the rest of `home/` the
   other systems' (`discoverAll`). Targets and `creates` lose a leading `~/` or `$HOME/` (`fromHome`). Entries and steps
   meant for Windows, and every source of a repository with anything for Windows, must also use names Windows can
   create. Its error joins every issue found (`config.Issues` splits it). It fails if the repository has neither a
   manifest nor `home/`. The CLI then chooses the platform, `--platform` or else `host.Detect`, and `FilesFor` and
   `SetupFor` select the entries for it: an entry's own `platforms`, or else the manifest's default, must cover it. When
   nothing applies and the manifest declares platforms, every command but `unlink` fails rather than reporting success
   for doing nothing.
4. For links, the CLI resolves the data directory (see `dataDir`). Then `link.Prepare` checks data directory ownership,
   and repository, home, and target directory ownership except for `unlink`, and validates every selected source, target
   parent, and target state, then adds an `Orphaned` plan for each recorded link the repository no longer declares. It
   changes nothing, so any failure leaves the home directory untouched. Files named after the repository narrow it to
   those entries (`link.PrepareSelected`, with the CLI's `matcher`): only their sources and targets are checked, every
   entry still counts as declared, and a name matching nothing fails before any change. The CLI notes each link a shell
   would run instead of another command (`shadowed`), then prints the plans for status, dry runs, or blocked links.
   Otherwise `link.Backup` (only for `link --force`) moves conflicts into the data directory's `backups/`, `link.Create`
   removes orphans and makes missing symlinks, or `link.Remove` deletes owned links and orphans. If linking fails after
   `link.Backup`, `link.Restore` moves back what was not yet replaced. Either way `link.Refresh` reads each target's
   state again and `link.Save` records the links that exist, keeping other recorded links when files were named. In a
   terminal, `cli`'s view (report.go) prints a header, groups, and a closing line; otherwise each entry or change is one
   tab-separated line, the format scripts parse.
5. For setup, the CLI keeps only the steps named after `setup`, if any, in manifest order, failing first on a name that
   matches no step for this platform (`selectSteps`), and offers those even when done (`setup.Options.Again`).
   `setup.Prepare` returns at once when there are no steps. Otherwise it requires the whole repository tree to be
   private and marks steps done when their `creates` path exists or their `provides` command is found, recording what it
   found, without running anything. A step with `inputs` is done only when, as well, `setup.json` in the data directory
   records that it last succeeded with the fingerprint it has now, of its commands and its inputs' contents (inputs.go);
   `setup.Run` records it after it succeeds. Dry runs list the steps. Otherwise `setup.Run` lists each done step with
   what was found, shows each pending step with every command in its `run`, asks for approval (default no) unless
   `--yes` was given, and runs approved steps' commands in order, stopping at the first failure. Before each step it
   checks again whether the step is done and that the tree passes `trust.CheckTree`, since an earlier step may have
   changed either. Under `--yes`, steps get no input and run in a session of their own, without the terminal
   (`runDetached`); an interrupt is passed on to them, and stops setup. Before each step, `searchPath` appends the
   home's `.local/bin` to `PATH` if it exists and passes `trust.CheckDirectory`; `provides`, the step's program, and the
   step's own environment all use that `PATH`, so a step can run what an earlier one installed. `provides` alone also
   looks in Homebrew's standard `bin` directories, when they and the program are owned by the user or root
   (`trust.CheckOwner`), which only detects a program and never runs one from there.

The ownership and containment rules are isolated in `internal/trust` so they can be audited in one place. Discovered and
listed entries reach `link.Prepare` in the same form, so every check after step 3 treats them alike.

The repository is the argument after the command, or `--repo`, in which case every argument names a file. Unnamed, it is
the current working directory, not the executable location, or, when that is not a repository, the one repository the
record of links shows linked into the home (`openRepository`, `link.Repositories`); with several recorded, none is
chosen and all are listed. The record's data directory must pass the usual location and ownership checks first, since a
repository taken from it is acted on without being named, and the header says where it came from. With no command, a
first argument that is plainly a path (`.`, `..`, or containing `/`, or on Windows `\` or a drive) means `status` for
that repository; the test is lexical so that a mistyped command is never read as a directory, and `status` changes
nothing either way. `--home` defaults to the user's home. Options go anywhere after the command. `--quiet` replaces the
output with nothing, leaving errors and the exit status; `setup` refuses it, since its steps must be shown before
approval. Linux, macOS (`darwin`), and Windows are supported; a build constraint enforces this because the ownership
checks read each system's own file metadata, in `_unix.go` and `_windows.go` files
([windows.md](windows.md#how-windows-differs)). On any other system, the program builds from `main_unsupported.go`
instead, which only says that the system is not supported.

## Installing homeskillet

`self` commands manage copies of the homeskillet binary itself under a prefix, `~/.local` unless `--prefix` says
otherwise. `install.New` resolves the prefix's existing directories, then:

- `self install` copies the running program, found with `os.Executable`, as the version `version.Current` reports. The
  copy is named by version under `share/homeskillet/versions/`, and `latest` and `previous` are recomputed as the
  highest and second-highest installed versions. `current` is created following `latest` if it is missing, and
  `bin/homeskillet` is created linking to `share/homeskillet/current/homeskillet` if it is missing. On Windows,
  `current` is a file, `latest` and `previous` are computed rather than linked, and `bin/homeskillet.exe` links to the
  chosen version's program (`pointCommand`, [windows.md](windows.md#installing-homeskillet)). A build Go made from a
  downloaded module, as `go run module@version` makes one (`version.Info.Downloaded`, from the module checksum Go
  records), installs only where no version is installed yet (`firstInstallOnly`). `self install VERSION` runs
  `self update VERSION` instead.
- `self use` points `current` at `latest` (follow) or at one version (pin). `previous` is resolved when the command
  runs, so a later install never changes a pin.
- `self remove` deletes one version that is neither current nor the running program, after moving `latest` and
  `previous` off it.
- `self list` reports versions, roles, and the build details in each version's `build.json`. Install writes that record
  from `version.Current`, which describes the binary being copied because it is the running program. Parsing the details
  out of each binary instead would link in `debug/buildinfo` and its ELF, Mach-O, and PE readers.
- `self update` downloads a published release, the newest or the one named, and runs it: `latestVersion` reads where
  GitHub's latest-release page redirects, `downloadRelease` fetches `SHA256SUMS` and then the binary for the detected
  hardware with the system's `curl` at a fixed path, `verifyDownload` hashes it with the system's hashing tool, and the
  download then installs itself with `self install`, as `wsl install` has a Linux build do in a distribution. The
  download is staged under the data directory, not `/tmp`, and a dry run stops before anything downloaded runs
  ([safety.md](safety.md#updating)).
- `self releases` lists the published releases from GitHub's API, marking the newest, those installed, and the current
  one. `update`, `install`, and `use` are the same commands without `self`, rewritten in `takeCommand`.
- The note that a newer release is available (`startUpdateCheck`, in `notice.go`) runs the same latest-release lookup
  alongside `status`, `link`, `unlink`, and `setup` at a terminal, once a day, recorded in `update.json` in the data
  directory, and is collected after the command's output, at most two seconds after the command started, so it never
  fails a command and slows one only on a slow network, once a day. The note is recorded as shown only once it is
  printed, and `homeskillet wsl` turns it off in the distribution through `WSLENV`.

Like `link.Prepare`, each command checks everything before changing anything, and dry runs stop after the checks.

## Safety Contract

- homeskillet acts only in a home directory, a configuration repository, and an installation prefix.
  `trust.CheckLocation` refuses system trees such as `/etc`, `/usr` (including `/usr/local`), `/proc`, and macOS's
  `/System` and `/Library`, and refuses `/`, `/home`, and `/Users` themselves, so `HOME=/` in a container or `--prefix
  /usr/local` fails before anything is read or written. On Windows it refuses drive roots, `C:\Users`, the Windows and
  program directories, and device paths ([windows.md](windows.md#system-locations)). The CLI, `link.Prepare`,
  `setup.Prepare` and `setup.Run`, and every `install.Layout` method apply it, and it also rejects relative and unclean
  paths, so an empty path never means the current directory.
- `init` checks the repository with `trust.CheckRepository` before writing, and shell completion reaches files only
  through `openRepository` and `selfLayout`, as `status` and `self list` do;
  [safety.md](safety.md#starter-manifests-checks-and-completion) has the rules both follow.
- Sources are regular files inside the selected configuration repository.
- Targets are relative to the selected home and may neither lie inside nor contain the repository or the data directory.
- Overlapping selected targets and symlinked target parents are rejected.
- Manifest paths and names discovered under `home/` may not contain control or formatting characters, which could forge
  terminal output. Directory names are checked before the directory is read, so error messages are safe too.
- Manifests are optional. When none lists `files`, every file under `home/` is an entry. An explicit `"files": []` links
  nothing. A manifest that exists but cannot be read is an error, never treated as absent. Two manifests merge;
  identical entries and steps count once, and two different steps with one name are an error rather than one silently
  replacing the other.
- For `status` and `link`, the repository, each manifest present, and every path to a source must be owned by the
  current user and not group- or world-writable. Symlinks on the way to a source or a manifest are followed one step at
  a time (`trust.Resolve`): every hop must stay inside the repository and every directory passed through must be
  private, so a hop through `/tmp` or a shared directory cannot be retargeted later. Ancestors must be owned by that
  user or root, and may be shared only if they are sticky directories; the sticky bit never excuses a regular file. The
  home directory and every existing directory a link goes into follow the repository rules (`trust.CheckDirectory`,
  `trust.CheckExisting`), since anyone who could write there could replace a link.
- Running as root, or elevated on Windows, requires `--allow-root`.
- On Windows, owners and permissions come from security descriptors, and junctions and other reparse points that are not
  directories are refused wherever ownership is checked ([windows.md](windows.md#ownership-and-permissions)). Nothing
  crosses between Windows and WSL: the Windows build refuses `\\wsl.localhost` and `\\wsl$`, and the Linux build Windows
  drives under `/mnt` ([windows.md](windows.md#windows-and-wsl-together)).
- Existing files, directories, and foreign symlinks are conflicts, never replaced except by `link --force`, which moves
  files and symlinks, never directories, into `backups/` in the data directory with the Unix time appended, never
  deleting one or overwriting a backup. It undoes its moves if one fails, and moves back what linking did not replace if
  linking fails. Only `backups purge` deletes a backup (`link.Backups`, `link.Purge`): a file or symlink named as one,
  shown first and approved, or under `--yes`.
- Conflicts and absent sources block every change by `link`; under `--force`, only absent sources do.
- New parent directories are created `0700`; on Windows they inherit the access control list of the checked directory
  they are made in.
- `unlink` removes only listed links pointing to their expected source paths, including dangling owned links. It skips
  conflicts and ownership checks so cleanup is always possible.
- Dry runs create nothing in a home, repository, or data directory, and never run setup steps. `wsl` may make a
  temporary copy of a repository in the distribution's temporary directory for one, and removes it afterward.
- Setup steps run only after the whole repository tree passes the ownership rules and every symlink in it stays inside
  the repository at every hop (`trust.CheckTree`), checked again before each step, and only once each step has been
  shown exactly and approved (or `--yes` was given). Unclear answers and end of input decline, and only the start of an
  answer is kept.
- Setup steps run as argument vectors from the repository root with `HOME` set to the selected home, and on Windows
  `USERPROFILE` too; no shell is added. The home's `.local/bin` is appended to `PATH`, never prepended, and only when it
  passes the ownership rules; on Windows, so are the registry's system and user `PATH` and winget's links directory,
  under the same rule. Command lookup skips relative `PATH` entries, and the program receives the reviewed name as
  `argv[0]`.
- Setup names and arguments may not contain control or formatting characters.
- A step's commands are shown and approved together, and each is looked up on a `PATH` computed again just before it
  runs.
- Setup steps for an operating system other than the detected one are listed with `--dry-run` only, never run.
- `self install` copies only a regular file owned by the current user or root and writable by nobody else, checked on
  the open file so the check covers the bytes copied. It writes only into directories that pass the repository ownership
  rules, creating new ones `0700`.
- Installed versions are immutable. Reinstalling one with different contents is refused, except for `+dirty` builds and
  rebuilds of the same unmodified commit with the same Go and platform, which keep the installed binary. Temporary links
  have per-process names, so concurrent `self` commands cannot disturb each other's. Version names must be semantic
  versions, so a version can never be a path such as `../x`.
- The `homeskillet` command in the prefix's `bin` is created only where nothing exists. Any other file there is a
  conflict, never replaced.
- Every file install writes, binaries and `build.json` alike, is written under a temporary name, flushed to disk,
  renamed into place, and its directory flushed, so a crash never leaves a partial file. Links are replaced by renaming
  a new link over the old one, so the command always resolves to a complete binary. `self remove` refuses the running
  version and deletes only files that install creates.
- The record of links (`links.json` in the data directory) lists, per home and per repository, every link `link` made. A
  recorded link the repository no longer declares is `Orphaned` only while it is still a symlink naming its recorded
  source; `link` and `unlink` remove it, and nothing else is ever removed for that reason. The record is validated on
  every read, so targets stay inside their home and sources inside their repository, and it never lives in a repository.
  It is written after every `link` and `unlink`, even one that failed partway, from the targets as they are. Homes that
  do not exist now are kept, since an unmounted volume looks like a deleted home.
- The record of setup steps with inputs (`setup.json` in the data directory) keeps, per home and per repository, each
  such step's fingerprint when it last succeeded. It can only keep a step from being offered, never run one, and is
  validated on every read all the same.
- The data directory, from `--data-dir`, `self --prefix`, `HOMESKILLET_DATA_DIR`, or `~/.local/share/homeskillet` in
  that order, must pass `trust.CheckLocation` and the ownership rules and may not be inside the repository. No link
  target may be inside it.
- Configuration must be unlinked before the configuration clone is moved.

Files homeskillet parses are read with size limits: 1 MiB for each manifest, 64 KiB for a `build.json`, 4 KiB for
`update.json`, and, for `update`, 64 KiB for a release's `SHA256SUMS` and 64 MiB for a build; a copy sent
to WSL is bounded in size and in files too (package mirror). Exported functions treat a nil output writer as discarding
and a nil context as `context.Background`.

Preflight is not a transaction or a concurrency guarantee, and two `self` commands running at once can race. An
application-time I/O failure may leave partial changes. Do not change a manifest or filesystem structure concurrently
with a command. Setup safety rests on review, not sandboxing: an approved step runs with the user's full privileges. No
force overwrite, automatic backup, Git wrapper, templating, or secret storage is provided.
