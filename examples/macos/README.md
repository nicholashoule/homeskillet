# macOS Example

A starter dotfiles repository for macOS with zsh and Homebrew: a `.zshrc`, a `.zprofile` that puts Homebrew on `PATH`, a
Git ignore file for every repository, and a setup step that installs what the Brewfile lists.

```text
macos/
├── bootstrap.json          for macOS, with the brew-bundle setup step
└── home/                   linked into your home directory
    ├── .Brewfile           -> ~/.Brewfile, Homebrew packages
    ├── .zprofile           -> ~/.zprofile
    ├── .zshrc              -> ~/.zshrc
    └── .config/git/ignore  -> ~/.config/git/ignore
```

## Get Going

Install [Homebrew](https://brew.sh) and
[homeskillet](https://github.com/nicholashoule/homeskillet/blob/main/docs/install.md), then from a clone of homeskillet:

```sh
cp -R examples/macos ~/dotfiles          # keep it there: links point into it
homeskillet check ~/dotfiles             # valid?
homeskillet link ~/dotfiles --dry-run    # what would change
homeskillet link ~/dotfiles
```

Open a new Terminal window, so the linked `.zprofile` puts `brew` on `PATH`, then:

```sh
homeskillet setup ~/dotfiles             # shows the step; runs it if you answer y
```

If you already have a `~/.zshrc` or `~/.zprofile`, `link` reports a conflict and changes nothing. Copy what you want
from yours into `home/`, then `homeskillet link ~/dotfiles --force` moves yours into a backup and links. After the first
`link`, `homeskillet link` and `homeskillet setup` find `~/dotfiles` from anywhere.

## Make It Yours

- Add a file under `home/` where it goes in your home, then `link` again: `home/.config/tmux/tmux.conf` becomes
  `~/.config/tmux/tmux.conf`.
- Add a package to `home/.Brewfile`, and `setup` offers the step again; until then it is done. Linked as `~/.Brewfile`,
  it also works with `brew bundle --global` from anywhere. `brew bundle dump --file=-` prints what this Mac already has.
- Keep the repository in Git: `git init ~/dotfiles`.

More: [the manifest](https://github.com/nicholashoule/homeskillet/blob/main/docs/manifest.md) and [every
command](https://github.com/nicholashoule/homeskillet/blob/main/docs/usage.md).
