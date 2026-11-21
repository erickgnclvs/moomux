#!/usr/bin/env bash
# Computes the next release tag from conventional-commit messages since the
# last vX.Y.Z tag reachable from HEAD, and prints it (e.g. "v0.2.9") to
# stdout. Prints nothing if HEAD is already tagged vX.Y.Z, or if there are no
# commits since the last tag — either way, there's nothing new to release.
# With no vX.Y.Z tag at all, the baseline is 0.0.0.
#
# Bump rules, checked per commit since the last tag:
#   - "type!: ..." in the subject                 -> major
#   - a "* type!: ..." bullet in the body (a squash merge listing its commits)
#                                                 -> major
#   - a "BREAKING CHANGE:" / "BREAKING-CHANGE:" footer, in the last paragraph
#     of the message (not a line quoted mid-body) -> major
#   - anything else (feat:, fix:, chore:, ...)    -> patch
# A commit that a later `git revert` message names ("This reverts commit
# <sha>") is ignored, and so is the revert itself. Ceiling: a revert of a
# revert is not re-applied.
#
# feat: used to bump minor, but nearly every commit here is written as
# feat:, so that rule was firing on almost every release. Minor bumps are
# now a manual call (tag/push vX.Y.0 yourself) instead of inferred.
set -euo pipefail

release_tag='^v[0-9]+\.[0-9]+\.[0-9]+$'

# Only vX.Y.Z tags count: a stray tag like "nightly" or "v0.6.0-rc1" on HEAD
# must not block a release, nor serve as the baseline.
for t in $(git tag --points-at HEAD); do
  if [[ $t =~ $release_tag ]]; then
    exit 0
  fi
done

# Reachable from HEAD only, so a tag on an unrelated branch is not picked up.
# `|| true`: no match is a normal state (first release), not an error.
last_tag=$(git tag -l --merged HEAD 'v*' | { grep -E "$release_tag" || true; } | sort -V | tail -n1)

if [ -z "$last_tag" ]; then
  range="HEAD"
  version="0.0.0"
else
  range="${last_tag}..HEAD"
  version="${last_tag#v}"
fi

commits=$(git rev-list "$range")
if [ -z "$commits" ]; then
  exit 0
fi

# Shas that a revert commit names.
reverted=" "
for c in $commits; do
  for r in $(git log -1 --pretty=%B "$c" | sed -nE 's/^This reverts commit ([0-9a-f]{7,40}).*/\1/p'); do
    reverted+="$(git rev-parse --verify -q "$r^{commit}" || true) "
  done
done

subject_re='^[a-zA-Z]+(\([^)]+\))?!:'
is_breaking() {
  local msg subject last
  msg=$(git log -1 --pretty=%B "$1")
  subject=${msg%%$'\n'*}
  [[ $subject =~ $subject_re ]] && return 0
  printf '%s\n' "$msg" | grep -qE '^[[:space:]]*[-*][[:space:]]+[a-zA-Z]+(\([^)]+\))?!:' && return 0
  last=$(printf '%s\n' "$msg" | awk 'BEGIN{RS="";ORS=""} {p=$0} END{print p}')
  printf '%s\n' "$last" | grep -qE '^BREAKING[ -]CHANGE:' && return 0
  return 1
}

bump="patch"
for c in $commits; do
  [[ $reverted == *" $c "* ]] && continue
  subject=$(git log -1 --pretty=%s "$c")
  [[ $subject == 'Revert "'* ]] && continue
  if is_breaking "$c"; then
    bump="major"
    break
  fi
done

IFS='.' read -r major minor patch <<<"$version"
case "$bump" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac

echo "v${major}.${minor}.${patch}"
