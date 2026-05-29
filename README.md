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

A TUI for managing [Claude Code](https://claude.com/claude-code) agent sessions across git worktrees. Replaces the create / resume / delete workflow of tools like claude-squad, using tmux as the session backend and your terminal for tab/window management.

Single Go binary. No daemon, no network, no background process.

## What it looks like

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ ^__^                                                                        │
│ (oo)\   moomux                                       project1  project2     │
│ (__)\                                                                       │
├─────────────────────────┬───────────────────────────────────────────────────┤
│ SESSIONS                │ DETAIL                                            │
│                         │                                                   │
│ test-session   ⬤ park  │ status:    ⬤ parked                              │
│ moomux-work-4  ⬤ work  │ name:      test-session                           │
│ moomux-work-3  ⬤ wait  │ branch:    test-session                           │
│ moomux-work-2  ⬤ wait  │ worktree:  ~/.local/share/moomux/worktrees/…      │
│ moomux-work    ⬤ park  │ tmux:      moomux-test-session                    │
│                         │ created:   6 min ago                              │
│                         │                                                   │
│                         │  _____________________________________            │
│                         │ / this is my very first prompt of the \           │
│                         │ \ session. just say hello world pls   /           │
│                         │  -------------------------------------            │
│                         │         \   ^__^                                  │
│                         │          \  (oo)\________                         │
│                         │             (__)\        )\/\                     │
│                         │                 ||----w |                         │
│                         │                 ||      ||                        │
│                         │                                                   │
├─────────────────────────┴───────────────────────────────────────────────────┤
│ n:new  enter:open  x:kill  d:delete  tab:switch  r:refresh  q:quit          │
│                                                     P:+project  D:-project  │
└─────────────────────────────────────────────────────────────────────────────┘
```

## What it does

For each session, moomux:

1. Creates a git worktree on a new branch off your base branch
2. Starts a detached tmux session in that worktree
3. Launches `claude` inside it
4. Opens an iTerm2 tab attached to the tmux session

Status is detected by polling `~/.claude/sessions/*.json` every 2 seconds and matching the session's `cwd` to known worktree paths.

## Supported terminals

moomux auto-detects your terminal from environment variables and opens sessions in a new window/tab. Supported terminals:

| Terminal        | Platform       | Detection              |
|-----------------|----------------|------------------------|
| iTerm2          | macOS          | `TERM_PROGRAM=iTerm.app` |
| Apple Terminal  | macOS          | `TERM_PROGRAM=Apple_Terminal` |
| Kitty           | macOS / Linux  | `KITTY_WINDOW_ID`      |
| WezTerm         | macOS / Linux  | `WEZTERM_PANE`         |
| Alacritty       | macOS / Linux  | `TERM=alacritty`       |
| GNOME Terminal  | Linux          | `VTE_VERSION`          |
| Konsole         | Linux          | `KONSOLE_VERSION`      |
| xterm           | Linux          | `XTERM_VERSION`        |
| Tilix           | Linux          | `TILIX_ID`             |

If your terminal isn't listed, moomux falls back to printing the `tmux attach` command for you to run manually.

## Requirements

- macOS or Linux
- Go 1.22+
- `tmux`
- `git`
- `claude` CLI on `$PATH` (or `codex` / `opencode`, depending on your project config)
- A supported terminal (see below)

## Install

```bash
git clone https://github.com/erickgnclvs/moomux
cd moomux
make install              # builds and installs to ~/.local/bin/moomux
```

Make sure `~/.local/bin` is on your `$PATH`.

## Configure

First run creates `~/.config/moomux/config.toml` with a commented example. Edit it directly, or press `P` inside the TUI to add a project and `D` to remove one.

```toml
[projects.project1]
repo          = "~/Development/project1"
branch_prefix = "erick"   # optional — prepended to the branch name
base_branch   = "main"
agent         = "claude"  # optional — "claude" (default), "codex", or "opencode"

[projects.other_repo]
repo        = "~/Development/other_repo"
base_branch = "main"
```

Each project key (`project1`, `other_repo`) becomes a tab in the TUI.

State lives in two places:

| Path                                      | What it holds                          |
|-------------------------------------------|----------------------------------------|
| `~/.config/moomux/config.toml`            | Project registry (you edit this)       |
| `~/.config/moomux/sessions.json`          | Active session metadata (moomux edits) |
| `~/.local/share/moomux/worktrees/<proj>/` | Worktree checkouts (one per session)   |

## Run

```bash
moomux
```

## Keybindings

| Key             | Action                                  |
|-----------------|-----------------------------------------|
| `↑` / `k`       | Move selection up                       |
| `↓` / `j`       | Move selection down                     |
| `enter`         | Open session in a new terminal window   |
| `n`             | New session (inline form)               |
| `x`             | Kill session (tmux only, keep worktree) |
| `d`             | Delete session (confirmation)           |
| `r`             | Force refresh                           |
| `tab`           | Cycle through projects                  |
| `P`             | Add a new project                       |
| `D`             | Remove current project                  |
| `q` / `ctrl+c`  | Quit                                    |
| `esc`           | Cancel current overlay                  |

## Status states

| Indicator    | Label    | Meaning                                                 |
|--------------|----------|---------------------------------------------------------|
| `⬤` green   | working  | Claude is actively running in the worktree              |
| `⬤` amber   | waiting  | Claude is attached but idle, waiting on you             |
| `⬤` dim     | parked   | Worktree exists, no tmux session — resume via `enter`   |

## New session flow

1. Press `n` → inline form appears
2. Type a session name → press enter
3. moomux:
   - `git fetch origin <base_branch>`
   - `git worktree add <path> -b <branch_prefix>/<name> origin/<base_branch>`
   - `tmux new-session -d -s moomux-<name> -c <worktree>`
   - `tmux send-keys claude Enter`
   - opens a new terminal window/tab attached to the new tmux session

If `branch_prefix` is unset the branch is named `<name>` directly.

## Delete flow

1. Press `d` → confirmation overlay
2. Press `y` → moomux:
   - kills the tmux session (if running)
   - removes the worktree (`git worktree remove --force`)
   - drops the entry from `sessions.json`
   - **keeps the branch** so you don't lose work

## Architecture

```
moomux/
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
make build                 # produces ./moomux
make run                   # build + run
```

## Out of scope (v1)

- Windows terminal openers
- PR/issue integration
- Session search / filter
- Branch ahead/behind status
- Multiple Claude instances per session
- Gemini CLI agent support (planned)
- Per-session agent override (agent is set per-project)

## Contributing

Contributions are welcome and encouraged.

I built moomux around my own workflow — iTerm2 on macOS, opening each agent in a new tab. That's the path I know best and the one that gets the most polish. If you use a different terminal or OS and want to improve the experience there, please go for it. The terminal-opener logic lives in `internal/iterm/` and the detection logic in `internal/app/`, so that's a good place to start.

Some areas that could use help:

- Better tab/window management for terminals beyond iTerm2
- Linux-native workflows (GNOME Terminal, Kitty, WezTerm, etc.)
- Windows support
- Anything in the [Out of scope](#out-of-scope-v1) list you feel strongly about

Open an issue or a PR — both are appreciated.

## License

MIT
