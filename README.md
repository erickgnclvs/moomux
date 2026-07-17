
# moomux

```
 ________________________________
< cowsay goes ai agents and tmux >
 --------------------------------
        \   ^__^
         \  (oo)\_______
            (__)\       )\/\
                ||----w |
                ||     ||
```

A TUI for managing [Claude Code](https://claude.com/claude-code) agent sessions across git worktrees. Creates a worktree + branch, starts a tmux session, launches `claude`, and opens a terminal tab — all in one keypress. Single Go binary, no daemon.

## Session layout

Each session is a tmux window split into two panes, both in the worktree directory:

- **Left (~2/3 width)** — the agent (`claude`, `codex`, or `opencode`)
- **Right (~1/3 width)** — a plain shell, for `git`, tests, etc. alongside the agent

It's a regular tmux window, so regular tmux pane controls apply — mouse click/drag to switch panes or resize (mouse mode is on by default), or the usual prefix keys:

| Action | Keys |
|---|---|
| Switch pane | `Ctrl-b` then arrow key |
| Cycle panes | `Ctrl-b o` |
| Split pane vertically (side-by-side) | `Ctrl-b %` |
| Split pane horizontally (stacked) | `Ctrl-b "` |
| Zoom/unzoom pane | `Ctrl-b z` |
| Close pane | `Ctrl-b x` |

These are plain tmux, not a moomux feature — see `man tmux` for the full list.

## Recommended tmux config

moomux launches plain tmux sessions, so tmux settings come from your own config. Add this to `~/.tmux.conf`:

```tmux
# Essential for Claude to avoid output breaking and desktop notification issues
set -g allow-passthrough on
set -s extended-keys on
set -as terminal-features 'xterm*:extkeys'

# Enable native mouse scrolling and selection
set -g mouse on

# Increase scrollback history for Claude's massive code generations
set -g history-limit 50000

# Start windows and panes at 1 instead of 0 for easier navigation
set -g base-index 1
set -g pane-base-index 1
```

Reload it in any running tmux session without restarting: press `Ctrl-b` then `:`, type `source-file ~/.tmux.conf`, and hit enter. Or from a shell: `tmux source-file ~/.tmux.conf`.

## Demo

https://github.com/user-attachments/assets/6a3aec4e-6c30-4cdf-89fa-fdadf02c6f3a

## Install

```bash
# Homebrew (recommended)
brew tap erickgnclvs/moomux
brew install moomux

# Go
go install github.com/erickgnclvs/moomux@latest

# From source
git clone https://github.com/erickgnclvs/moomux && cd moomux && make install
```

Requires `tmux`, `git`, and `claude` on `$PATH`.

**Windows**: tmux has no native Windows build. Run moomux inside [WSL](https://learn.microsoft.com/windows/wsl/install) — the Linux binary above works as-is. In Windows Terminal, moomux opens a new tab and attaches automatically; in any other terminal it prints a `tmux attach -t <session>` hint instead.

## Build

```bash
git clone https://github.com/erickgnclvs/moomux && cd moomux

make build    # compile ./moomux
make test     # go test ./... -race -count=1
make test-e2e # go test -tags e2e ./e2e/... — real tmux sessions + git worktrees under a temp dir
make install  # build + copy to $PREFIX/bin (default ~/.local/bin)
make run      # build + run
make clean    # remove the built binary
```

Requires Go, plus `tmux` and `git` (checked by `make install`/`make run` via `check-deps`).

Or build and run directly with `go` instead of `make`:

```bash
go build -o moomux . && ./moomux
```

## Run

```bash
moomux
```

Keys: `?` help (full command list) · `n` new · `enter` open · `x` kill · `d` delete · `a` archive/restore · `A` toggle archived view · `t` tag · `shift+↑`/`shift+↓` reorder · `tab` switch project · `q` quit

Press `?` at any time on the list screen to open a command palette with every keybinding grouped by category, so you don't have to memorize the footer.
