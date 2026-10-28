# Linux Example

A starter dotfiles repository for Linux with bash and apt, as on Debian and Ubuntu: a `.bashrc`, a Git ignore file for
every repository, and a setup step that installs the packages in a list.

```text
linux/
├── bootstrap.json            for Linux, with the packages setup step
└── home/                     linked into your home directory
    ├── .bashrc               -> ~/.bashrc
    ├── .config/apt/packages  -> ~/.config/apt/packages, one per line
    └── .config/git/ignore    -> ~/.config/git/ignore
```

## Get Going

[Install homeskillet](https://github.com/nicholashoule/homeskillet/blob/main/docs/install.md), then from a clone of
homeskillet:

```sh
cp -R examples/linux ~/dotfiles          # keep it there: links point into it
homeskillet check ~/dotfiles             # valid?
homeskillet link ~/dotfiles --dry-run    # what would change
homeskillet link ~/dotfiles
homeskillet setup ~/dotfiles             # shows the step; runs it if you answer y
```

Your distribution already made a `~/.bashrc`, so `link` reports a conflict and changes nothing. Copy what you want from
yours into `home/.bashrc`, then `homeskillet link ~/dotfiles --force` moves yours into a backup and links. After the
first `link`, `homeskillet link` and `homeskillet setup` find `~/dotfiles` from anywhere.

## Make It Yours

- Add a file under `home/` where it goes in your home, then `link` again: `home/.config/tmux/tmux.conf` becomes
  `~/.config/tmux/tmux.conf`.
- Add a package to `home/.config/apt/packages`, and `setup` offers the step again; until then it is done. `apt-mark
  showmanual` lists the packages you installed.
- On Fedora, use `dnf` in `bootstrap.json` instead of `apt-get`, and drop `update`.
- Keep the repository in Git: `git init ~/dotfiles`.

More: [the manifest](https://github.com/nicholashoule/homeskillet/blob/main/docs/manifest.md) and [every
command](https://github.com/nicholashoule/homeskillet/blob/main/docs/usage.md).
