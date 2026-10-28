# Releasing

How a version of homeskillet is published. The checks a change must pass first are in [development.md](development.md).

## Versions

Go 1.24 and newer stamp each build from a Git checkout with the version (the tag, or a pseudo-version after it), the
commit, and whether the tree was modified, which adds `+dirty`; the Makefile adds the build time (`BUILT`, from
`tools/buildtime`, which honors `SOURCE_DATE_EPOCH`). `homeskillet version` prints them, and `self list` reads them from
each installed binary. A release is therefore a tag on `main`, and the only text written by hand is its changelog entry.

Until the repository has a commit, builds report `(devel)`, which `self install` refuses. `make install VERSION=v0.0.1`
(or `make build VERSION=v0.0.1`) stamps a version with `-ldflags -X` for builds without Git metadata, such as containers
that exclude `.git`; it overrides the version only, not the commit details. In a checkout, tag instead: a stamped build
differs from the tagged one, and installed versions are never rewritten.

## The Changelog

[CHANGELOG.md](../CHANGELOG.md) is the release notes. It holds the latest release and the changes since, under
`Unreleased`; each earlier release is one link under `Earlier Releases`, pointing at the GitHub Release that keeps its
notes. While work lands, each change someone using homeskillet would notice gets a line under `Unreleased`, written as
what they can now do, not as what the code does; refactors, test fixes, and CI work stay out. Group lines under `Added`,
`Changed`, `Fixed`, `Removed`, or `Security`, and open a release's section with a sentence or two on what the release is
about.

To release, move the previous version's heading to a link line under `Earlier Releases` and drop its body, rename
`Unreleased` to `## [X.Y.Z]`, start a new empty `Unreleased` above it, update the two reference links at the bottom, and
merge that through a pull request. Headings and links carry no date: one written before the tag goes stale, and the tag
and its GitHub Release record when the version shipped. The release workflow copies the tag's section into the GitHub
Release as its notes, pointing links to files in the repository at the tag's copies, and refuses a tag that has no
section, so a release cannot go out unexplained.

## Tagging

Tag the merge commit on `main`, signed, and push the tag; nothing else is needed:

```sh
git checkout main && git pull --ff-only
git tag -s v0.1.1 -m 'homeskillet v0.1.1'
make install             # installs v0.1.1 and makes it latest; the old latest becomes previous
git push origin v0.1.1   # publishes it
```

Pushing the tag runs [.github/workflows/release.yml](../.github/workflows/release.yml), which publishes the binaries as
a GitHub Release, where people look for them:

- It refuses a tag whose commit is not on `main`, and one whose version has no section in `CHANGELOG.md`.
- It builds with `make cross`, setting `SOURCE_DATE_EPOCH` to the commit's time, so building the tag again with the same
  Go release gives the same files, and writes `SHA256SUMS`.
- It checks, with `go version -m`, that every binary reports the tag from a clean tree. Go ignores a tag that is not
  valid for the module path: a `v2` or later tag needs the path to end in `/v2` first.
- From a public repository, it attests each binary's provenance with `actions/attest-build-provenance`: a statement
  signed through GitHub that this workflow built the file from this commit, checked with `gh attestation verify`.
  GitHub offers attestations only to public repositories, so a private one skips the step.
- It creates the Release, titled `homeskillet vX.Y.Z`, with the changelog section as its notes and the binaries and
  `SHA256SUMS` attached, as a pre-release when the tag has a pre-release part, such as `v0.2.0-rc.1`.

The notes, the build, and the checks are one script, [tools/release/build.sh](../tools/release/build.sh), which CI also
runs on every pull request, on a tag it makes and never pushes, so a change that would break a release fails there,
before anything is tagged. The script refuses a checkout with any change or untracked file, since Go would then stamp
every binary `+dirty`; a step that makes a file writes it outside the checkout, as in `$RUNNER_TEMP`. It joins each
paragraph and list item of the notes into one line, because GitHub shows every newline in release notes as a line
break, so the changelog's wrapping never shows on the Release page.

A tag may be moved only while no Release exists for it, as after a run that failed before publishing; once the Release
is out, a problem gets a new patch version, since a moved tag is what a pinned download cannot tolerate. Do not create
the release in GitHub's web interface: the tag alone is the release. If one exists when the workflow runs,
as after a failed upload or a release made by hand, the workflow keeps it and sets its title, notes, and files to what a
fresh run would have produced. A run that is needed again, say after the Release was deleted, is
`gh run rerun <run-id>`, not a new tag.

[install.md](install.md#downloading-a-release) shows how to download a release.

## Trying Installs

To try installs without touching your own `~/.local`, point them elsewhere:

```sh
bin/homeskillet self install --prefix "$(mktemp -d)" --dry-run
bin/homeskillet self install --home /path/to/scratch/home
```
