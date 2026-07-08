#!/usr/bin/env bash
# Renders a moomux TUI screen to a PNG screenshot.
#
# Usage: scripts/screenshot.sh <screen> <output.png> [width] [height]
#
# <screen> is one of the scenarios uishot knows about: run
# `go run ./cmd/uishot -screen=bogus` to print the current list.
#
# How it works: builds cmd/uishot, runs it inside a pty (via `script`) so
# lipgloss emits color even though stdout isn't a real terminal, strips the
# terminal-query escape sequences bubbletea/termenv emit on startup,
# converts the ANSI capture to HTML with ansi2html, then rasterizes that
# HTML with a headless Chromium via Playwright (scripts/render_html.js).
#
# Requires: go, script (bsdutils/util-linux), python3 with the ansi2html
# package (`pip install ansi2html`), and node with playwright installed
# (`npm install -g playwright` plus a Chromium build — set
# PLAYWRIGHT_CHROMIUM_PATH if it's not on Playwright's default search path).
set -euo pipefail

screen="${1:?usage: screenshot.sh <screen> <output.png> [width] [height]}"
out="${2:?usage: screenshot.sh <screen> <output.png> [width] [height]}"
cols="${3:-100}"
rows="${4:-32}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

if ! command -v ansi2html >/dev/null 2>&1; then
  if python3 -c "import ansi2html" >/dev/null 2>&1; then
    ansi2html() { python3 -m ansi2html "$@"; }
  else
    echo "screenshot.sh: ansi2html not found — install it with 'pip install ansi2html'" >&2
    exit 1
  fi
fi

bin="$workdir/uishot"
go build -o "$bin" "$repo_root/cmd/uishot"

raw="$workdir/raw.ansi"
TERM=xterm-256color COLORTERM=truecolor script -qec "'$bin' -screen='$screen' -width='$cols' -height='$rows'" /dev/null >"$raw"

clean="$workdir/clean.ansi"
python3 - "$raw" "$clean" <<'PY'
import re, sys
data = open(sys.argv[1], "rb").read()
# strip the OSC-11 background-color query and CPR cursor-position query that
# termenv sends on startup to detect terminal capabilities — script(1)
# captures them as literal bytes since there's no real terminal to answer.
data = re.sub(rb"\x1b\]11;\?\x1b\\", b"", data)
data = re.sub(rb"\x1b\[6n", b"", data)
open(sys.argv[2], "wb").write(data)
PY

html="$workdir/term.html"
ansi2html -W -f 16 <"$clean" >"$html"

px_w=$(( cols * 10 + 40 ))
px_h=$(( rows * 21 + 40 ))

node_path="$(npm root -g 2>/dev/null || true)"
NODE_PATH="$node_path" node "$repo_root/scripts/render_html.js" "$html" "$out" "$px_w" "$px_h"

echo "wrote $out"
