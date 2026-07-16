#!/usr/bin/env bash
# Computes the next release tag from conventional-commit messages since the
# last vX.Y.Z tag reachable from HEAD, and prints it (e.g. "v0.2.9") to
# stdout. Prints nothing if HEAD is already tagged, or if there are no
# commits since the last tag — either way, there's nothing new to release.
#
# Bump rules, checked against every commit subject since the last tag:
#   - "type!: ..." or a "BREAKING CHANGE" footer -> major
#   - "feat: ..." (or "feat(scope): ...")         -> minor
#   - anything else (fix:, chore:, docs:, ...)    -> patch
set -euo pipefail

if [ -n "$(git tag --points-at HEAD)" ]; then
  exit 0
fi

last_tag=$(git tag -l 'v*' | sort -V | tail -n1)

if [ -z "$last_tag" ]; then
  commits=$(git log --pretty=%s)
  version="0.0.0"
else
  commits=$(git log "${last_tag}..HEAD" --pretty=%s)
  version="${last_tag#v}"
fi

if [ -z "$commits" ]; then
  exit 0
fi

bump=patch
if echo "$commits" | grep -qE '^[a-zA-Z]+(\([^)]+\))?!:|^BREAKING CHANGE:'; then
  bump=major
elif echo "$commits" | grep -qE '^feat(\([^)]+\))?:'; then
  bump=minor
fi

IFS='.' read -r major minor patch <<<"$version"
case "$bump" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac

echo "v${major}.${minor}.${patch}"
