# Safety

What homeskillet refuses to do, and why. [architecture.md](architecture.md) describes how the code enforces it.

## Links and Conflicts

`status` reports `missing`, `linked`, `conflict`, or `no-source` (the source file is absent) for each entry, and
`orphaned` for a link homeskillet made earlier for a file the repository no longer has (see below). Existing files,
directories, and unrelated symlinks are never overwritten or deleted, even if their contents match a source. Conflicts
and sources absent from the repository make `status` and `link` exit nonzero, and `link` then changes nothing. Escaping
paths, overlapping targets, source symlinks resolving outside the repository, symlinked target parents, and paths or
file names containing control or formatting characters are rejected. Any other name, such as `rm -rf` or `$(touch x)`,
is only a name: homeskillet makes links with system calls and never hands a name to a shell. Put `--` before a file name
that starts with `-`, as in `homeskillet unlink ~/dotfiles -- -rf`.

An empty option value or argument, as from an unset variable in `--home "$SCRATCH"` or `unlink ~/dotfiles "$FILE"`, is
refused. Read as the default, it would mean your real home, or every file.

A linked executable that a shell finds first, ahead of a command of the same name later on `PATH`, such as
`~/.local/bin/sudo` ahead of `/usr/bin/sudo`, is linked, but `status` and `link` list it under "Shadowing commands"
(scripts get a line on standard error). `~/.local/bin` and `~/bin` count as first on `PATH` even before your shell adds
them, as many systems' startup files do. A repository from someone else could use such a link to capture what you type,
a `sudo` password included. Aliases and functions in a linked startup file can do the same unseen, and no check of file
contents could find them reliably: `eval $'\x73\x75\x64\x6f'` runs `sudo` without the word appearing. Ownership rules
keep anyone else from changing what you link, but what you link runs as you, so link only a repository you have
reviewed.

`link --force` is the one way past a conflict, and you must ask for it. It moves each conflicting file or symlink into
`backups/` in the data directory, at its path in home with the Unix time appended, then links:

- It moves, never deletes, and never overwrites an earlier backup, so anything it replaced can be restored by moving it
  back.
- It never moves a directory. A directory where the repository has a file can hold anything, even the repository itself,
  so moving it is left to you.
- No target may contain or lie inside the repository or the data directory, with or without `--force`, so a target such
  as `.config` for a clone in `~/.config/dotfiles` is refused. Paths are compared without regard to case here, since
  macOS file systems usually are, while every check that a path stays inside the repository compares exactly.
- An absent source still blocks it, so nothing moves when linking would fail.
- If any move fails, the moves already made are undone. If linking fails after the moves, each file not yet replaced by
  its link is moved back, and the links already made are recorded.
- Backups stay out of home, so a program that reads every file in a directory, as an `Include config.d/*` does, never
  loads one.
- Name files after the repository to replace only those: `homeskillet link ~/dotfiles .bashrc --force`. `--dry-run
  --force` shows what would move.

`backups purge` is the one way homeskillet deletes a file it did not create, and only when you ask:

- It deletes only a file or symlink in the data directory's `backups/` named as `--force` names a backup, its path, a
  dot, and the Unix time; anything else there stays. A symlink is deleted itself, never what it leads to.
- It lists every backup it would delete, then asks, with no as the default, and only `y` or `yes` deletes; `--yes`
  stands in for that answer and `--dry-run` only lists. Each backup is looked at again just before it goes, and only the
  folders the purge emptied are removed with it.
- The data directory must pass the ownership rules first, as it decides what counts as a backup.

## Ownership

Linked files such as `.bashrc` run as you, so `status` and `link` refuse a repository that another user could change,
following OpenSSH's rules for `~/.ssh`:

- The repository, each manifest it has (`bootstrap.json`, `dotfiles.json`), and every directory and file leading to a
  source must be owned by you and not writable by group or others. Files found under `home/` are checked exactly like
  listed ones.
- Symlinks are followed one step at a time. Every symlink on the way to a source or a manifest must lead inside the
  repository, not only the last, and every directory the path passes through must be private. A link that bounces
  through `/tmp` back into the repository is refused, because whoever controls the hop in `/tmp` could retarget it
  later.
- Each directory above the repository must be owned by you or root, and be writable only by its owner unless it is
  sticky, like `/tmp`. The sticky bit excuses only directories: on a file it does not stop others writing to it.
- The home directory, and every existing directory a link goes into, such as `~/.config`, must be owned by you and not
  writable by group or others, with the same rule for the directories above home. Anyone who could write there could
  replace a link with their own file, which would then run as you.
- Running as root is refused unless `--allow-root` is passed, and root must then own the repository. This stops `sudo
  homeskillet link` from pointing root's shell files at files your account can edit.

Parent directories that `link` creates are private (`0700`); existing ones are left unchanged.

Windows has no mode bits, so there the same rules read each file's owner and access control list, an elevated process
counts as root, and junctions are refused wherever ownership is checked
([windows.md](windows.md#ownership-and-permissions)).

Nothing is linked across the line between Windows and WSL. The Windows build refuses a WSL distribution's files, under
`\\wsl.localhost` or `\\wsl$`, where Windows reports no owner at all, and inside WSL the Linux build refuses Windows
drives under `/mnt`, where every file looks writable by everyone. Each system's homeskillet links its own home from
files on its own file system ([windows.md](windows.md#windows-and-wsl-together)).

## Copies in WSL

`homeskillet wsl` gives WSL a Windows clone by copying it, so what WSL links must be exactly what Windows checked:

- The clone passes every check the other commands make, the ownership rules for each file included, before anything is
  sent, and each file is checked again as it is read. Only files and folders are sent; a symlink, which the copy could
  not check the way it leads, is refused.
- The Linux build writes the copy through an `os.Root` into a new private directory, creating every file anew, so no
  name in it can reach outside it, through `..` or a link. Names with control characters, a stream too large or with too
  many files, and a stream that ends early are refused, and nothing changes.
- The new copy replaces the old only once all of it has arrived, by renames, so links into it never lead into a
  half-written copy. `status` and dry runs never change it.
- `wsl install` installs only a Linux build of its own version: a release's, checked against that release's `SHA256SUMS`
  inside the distribution, or one given with `--from` that passes the rules for a program homeskillet installs. Either
  is checked to have arrived whole before it runs.
- Commands run in the distribution with `wsl.exe --exec`, never through a shell there, and `wsl.exe` comes from the
  system directory, never `PATH`. Only a command that changes nothing is ever retried.
- `wsl` from a WSL shell runs only the Windows build `wsl install` recorded, never a `homeskillet.exe` found on `PATH`,
  and reads that record only from a data directory that passes the ownership rules, since it names a program to run. The
  program itself is on a Windows drive, where WSL shows no owner or permissions to check, so Windows's own access rules
  guard it, as when it runs from Windows.

## Unlinking and Orphaned Links

`unlink` removes only listed symlinks pointing to the selected configuration repository, including dangling links.
Anything else at a listed target is reported as `skipped` and left alone, and the other entries are still unlinked.
`unlink` skips the ownership checks of the repository and home, and looks at no source, so it can clean up after a
source that was deleted or replaced, or a repository whose permissions went wrong. It still requires a private data
directory, checked before anything changes: the record there decides which links are homeskillet's, and `unlink` must
save it afterward. `unlink` does not remove sources or parent directories, or restore backups. Unlink before moving or
deleting a configuration clone.

Removing or renaming a file under `home/`, or a manifest entry, needs no unlinking first. After each `link` and
`unlink`, homeskillet records the links it made in `links.json` in its data directory. When the repository stops
declaring a file, `status` reports its old link as `orphaned`, without failing, and the next `link` or `unlink` removes
it. A link counts as orphaned only while it still points exactly where homeskillet pointed it: anything you have put in
its place is left alone. Links made before homeskillet kept a record are recorded at the next `link`.

## Setup Steps

A setup step is code from the repository, which may be someone else's, so `setup` relies on your review:

- Every file and directory in the repository must pass the ownership rules above, not just the manifests, because a step
  can run or source any of them. So must the home directory, as for `link`: steps run with `HOME` set to it, and a step
  is skipped when its `creates` path exists there, so someone who could write to it could plant that file or redirect
  what a step installs.
- Every symlink in the repository must lead, at every hop, to something inside it. A step that runs a symlinked script
  runs whatever the link points at, so a link leading outside, even on its way back in, or a dangling one that someone
  could complete later, is refused.
- Both checks run again before each step, as does whether the step is still needed, so a step that loosens the
  repository stops the steps after it, and one whose work an earlier step did is skipped.
- Each pending step is shown exactly as it will run, with its arguments quoted and its working directory, before you are
  asked; a step with several commands shows them all and is approved once. Only `y` or `yes` runs it. Anything else,
  including end of input, declines, so a closed or empty redirected input never approves by accident; `echo y |` is an
  approval. A step that is done is listed with the file that shows it, never silently skipped.
- Commands run without a shell unless the step names one, so you see any pipeline or substitution in the review.
- Setup names and arguments cannot contain control or formatting characters, which could hide part of a command from the
  review.
- `HOME` is set to the selected home, and on Windows `USERPROFILE` too, so `--home` pointed at a scratch directory keeps
  installers that honor them out of your real one. Everything else in your environment is passed to the step as it is,
  proxy settings and any tokens included, so approving a step also hands it those values.
- Under `--yes`, each step is shown but not asked about, and runs with no input and no terminal, in a session of its
  own. An unattended run never hangs on a step that prompts, even one that reads the terminal directly as `sudo` does,
  and `sudo` in a step cannot use a password you typed in the last few minutes, which it remembers per terminal. The
  kernel enforces this however a step reaches `sudo`: through `sh -c`, a script, or `eval` of hex or base64 text, which
  would defeat any check of the command's words. It cannot stop `sudo` set up to need no password (`NOPASSWD`) or to
  share one cached password across terminals (`timestamp_type=global`). Ctrl-C stops the step, kills whatever it started
  that is still running a few seconds later, and stops setup even if the step then exits successfully. Steps you approve
  one at a time keep the terminal. On Windows, a step under `--yes` keeps the console but gets no input, and Windows's
  `sudo` asks through a dialog every time; Linux `sudo` reached through `wsl.exe` can still ask on that console, so such
  a step must check for a terminal itself ([windows.md](windows.md#setup-steps)).
- A Homebrew directory counts for `provides` only when you or root own it, so another account, such as a user named
  `linuxbrew`, cannot make a step look done.
- A step with `inputs` is done while `setup.json` in the data directory records that it succeeded with them as they are.
  That record can only keep a step from being offered, never run one, so its fingerprint needs no cryptographic hash; it
  is read only from a data directory that passes the ownership rules, and validated on every read all the same. Inputs
  are read through the same rules as linked files, inside the repository only.

Review a step before approving it. homeskillet cannot tell what an installer will do once it runs.

`homeskillet prepare` offers its own steps, such as installing Git with winget, through the same review and prompt:
shown exactly, then run only on `y` or `yes`, or under `--yes`. Their commands are part of homeskillet, so there is no
repository to check, and they run in the home, which must still pass the ownership rules, as must every program they
find on `PATH`. Developer Mode is never switched on for you: it takes an administrator, so `prepare` only opens the
Settings page, with `explorer.exe` from the Windows directory, and checks again once you say it is on. Registering or
repairing winget runs Windows PowerShell from the Windows directory, never one found on `PATH`; the repair installs
Microsoft's `Microsoft.WinGet.Client` module from the PowerShell Gallery, for your account only, which is code from the
network like any installer, so it is offered only when registering did not bring winget, and asked about even under
`--yes` ([windows.md](windows.md#preparing-windows)).

## Starter Manifests, Checks, and Completion

- `init` writes `bootstrap.json` only into a repository with no manifest under either name, not even as a symlink, never
  over one that appears while it runs, and refuses a repository directory another user could write to, since the
  manifest decides what runs. An existing `home/` must be a directory, or a symlink to one inside the repository, as
  linking requires. It creates a missing `home/` private, and removes it again if writing the manifest fails. It shows
  what it would create and creates nothing until you approve, with no as the default, or pass `--yes`, and it refuses
  the home directory itself as a repository.
- `check` only reads: it applies the location, ownership, and manifest rules of the other commands, never runs a step,
  and never looks at a home. Like every other command, it refuses to run as root without `--allow-root`.
- Shell completion only reads, as `status` and `self list` do: it never runs a setup step or writes a file. Running as
  root, it reads nothing without `--allow-root`, and it never offers a candidate with control or formatting characters,
  which could rewrite the terminal ([install.md](install.md#shell-completion)).

## Installing homeskillet

The installed `homeskillet` command runs as you too, so `self install` applies the same ownership rules to what it
copies and where it copies it:

- The binary being installed must be a regular file owned by you or root and writable by nobody else. Under `sudo
  --allow-root`, that refuses a binary your own account could have changed.
- The installation directories, and every directory above them, follow the repository rules. New directories are private
  (`0700`), and installed binaries are executable only by you.
- `self remove` deletes only the binary, its `build.json` record, and temporary files that install creates, never
  anything else found in a version directory.
- Each file is written under a temporary name and renamed into place only once it is complete and flushed to disk, so a
  crash or a full disk never leaves a partial binary.
- On Windows, when the installation's `bin` directory is not on your Path, `install` adds it, the only change
  homeskillet makes to the registry: to the one value, `Path` in `HKEY_CURRENT_USER\Environment`; only appended to, so
  every entry already there keeps its place and text, and a command of the same name earlier on the Path still runs
  first; keeping the value's type, so variables in it stay variables; and only with a plain directory on a drive,
  holding no `;`, `"`, `%`, or control characters, that passes the ownership rules above, since anyone who could add a
  program to a directory on the Path could have it run as you. It is added only when the home is your own profile,
  never a scratch one given with `--home`, and `--dry-run` changes nothing.

## Updating

`homeskillet update`, and `homeskillet install VERSION`, which is the same, run a program just downloaded, so
everything on the way to running it is checked:

- The version must be a release's: three numbers, with or without a leading `v`, and no pre-release or build part.
  Anything else is refused before curl is even looked for, so no other text reaches a URL.
- It downloads only from this project's releases on GitHub, at URLs fixed in the binary, over HTTPS with TLS 1.2 or
  later, and never follows a redirect to plain HTTP. There is no option to download from elsewhere.
- It uses the system's own `curl` and hashing tool (`sha256sum` or `shasum`; `certutil` on Windows) at their fixed
  paths, never ones found on `PATH`, and each must pass the rules for a program homeskillet copies: a regular file,
  owned by you or root, writable by nobody else.
- The binary goes into a private directory made under the data directory, whose ownership and ancestors pass the rules
  above, so nobody else can swap the file between the check and the run, and no `noexec` mount of `/tmp` gets in the
  way. It is bounded in size, must match the SHA-256 the release's `SHA256SUMS` lists for it, and must report the
  version asked for, before anything runs it.
- It then installs itself with `self install`, under the rules above, so the version that was running stays installed,
  and the closing note names it when what runs has changed. A version already installed is never downloaded again or
  rewritten.
- A dry run stops after the download and the checksum check. It runs nothing it downloaded, so what it changes never
  depends on the download honoring `--dry-run`.

`SHA256SUMS` comes from the same release as the binary, so the check proves the download arrived intact, not who
published it; the release workflow is what stands behind that ([release.md](release.md#tagging)).

The once-a-day check behind the "is available" note asks only where GitHub's latest-release page leads, over HTTPS,
and sends nothing about you or your machine; it runs only for a release build, at a terminal, when the data directory
exists, and exporting `HOMESKILLET_UPDATE_CHECK=off` turns it off ([install.md](install.md#updating)). Its record is
read only from a data directory that passes the ownership rules, so another account cannot plant a note.

## System Directories

homeskillet also refuses to work in system directories. The home directory, the repository, and the install prefix may
not be `/`, `/home`, or `/Users` themselves, or be inside `/etc`, `/usr` (including `/usr/local`), `/bin`, `/lib`,
`/proc`, `/sys`, `/var/lib`, or macOS's `/System` and `/Library`, among others. That turns mistakes such as `HOME=/` in
a container or `--prefix /usr/local` into errors before anything is read or written. Temporary directories, `/root`, and
homes anywhere else are fine. On Windows, drive roots, `C:\Users` itself, the Windows and program directories, and a WSL
distribution's files are refused the same way ([windows.md](windows.md#system-locations)).

## Limits

Operations are idempotent but not transactional. An I/O failure can leave a partial installation; resolve it and rerun.
Do not modify a manifest or directory structure concurrently with the CLI, and run one homeskillet at a time per data
directory: two runs, as for a scratch home and the real one, can each save the record of links without the other's
update. Cloning from git, templating, and secret management remain outside the manager's scope.
