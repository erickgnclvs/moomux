#!/usr/bin/env bash
# Screenshot the running app — the GUI equivalent of the Go side's
# scripts/screenshot.sh, and the same rule applies: look at the PNG before
# calling a UI change done, and send it to the user as a file:// link.
#
# Usage: Scripts/shot.sh [out.png]
#
# The app must already be running (`make dev`). Screen Recording permission is
# required for screencapture to see another app's window; without it macOS
# silently captures the desktop instead. Against a locked screen this captures
# the lock screen, so a blank-looking shot is not evidence of a broken UI.
set -euo pipefail

cd "$(dirname "$0")/.."
out="${1:-/tmp/moomux-macos.png}"

pgrep -x Moomux >/dev/null || { echo "Moomux is not running — try 'make dev'"; exit 1; }

frame="$(swift Scripts/ui.swift frame)"
[ -n "$frame" ] || { echo "could not read the window frame"; exit 1; }

screencapture -x -R "$frame" "$out"
echo "$out"
