# Windows Example

A starter dotfiles repository for Windows 10 and 11: a PowerShell profile, a
Git ignore file for every repository, and setup steps that install
PowerShell 7 with winget and load the profile from Documents.

```text
windows/
├── bootstrap.json          for Windows, with the powershell and profile setup steps
├── setup/profile.ps1       writes the line in Documents that loads the profile
└── home/                   linked into %USERPROFILE%
    ├── .config/powershell/profile.ps1
    └── .config/git/ignore
```

## Get Going

Turn on Developer Mode (`start ms-settings:developers`), which Windows needs
for links, and
[install homeskillet](https://github.com/nicholashoule/homeskillet/blob/main/docs/windows.md#installing).
Then, in a PowerShell window that is not elevated, from a clone of
homeskillet:

```powershell
Copy-Item -Recurse examples\windows $HOME\dotfiles   # keep it there: links point into it
homeskillet check $HOME\dotfiles                     # valid?
homeskillet link $HOME\dotfiles --dry-run            # what would change
homeskillet link $HOME\dotfiles
homeskillet setup $HOME\dotfiles                     # shows each step; runs it if you answer y
```

Open a new terminal and run `pwsh`: the profile loads. If you already have a
profile, the `profile` step leaves it alone and prints the line to add to it.
After the first `link`, `homeskillet link` and `homeskillet setup` find the
repository from anywhere.

## Make It Yours

- Add a file under `home/` where it goes in `%USERPROFILE%`, then `link`
  again: `home/AppData/Roaming/x/config.json` becomes
  `%USERPROFILE%\AppData\Roaming\x\config.json`.
- Install another tool with a step like `powershell`: its winget `--id`, and
  the command it `provides`, so the step is done once it is installed.
- Configure WSL from here too, with `home/wsl/`
  ([Windows and WSL together](https://github.com/nicholashoule/homeskillet/blob/main/docs/windows.md#windows-and-wsl-together)).
- Keep the repository in Git: `git init $HOME\dotfiles`.

More: [Windows](https://github.com/nicholashoule/homeskillet/blob/main/docs/windows.md),
[the manifest](https://github.com/nicholashoule/homeskillet/blob/main/docs/manifest.md),
and [every command](https://github.com/nicholashoule/homeskillet/blob/main/docs/usage.md).
