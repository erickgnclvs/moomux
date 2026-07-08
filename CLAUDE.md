# moomux

A TUI (Bubble Tea) for managing Claude Code / codex / opencode sessions across git worktrees. See README.md for what it does and how to build/run it.

## UI changes

This is a terminal UI — you can't see it render just by reading the Go source. After any change to `internal/tui/` (new fields, layout tweaks, new modes, copy changes, etc.), capture a screenshot of the affected screen(s) and look at it before considering the change done:

```bash
./scripts/screenshot.sh <screen> /tmp/<screen>.png
```

`<screen>` is one of the scenarios `cmd/uishot` knows about (`list`, `new-session`, `new-project`, `tag`, `confirm-delete`, `confirm-delete-project` — run `go run ./cmd/uishot -screen=x` to see the current list if unsure). It renders the real `tui.Model` against a fake backend with canned sample data, so no real projects, git repos, or tmux sessions are needed.

If a change adds a new mode or scenario that isn't covered, add it to the `screens` map in `cmd/uishot/main.go` (drive it there with the same key-press sequence a user would use) rather than skipping the screenshot.

Send the resulting PNG to the user so they can see the change, the same way you'd report a code diff.
