# Windows

homeskillet does not run on Windows yet. A Windows build only prints that
Windows isn't supported at this time and suggests running homeskillet inside
WSL, where it is a Linux program and fully supported.

## What Works Already

Configuration repositories can target Windows today, and be checked from
Linux or macOS:

- A manifest's `platforms` may name `windows`, `windows/amd64`, or
  `windows/arm64`, at the top level or per entry and step.
- `--platform windows` acts for Windows from another system. Files link into
  a scratch home given with `--home`, which shows that every file is found and
  every target is valid; setup steps can only be listed, with `--dry-run`.
- Files and steps meant for Windows are held to Windows' naming rules: no
  device names such as `CON` or `nul.txt`, no names ending in a dot or space,
  none of `<>:"|?*\`, and no two targets that differ only in case.
- Discovery skips `Thumbs.db` and `desktop.ini`, which Explorer creates on its
  own, as it skips macOS's `.DS_Store`.
- `internal/host` detects Windows on Arm running an x64 build through
  emulation, through `IsWow64Process2`, as it detects Rosetta 2 on macOS.
- `make check` vets the Windows build, so code that breaks it fails on any
  machine.

## What Running on Windows Needs

Each item is a design decision as much as code. Until all of them are done, a
Windows build must keep refusing to run.

1. **Ownership checks.** `internal/trust` reads Unix owners and mode bits.
   Windows needs the equivalent from security descriptors: the owner must be
   the current user, and the access control list must not let anyone else
   (Everyone, Authenticated Users, Users) write. The standard library lacks
   `GetNamedSecurityInfo` and ACL parsing, so either declare the `advapi32`
   procedures with `syscall.NewLazyDLL`, or, with approval, depend on
   `golang.org/x/sys/windows`. The checks must fail closed.
2. **Symlinks.** Creating one needs Developer Mode or administrator rights;
   `os.Symlink` asks for an unprivileged link, which works only in Developer
   Mode. When it is refused, say how to turn Developer Mode on. The install
   layout's `latest`, `previous`, and `current` links point at directories,
   which junctions can do without any privilege.
3. **Known folders.** Documents, and with it every PowerShell profile, is often
   redirected into OneDrive, and AppData can be redirected too, so a target
   relative to `%USERPROFILE%` can be wrong. Targets need a prefix resolved
   through `SHGetKnownFolderPath`, such as `{documents}/PowerShell/...`.
4. **System locations.** `trust.CheckLocation` needs Windows' system
   directories: `C:\Windows`, `C:\Program Files` and `(x86)`, `C:\ProgramData`,
   every drive root, and `C:\Users` itself.
5. **Setup steps.** Command lookup must use `PATHEXT`, since Windows has no
   execute bit. Steps need `USERPROFILE` set to the selected home, and `HOME`
   too for Git and other Unix-born tools. The counterpart of `~/.local/bin` is
   `%LOCALAPPDATA%\Microsoft\WinGet\Links`, where winget puts the commands it
   installs.
6. **Self install.** Windows cannot delete a running `.exe` but can rename one,
   so switching versions renames rather than replaces. The command belongs in
   a per-user directory such as `%LOCALAPPDATA%\Programs\homeskillet`.
7. **Case.** Windows paths ignore case, so overlap, containment, and orphan
   comparisons must too.
8. **Elevation.** The counterpart of refusing root is refusing an elevated
   (administrator) process unless `--allow-root`, renamed or aliased, is given.

The `dotfiles-windows` configuration repository is the test bed: it already
declares `"platforms": ["windows"]` and is checked from Linux with
`--platform windows`.
