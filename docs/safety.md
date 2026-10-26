# Safety

What homeskillet refuses to do, and why. [architecture.md](architecture.md)
describes how the code enforces it.

## Links and Conflicts

`status` reports `missing`, `linked`, `conflict`, or `no-source` (the source
file is absent) for each entry, and `orphaned` for a link homeskillet made
earlier for a file the repository no longer has (see below). Existing files,
directories, and unrelated symlinks are never overwritten or deleted, even if
their contents match a source. Conflicts and sources absent from the
repository make `status` and `link` exit nonzero, and `link` then changes
nothing. Escaping paths, overlapping targets, source symlinks resolving outside
the repository, symlinked target parents, and paths or file names containing
control or formatting characters are rejected. Any other name, such as
`rm -rf` or `$(touch x)`, is only a name: homeskillet makes links with system
calls and never hands a name to a shell. Put `--` before a file name that
starts with `-`, as in `homeskillet unlink ~/dotfiles -- -rf`.

An empty option value or argument, as from an unset variable in
`--home "$SCRATCH"` or `unlink ~/dotfiles "$FILE"`, is refused. Read as the
default, it would mean your real home, or every file.

A linked executable that a shell finds before a command of the same name
elsewhere on `PATH`, such as `~/.local/bin/sudo` ahead of `/usr/bin/sudo`, is
linked, but `status` and `link` list it under "Shadowing commands" (scripts
get a line on standard error). `~/.local/bin` and `~/bin` count as first on
`PATH` even before your shell adds them, as many systems' startup files do. A
repository from someone else could use such a link to capture what you type,
a `sudo` password included. Aliases and functions in a linked startup file can
do the same unseen, and no check of file contents could find them reliably:
`eval $'\x73\x75\x64\x6f'` runs `sudo` without the word appearing. Ownership
rules keep anyone else from changing what you link, but what you link runs as
you, so link only a repository you have reviewed.

`link --force` is the one way past a conflict, and you must ask for it. It
moves each conflicting file or symlink into `backups/` in the data directory,
at its path in home with the Unix time appended, then links:

- It moves, never deletes, and never overwrites an earlier backup, so
  anything it replaced can be restored by moving it back.
- It never moves a directory. A directory where the repository has a file
  can hold anything, even the repository itself, so moving it is left to you.
- No target may contain the repository or the data directory, with or
  without `--force`, so a target such as `.config` for a clone in
  `~/.config/dotfiles` is refused.
- An absent source still blocks it, so nothing moves when linking would fail.
- If any move fails, the moves already made are undone. If linking fails after
  the moves, each file not yet replaced by its link is moved back, and the
  links already made are recorded.
- Backups stay out of home, so a program that reads every file in a
  directory, as an `Include config.d/*` does, never loads one.
- Name files after the repository to replace only those:
  `homeskillet link ~/dotfiles .bashrc --force`. `--dry-run --force` shows
  what would move.

## Ownership

Linked files such as `.bashrc` run as you, so `status` and `link` refuse a
repository that another user could change, following OpenSSH's rules for
`~/.ssh`:

- The repository, its manifest if it has one, and every directory and file
  leading to a source must be owned by you and not writable by group or
  others. Files found under `home/` are checked exactly like listed ones.
- Symlinks are followed one step at a time. Every symlink on the way to a
  source or the manifest must lead inside the repository, not only the last,
  and every directory the path passes through must be private. A link that
  bounces through `/tmp` back into the repository is refused, because
  whoever controls the hop in `/tmp` could retarget it later.
- Each directory above the repository must be owned by you or root, and be
  writable only by its owner unless it is sticky, like `/tmp`. The sticky bit
  excuses only directories: on a file it does not stop others writing to it.
- The home directory, and every existing directory a link goes into, such as
  `~/.config`, must be owned by you and not writable by group or others, with
  the same rule for the directories above home. Anyone who could write there
  could replace a link with their own file, which would then run as you.
- Running as root is refused unless `--allow-root` is passed, and root must
  then own the repository. This stops `sudo homeskillet link` from pointing
  root's shell files at files your account can edit.

Parent directories that `link` creates are private (`0700`); existing ones are
left unchanged.

## Unlinking and Orphaned Links

`unlink` removes only listed symlinks pointing to the selected configuration
repository, including dangling links. Anything else at a listed target is
reported as `skipped` and left alone, and the other entries are still unlinked.
`unlink` skips the ownership checks of the repository and home, so it can
clean up after a deleted source or a repository whose permissions went wrong.
It still requires a private data directory, checked before anything changes:
the record there decides which links are homeskillet's, and `unlink` must save
it afterward. `unlink` does not remove sources or parent directories, or
restore backups. Unlink before moving
or deleting a configuration clone.

Removing or renaming a file under `home/`, or a manifest entry, needs no
unlinking first. After each `link` and `unlink`, homeskillet records the links
it made in `links.json` in its data directory. When the repository stops
declaring a file, `status` reports its old link as `orphaned`, without failing,
and the next `link` or `unlink` removes it. A link counts as orphaned only while
it still points exactly where homeskillet pointed it: anything you have put in
its place is left alone. Links made before homeskillet kept a record are
recorded at the next `link`.

## Setup Steps

A setup step is code from the repository, which may be someone else's, so
`setup` relies on your review:

- Every file and directory in the repository must pass the ownership rules
  above, not just the manifest, because a step can run or source any of them.
- Every symlink in the repository must lead, at every hop, to something
  inside it. A step that runs a symlinked script runs whatever the link points
  at, so a link leading outside, even on its way back in, or a dangling one
  that someone could complete later, is refused.
- Both checks run again before each step, as does whether the step is still
  needed, so a step that loosens the repository stops the steps after it, and
  one whose work an earlier step did is skipped.
- Each pending step is shown exactly as it will run, with its arguments quoted
  and its working directory, before you are asked; a step with several
  commands shows them all and is approved once. Only `y` or `yes` runs it.
  Anything else, including closed or redirected input, declines. A step that
  is done is listed with the file that shows it, never silently skipped.
- Commands run without a shell unless the step names one, so you see any
  pipeline or substitution in the review.
- Setup names and arguments cannot contain control or formatting characters,
  which could hide part of a command from the review.
- `HOME` is set to the selected home, so `--home` pointed at a scratch
  directory keeps installers that honor `HOME` out of your real one.
- Under `--yes`, each step is shown but not asked about, and runs with no
  input and no terminal, in a session of its own. An unattended run never
  hangs on a step that prompts, even one that reads the terminal directly as
  `sudo` does, and `sudo` in a step cannot use a password you typed in the
  last few minutes, which it remembers per terminal. The kernel enforces this
  however a step reaches `sudo`: through `sh -c`, a script, or `eval` of hex
  or base64 text, which would defeat any check of the command's words. It
  cannot stop `sudo` set up to need no password (`NOPASSWD`) or to share one
  cached password across terminals (`timestamp_type=global`). Ctrl-C still
  stops the step and everything it started. Steps you approve one at a time
  keep the terminal.
- A Homebrew directory counts for `provides` only when you or root own it, so
  another account, such as a user named `linuxbrew`, cannot make a step look
  done.

Review a step before approving it. homeskillet cannot tell what an installer
will do once it runs.

## Installing homeskillet

The installed `homeskillet` command runs as you too, so `self install` applies
the same ownership rules to what it copies and where it copies it:

- The binary being installed must be a regular file owned by you or root and
  writable by nobody else. Under `sudo --allow-root`, that refuses a binary
  your own account could have changed.
- The installation directories, and every directory above them, follow the
  repository rules. New directories are private (`0700`), and installed
  binaries are executable only by you.
- `self remove` deletes only the binary, its `build.json` record, and
  temporary files that install creates, never anything else found in a
  version directory.
- Each file is written under a temporary name and renamed into place only
  once it is complete and flushed to disk, so a crash or a full disk never
  leaves a partial binary.

## System Directories

homeskillet also refuses to work in system directories. The home directory,
the repository, and the install prefix may not be `/`, `/home`, or `/Users`
themselves, or be inside `/etc`, `/usr` (including `/usr/local`), `/bin`,
`/lib`, `/proc`, `/sys`, `/var/lib`, or macOS's `/System` and `/Library`, among
others. That turns mistakes such as `HOME=/` in a container or
`--prefix /usr/local` into errors before anything is read or written. Temporary
directories, `/root`, and homes anywhere else are fine.

## Limits

Operations are idempotent but not transactional. An I/O failure can leave a
partial installation; resolve it and rerun. Do not modify the manifest or
directory structure concurrently with the CLI. Cloning from git, templating, and
secret management remain outside the manager's scope.
