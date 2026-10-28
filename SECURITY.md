# Security Policy

homeskillet links files such as `.bashrc` that run as you, so a weakness in it can mean someone else's code running
under your account. Reports are welcome, and so are questions about whether something is a problem.

## Reporting a Vulnerability

Report it privately through GitHub: open the repository's [Security
tab](https://github.com/nicholashoule/homeskillet/security) and choose "Report a vulnerability". Please keep it out of
public issues and pull requests until a fixed release is out.

Include what you found, a repository or home directory layout that shows it, the output of `homeskillet version`, and
the operating system. You should hear back within a week. A confirmed problem is fixed in a new release, listed under
Security in [CHANGELOG.md](CHANGELOG.md), and credited to you if you want.

## Supported Versions

Only the latest release receives fixes. `homeskillet update` installs the newest release and keeps the old version for
rollback ([docs/install.md](docs/install.md#updating)).

## What Counts

homeskillet promises the rules in [docs/safety.md](docs/safety.md) and the contract in
[docs/architecture.md](docs/architecture.md#safety-contract). A way around any of them is a vulnerability, for example:

- Changing, deleting, or creating a file outside the selected home directory, or a file homeskillet did not create,
  including through a symlink.
- Linking from, or running a setup step out of, a repository or directory that another user can change, despite the
  ownership checks.
- Running a setup step without approval, or running something other than what was shown; a step reaching a terminal or
  input under `--yes`.
- Output that lets a file name or path from a repository control the terminal.
- `self install` replacing a `homeskillet` command it did not create, or leaving the command pointing at an incomplete
  binary.
- `update` running a download that does not match the release's `SHA256SUMS`, or fetching from anywhere but this
  project's releases over HTTPS.

Out of scope: what a setup step does once you approve it, since steps are code from a repository you chose to use, and
anything that already requires root or write access to your home directory, which the checks assume is yours alone.

## Release Binaries

The release workflow builds each release's binaries from its tag and publishes them with a `SHA256SUMS` file, and, from
the public repository, a signed provenance attestation for each binary. Check a download against `SHA256SUMS` before
installing, and with `gh attestation verify` that the release workflow built it
([docs/install.md](docs/install.md#downloading-a-release)); `homeskillet update` checks the first but not yet the
second. Builds are reproducible, so you can also build the tag yourself and compare ([docs/release.md](docs/release.md)).
