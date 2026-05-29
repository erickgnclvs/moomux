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

## Requirements

- macOS or Linux
- Go 1.22+
- `tmux`
- `git`
- `claude` CLI on `$PATH` (or `codex` / `opencode`, depending on your project config)

## Install

```bash
git clone https://github.com/erickgnclvs/moomux
cd moomux
make install              # builds and installs to ~/.local/bin/moomux
```

Make sure `~/.local/bin` is on your `$PATH`.

## Configure

First run creates `~/.config/moomux/config.toml`. Edit it directly, or press `P` inside the TUI to add a project and `D` to remove one.

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

Each project key becomes a tab in the TUI.

## Run

```bash
moomux
```

## Keybindings

| Key             | Action                                  |
|-----------------|-----------------------------------------|
| `↑` / `k`       | Move selection up                       |
| `↓` / `j`       | Move selection down                     |
| `enter`         | Open session in a new terminal tab      |
| `n`             | New session                             |
| `x`             | Kill session (keep worktree)            |
| `d`             | Delete session (confirmation)           |
| `r`             | Force refresh                           |
| `tab`           | Cycle through projects                  |
| `P`             | Add a new project                       |
| `D`             | Remove current project                  |
| `q` / `ctrl+c`  | Quit                                    |

## Status states

| Indicator    | Label    | Meaning                                                 |
|--------------|----------|---------------------------------------------------------|
| `⬤` green   | working  | Claude is actively running in the worktree              |
| `⬤` amber   | waiting  | Claude is attached but idle, waiting on you             |
| `⬤` dim     | parked   | Worktree exists, no tmux session — resume via `enter`   |

## Development

```bash
make test                  # go test ./... -race
make build                 # produces ./moomux
make run                   # build + run
```

## Contributing

Contributions are welcome and encouraged.

I built moomux around my own workflow — iTerm2 on macOS, opening each agent in a new tab. That's the path I know best and the one that gets the most polish. If you use a different terminal or OS and want to improve the experience there, please go for it. The terminal-opener logic lives in `internal/iterm/` and the detection logic in `internal/app/`, so that's a good place to start.

Open an issue or a PR — both are appreciated.

## License

MIT
