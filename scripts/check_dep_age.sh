#!/usr/bin/env bash
# Fails if any module in the build graph, or any `go run tool@version` pin in
# .github/workflows or the Makefile, was published less than MIN_AGE_HOURS (default 72) ago.
# A fresh release is the window in which a hijacked or broken version is most
# likely to still be live, so new versions wait out a cooldown before we adopt
# them. Dependabot applies the same cooldown (.github/dependabot.yml); this
# catches a hand-run `go get` or a hand-edited tool pin too.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

min_hours=${MIN_AGE_HOURS:-72}
cutoff=$(( $(date -u +%s) - min_hours * 3600 ))

# Allow-list for a security fix that cannot wait out the cooldown: one
# `module@version` per line, `#` starts a comment (give the reason and the
# advisory id). Lives in the repo so the exception is reviewed in a PR, and
# the entry is deleted once the version is old enough.
allow_file=${DEP_AGE_ALLOW:-.dep-age-allow}
allowed=''
if [ -f "$allow_file" ]; then
  allowed=$(sed -E 's/#.*//; s/[[:space:]]+//g; /^$/d' "$allow_file")
fi

# Tool pins name a package (golang.org/x/vuln/cmd/govulncheck@v1.8.0); the
# proxy wants its module, which for every tool we pin is the path before /cmd/.
tools=$(sed -nE 's#.*go run ([^ @]+)/cmd/[^ @]+@(v[^ ]+).*#\1@\2#p' .github/workflows/*.yml Makefile | sort -u)

too_new=$(
  {
    go list -m -json all
    for t in $tools; do go list -m -json "$t"; done
  } | jq -r --argjson cutoff "$cutoff" '
    select(.Main | not)
    | select((.Time | sub("\\.[0-9]+Z$"; "Z") | fromdate) > $cutoff)
    | "\(.Path)@\(.Version)  published \(.Time)"'
)

if [ -n "$allowed" ] && [ -n "$too_new" ]; then
  too_new=$(printf '%s\n' "$too_new" | ALLOWED="$allowed" awk 'BEGIN { n = split(ENVIRON["ALLOWED"], a, "\n"); for (i = 1; i <= n; i++) ok[a[i]] = 1 } !($1 in ok)')
fi

if [ -n "$too_new" ]; then
  echo "Published less than ${min_hours}h ago; wait out the cooldown before adopting:" >&2
  echo "$too_new" >&2
  exit 1
fi
echo "ok: every module and tool pin is at least ${min_hours}h old"
