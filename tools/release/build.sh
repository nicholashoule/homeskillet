#!/usr/bin/env bash
# Builds homeskillet's release for TAG, as the release workflow publishes it
# and CI rehearses it on every pull request, so a change that would break a
# release fails in CI rather than when a tag is pushed. It publishes nothing:
#
#   - the tag's CHANGELOG.md section, written to NOTES, which must be outside
#     the checkout, with links to files pointed at the tag's copies;
#   - the binaries from make cross, and bin/SHA256SUMS;
#   - a check that every binary reports TAG, from a clean tree.
#
# HEAD must carry TAG. Runs in GitHub Actions, which sets GITHUB_SERVER_URL and
# GITHUB_REPOSITORY. See docs/release.md.
#
# Usage: build.sh TAG NOTES
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: build.sh TAG NOTES" >&2
  exit 2
fi
tag=$1
notes=$2
version=${tag#v}

# The notes are the lines after the "## [X.Y.Z]" heading up to the next
# heading. A tag without one is refused, so a release cannot go out
# unexplained. Links to files in the repository are relative there, and the
# Release page is not in the tree.
awk -v heading="[$version]" '
  /^## / { wanted = ($2 == heading); next }
  wanted' CHANGELOG.md > "$notes"
if ! grep -q '[^[:space:]]' "$notes"; then
  echo "::error::CHANGELOG.md has no section for $version; add one before tagging (docs/release.md)"
  exit 1
fi
sed -i "s#](\(\(docs/\)\?[A-Za-z0-9_.-]*\.md[^)]*\))#](${GITHUB_SERVER_URL:?}/${GITHUB_REPOSITORY:?}/blob/$tag/\1)#g" "$notes"
cat "$notes"

# Go stamps the tag as the version only when the checkout has no changes, and
# counts any file Git does not ignore, so a stray file left by an earlier step
# would stamp every binary +dirty. Name it here rather than after the build.
changed=$(git status --porcelain --untracked-files=all)
if [[ -n $changed ]]; then
  echo "::error::the checkout has changes, so Go would stamp every binary +dirty; write anything a step makes outside it, such as in \$RUNNER_TEMP:"
  echo "$changed"
  exit 1
fi

# SOURCE_DATE_EPOCH stamps the commit's time as the build time, so building
# the tag again with the same Go release gives identical files.
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct) make cross
(cd bin && sha256sum homeskillet-*-* > SHA256SUMS && cat SHA256SUMS)

# Go also ignores a tag that is not valid for the module path, so check that
# every binary reports the tag exactly (docs/release.md, Versions).
module=$(go list -m)
for binary in bin/homeskillet-*-*; do
  go version -m "$binary" | awk -v tag="$tag" -v module="$module" -v binary="$binary" '
    $1 == "mod" && $2 == module { version = $3 }
    $1 == "build" && $2 == "vcs.modified=false" { clean = 1 }
    END {
      if (version != tag || !clean) {
        printf "::error::%s reports version %s from a %s tree, not %s from a clean one\n", binary, version, clean ? "clean" : "modified", tag
        exit 1
      }
    }'
done
