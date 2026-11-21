#!/usr/bin/env bash
# Regression test for next_version.sh's bump rules. Run directly:
#   ./scripts/next_version_test.sh
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail=0

# new_repo <name> [tag]: a scratch repo with one commit, tagged (default
# v0.5.0; pass "" for no tag), and cd into it.
new_repo() {
  mkdir "$tmp/$1"
  cd "$tmp/$1"
  git init -q
  git config user.email test@test.com
  git config user.name test
  git commit -q --allow-empty -m "chore: initial"
  if [ "${2-v0.5.0}" != "" ]; then git tag "${2-v0.5.0}"; fi
}

# revert_commit <rev>: what `git revert` writes, minus the diff (the scratch
# commits are empty, and git revert refuses an empty one).
revert_commit() {
  local sha subject
  sha=$(git rev-parse "$1")
  subject=$(git log -1 --pretty=%s "$sha")
  git commit -q --allow-empty -m "Revert \"$subject\"" -m "This reverts commit $sha."
}

# check <label> <want>: compare next_version.sh's output in the current repo.
check() {
  local label="$1" want="$2" got
  got="$("$script_dir/next_version.sh")"
  if [ "$got" != "$want" ]; then
    echo "FAIL: $label -> got '$got', want '$want'"
    fail=1
  else
    echo "ok: $label -> ${got:-<nothing>}"
  fi
}

assert_next() {
  local msg="$1" want="$2"
  git commit -q --allow-empty -m "$msg"
  check "commit '$msg'" "$want"
  git tag "$want"
}

new_repo basic

# feat: is used on nearly every commit in this repo's history, so it must
# NOT trigger a minor bump on its own anymore.
assert_next "feat: add widget" "v0.5.1"
assert_next "fix: widget crash" "v0.5.2"

git commit -q --allow-empty -m "feat!: breaking widget rewrite"
check "feat! subject" "v1.0.0"

# --- R7 edge cases -------------------------------------------------------

new_repo footer
git commit -q --allow-empty -m "feat: x" -m "BREAKING CHANGE: the api changed"
check "BREAKING CHANGE footer" "v1.0.0"

new_repo footer-dash
git commit -q --allow-empty -m "feat: x" -m "BREAKING-CHANGE: the api changed"
check "BREAKING-CHANGE footer" "v1.0.0"

new_repo quoted-footer
git commit -q --allow-empty -m "docs: explain versioning" \
  -m "The rule reads:
BREAKING CHANGE: anything here means major." \
  -m "Refs: none"
check "BREAKING CHANGE quoted mid-body in a docs commit" "v0.5.1"

new_repo revert
git commit -q --allow-empty -m "feat!: risky rewrite"
revert_commit HEAD
check "feat! followed by its revert" "v0.5.1"

new_repo revert-keeps-others
git commit -q --allow-empty -m "feat!: risky rewrite"
broken=$(git rev-parse HEAD)
git commit -q --allow-empty -m "feat!: kept break"
revert_commit "$broken"
check "a revert does not hide a different feat!" "v1.0.0"

new_repo squash
git commit -q --allow-empty -m "feat: big change (#1)" -m "* feat!: drop the old api
* fix: tidy"
check "squash body lists a feat! bullet" "v1.0.0"

new_repo squash-plain
git commit -q --allow-empty -m "feat: big change (#1)" -m "* feat: add a thing
* fix: tidy"
check "squash body without a breaking bullet" "v0.5.1"

new_repo stray-tag
git commit -q --allow-empty -m "fix: y"
git tag nightly
git tag v0.6.0-rc1
check "non-release tags on HEAD do not block" "v0.5.1"

new_repo release-tag-on-head
git commit -q --allow-empty -m "fix: y"
git tag v0.5.1
check "a vX.Y.Z tag on HEAD blocks" ""

# --- R6: no release tag yet ----------------------------------------------

new_repo no-tags ""
git commit -q --allow-empty -m "fix: first"
check "no vX.Y.Z tag at all starts from 0.0.0" "v0.0.1"

exit "$fail"
