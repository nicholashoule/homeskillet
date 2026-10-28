# Using homeskillet

How to run homeskillet day to day. [manifest.md](manifest.md) describes what a configuration repository holds, and
[safety.md](safety.md) what homeskillet refuses to do and why. `homeskillet help` (or `-h`) lists every command and
option.

## Commands

| Command | What it does |
|---|---|
| `status` | Show whether each file is linked. The default, so `homeskillet ~/dotfiles` is a status. |
| `link` | Link the repository's files into the home directory. |
| `unlink` | Remove the repository's links from the home directory. |
| `setup` | Show each setup step and run the ones you approve ([manifest.md](manifest.md#setup-steps)). |
| `init` | Write a starter `bootstrap.json` where there is no manifest ([manifest.md](manifest.md#creating-one)). |
| `check` | List every issue in a repository, for every platform, or say it is valid ([manifest.md](manifest.md#checking-one)). |
| `wsl` | On Windows, or from a WSL shell, act in a WSL distribution ([windows.md](windows.md#the-wsl-command)). |
| `backups` | List the files `link --force` moved aside; `backups purge` deletes them once you approve ([Conflicts and Backups](#conflicts-and-backups)). |
| `update` | Install the newest release from GitHub, or the version given, checked against its `SHA256SUMS`, keeping the old one ([install.md](install.md#updating)). |
| `install`, `use` | Install this build as a version of its own; run `latest`, `previous`, or a version ([install.md](install.md#versions-and-updates)). |
| `self` | The same as `update`, `install`, and `use`, plus `self list`, `self releases`, and `self remove` ([install.md](install.md#versions-and-updates)). |
| `version`, `completion`, `help` | Show the version, print a shell completion script ([install.md](install.md#shell-completion)), or show the help. |

```sh
homeskillet ~/dotfiles                        # status: what link would change
homeskillet link ~/dotfiles --dry-run         # preview; nothing changes
homeskillet link ~/dotfiles                   # link every file
homeskillet link ~/dotfiles .bashrc           # link only ~/.bashrc
homeskillet link ~/dotfiles .bashrc --force   # replace ~/.bashrc, keeping a backup
homeskillet unlink ~/dotfiles
homeskillet setup ~/dotfiles                  # offer each setup step
homeskillet setup ~/dotfiles uv               # only the step named uv
homeskillet check ~/dotfiles
```

## The Repository

The repository is the first argument after the command, or `--repo`. Left out, it is the current directory or, when that
is not a repository, the one repository already linked into the home, so after the first `link`, `homeskillet link`
alone works anywhere; with several linked, homeskillet lists them rather than choose. In place of a command, and after
`setup`, where other words name steps, the repository must plainly be a path: `.`, `..`, or containing `/`, or on
Windows `\` or a drive. A mistyped command is then never read as a directory: write `./dotfiles`, not `dotfiles`.

A path that starts with `~`, as `~/dotfiles`, means your home from any shell, `cmd` included. Keep the clone in a
permanent place, since links point into it, and unlink before moving or deleting it.

## Files and Steps

Files after the repository limit `status`, `link`, and `unlink` to them: a target (`.bashrc`), a source
(`home/.bashrc`), or a directory of either (`.bashrc.d`). Names after `setup` choose steps
([manifest.md](manifest.md#running-steps)). Put `--` before a name that starts with `-`, as in `homeskillet unlink
~/dotfiles -- -rf`.

## Options

Options go anywhere after the command, and one that does not apply to it is an error rather than ignored. An empty
value, as from an unset variable in `--home "$SCRATCH"`, is refused rather than read as the default.

| Option | Effect |
|---|---|
| `--dry-run` | Change nothing and run no setup step; say what would happen. |
| `--home DIR` | Act on another home, such as a scratch one to try a repository in. |
| `--platform OS[/ARCH]` | Act for another platform, such as `darwin` or `linux/arm64` ([manifest.md](manifest.md#platforms)). |
| `--quiet` | Print nothing but errors; the exit status reports the result. Not for `setup`, whose steps must be shown. |
| `--force` | For `link`: move conflicting files into a backup, then link (see below). |
| `--yes` | For `setup`: run every pending step without asking, for unattended builds such as a Dockerfile ([safety.md](safety.md#setup-steps)). For `backups purge`: delete every backup listed without asking. |
| `--older-than AGE` | For `backups`: only backups moved more than `AGE` ago, such as `30d`. |
| `--data-dir DIR` | Keep the records of links and setup steps, backups, and installed versions elsewhere ([install.md](install.md#data-directory)). |
| `--prefix DIR` | For `self`: install under another prefix. |
| `--allow-root` | Run as root, or elevated on Windows, for root's own dotfiles ([safety.md](safety.md#ownership)). |
| `--distribution NAME`, `--from FILE` | For `wsl` ([windows.md](windows.md#the-wsl-command)). |

## Output

At a terminal, homeskillet says what it is running and where, groups files by what happens to them, with problems first,
and ends with what changed or what to do; after `link` and `status`, it also names any setup steps still pending. In a
pipe or a script it prints one tab-separated line per file or step instead, a stable format to parse, such as
`linked<TAB>/home/you/.bashrc -> /home/you/dotfiles/home/.bashrc`.

`status` and `link` exit nonzero when anything blocks linking: a file that is not homeskillet's link where a link should
go, or a listed file the repository lacks. A linked file a shell would run in place of a command, such as
`~/.local/bin/sudo`, is pointed out ([safety.md](safety.md#links-and-conflicts)).

## Conflicts and Backups

`link` never replaces a file it did not create. With `--force`, it moves each conflicting file, never a directory, into
the data directory's `backups/`, at its path with the Unix time appended (`backups/.bashrc.1761393600`), then links;
move a backup back to restore it. `--dry-run --force` shows what would move. [safety.md](safety.md#links-and-conflicts)
has every rule.

Backups stay until you purge them:

```sh
homeskillet backups                           # list each backup, when it was moved, and its size
homeskillet backups purge                     # show them, then delete them if you answer y
homeskillet backups purge --older-than 30d    # only those moved more than 30 days ago
homeskillet backups purge --dry-run           # show what would be deleted
```

A purge shows every backup it would delete and deletes nothing until you answer `y` or `yes`; anything else, including
end of input, declines. `--yes` skips the question, for scripts. `--older-than` takes days (`30d`), weeks (`2w`), or
hours (`12h`). In a script, `backups` prints one tab-separated line per backup: `backup`, its path, the path it came
from in the home, the Unix time it was moved, and its size in bytes.
