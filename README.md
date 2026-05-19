# curral

A general-purpose TUI for managing [Claude Code](https://claude.com/claude-code) agent sessions across git worktrees. Replaces the create / resume / delete workflow of tools like claude-squad, using tmux as the session backend and iTerm2 for tab management.

Single Go binary. No daemon, no network, no background process.

## What it does

```
┌─────────────────────────────────────────────────────────────────┐
│ curral                                    eg_system  other_repo  │
├──────────────────────────┬──────────────────────────────────────┤
│ SESSIONS                 │ DETAIL                               │
│                          │                                      │
│ ▶ hash-password  ⬤ work │  status:   ⬤ working               │
│   helping-tati   ⬤ wait │  branch:   erickgoncalves/hash-…    │
│   json-password  ○ park  │  worktree: ~/.local/share/curral/…  │
│   mfa-password   ○ park  │  created:  2 days ago               │
│                          │                                      │
├──────────────────────────┴──────────────────────────────────────┤
│ n:new  enter:open  d:delete  r:refresh  tab:project  q:quit     │
└─────────────────────────────────────────────────────────────────┘
```

For each session, curral:

1. Creates a git worktree on a new branch off your base branch
2. Starts a detached tmux session in that worktree
3. Launches `claude` inside it
4. Opens an iTerm2 tab attached to the tmux session

Status is detected by polling `~/.claude/sessions/*.json` every 2 seconds and matching the session's `cwd` to known worktree paths.

## Requirements

- macOS (the tab opener uses `osascript` against iTerm2)
- Go 1.22+
- `tmux`
- `git`
- `claude` CLI on `$PATH`
- iTerm2

## Install

```bash
git clone https://github.com/erickgnclvs/curral
cd curral
make install              # builds and installs to ~/.local/bin/curral
```

Make sure `~/.local/bin` is on your `$PATH`.

## Configure

First run creates `~/.config/curral/config.toml` with a commented example. Edit it:

```toml
[projects.eg_system]
repo          = "~/Development/eg_system"
branch_prefix = "erickgoncalves"   # optional — prepended to the branch name
base_branch   = "main"

[projects.other_repo]
repo        = "~/Development/other_repo"
base_branch = "main"
```

Each project key (`eg_system`, `other_repo`) becomes a tab in the TUI.

State lives in two places:

| Path                                      | What it holds                          |
|-------------------------------------------|----------------------------------------|
| `~/.config/curral/config.toml`            | Project registry (you edit this)       |
| `~/.config/curral/sessions.json`          | Active session metadata (curral edits) |
| `~/.local/share/curral/worktrees/<proj>/` | Worktree checkouts (one per session)   |

## Run

```bash
curral
```

## Keybindings

| Key             | Action                                  |
|-----------------|-----------------------------------------|
| `↑` / `k`       | Move selection up                       |
| `↓` / `j`       | Move selection down                     |
| `enter`         | Open session in a new iTerm2 tab        |
| `n`             | New session (inline form)               |
| `d`             | Delete session (confirmation)           |
| `r`             | Force refresh                           |
| `tab`           | Cycle through projects                  |
| `q` / `ctrl+c`  | Quit                                    |
| `esc`           | Cancel current overlay                  |

## Status states

| Indicator   | Label    | Meaning                                                 |
|-------------|----------|---------------------------------------------------------|
| `⬤` green   | working  | Claude is actively running in the worktree              |
| `⬤` amber   | waiting  | Claude is attached but idle, waiting on you             |
| `○` dim     | parked   | Worktree exists, no tmux session — resume via `enter`   |

## New session flow

1. Press `n` → inline form appears
2. Type a session name → press enter
3. curral:
   - `git fetch origin <base_branch>`
   - `git worktree add <path> -b <branch_prefix>/<name> origin/<base_branch>`
   - `tmux new-session -d -s curral-<name> -c <worktree>`
   - `tmux send-keys claude Enter`
   - opens an iTerm2 tab attached to the new tmux session

If `branch_prefix` is unset the branch is named `<name>` directly.

## Delete flow

1. Press `d` → confirmation overlay
2. Press `y` → curral:
   - kills the tmux session (if running)
   - removes the worktree (`git worktree remove --force`)
   - drops the entry from `sessions.json`
   - **keeps the branch** so you don't lose work

## Architecture

```
curral/
├── main.go                 # entrypoint
└── internal/
    ├── config/             # TOML config loader
    ├── session/            # JSON session store
    ├── tmux/               # tmux CLI wrapper
    ├── iterm/              # osascript-based iTerm2 tab opener
    ├── gitwt/              # git worktree wrapper
    ├── watcher/            # ~/.claude/sessions/*.json poller
    ├── app/                # backend glue (Backend interface impl)
    └── tui/                # Bubbletea model, views, keys
```

Every shell-calling package (`tmux`, `iterm`, `gitwt`) has an injectable `Runner` interface so tests don't shell out.

## Development

```bash
make test                  # go test ./... -race
make build                 # produces ./curral
make run                   # build + run
```

## Out of scope (v1)

- Linux / Windows terminal openers — iTerm2/macOS only
- PR/issue integration
- Session search / filter
- Branch ahead/behind status
- Multiple Claude instances per session

## License

MIT
