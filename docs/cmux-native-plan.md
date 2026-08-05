# moomux on cmux, without tmux

Exploratory plan for what a cmux-native moomux backend could look like:
opening agent sessions as cmux workspaces/panes/surfaces directly, instead of
tmux sessions moomux attaches a terminal tab to.

## Background

moomux currently manages every agent session as a tmux session
(`internal/tmux/tmux.go`), and separately detects/opens a terminal tab to
view it (`internal/terminal/`). We added `cmux` as a supported terminal
(`TabReopener` via `cmux select-workspace`/`new-workspace`), but discovered
cmux's control socket refuses any process reached through tmux by design —
its per-launch auth capability (`CMUX_SOCKET_CAPABILITY`) is deliberately
never synced into tmux's shared global environment (see
`cmux-zsh-integration.zsh`'s `_CMUX_TMUX_SYNC_KEYS` comment: "tmux's global
environment is readable by clients that were not started inside cmux").
Since moomux itself normally runs inside a tmux pane, the tab-reuse feature
degrades to a manual `tmux attach` hint in that case — which is correct,
not a bug, given cmux's security model.

That raised the real question: what if moomux didn't put tmux in the middle
at all when the terminal is cmux?

## What cmux already gives you for free

cmux isn't just a terminal to open tabs in — it has native agent-session
surfaces (`cmux new-surface --type agent-session --provider
codex|claude|opencode`) with their own hook-based lifecycle tracking,
notifications, auto-naming, and resume-on-relaunch
(`~/.cmuxterm/<agent>-hook-sessions.json`, cmux's `docs/agent-hooks.md`).
Concretely, cmux already reimplements most of what moomux's
tmux+hooks+watcher stack does today:

| moomux does this today (via tmux) | cmux already has it natively |
|---|---|
| `claudehook`/`codexhook` write hook configs, `watcher` polls a sqlite/marker dir for `needs-input`/`working`/`done` | Agent hooks record lifecycle (`running`/`idle`/`needsInput`/`unknown`) per session automatically; surfaced as the workspace's `todo` status lane |
| `SetSessionStatusTitle` renames the tmux window with a status glyph | `workspace status` (inferred) drives the sidebar badge/color directly |
| Re-attaching a dead tmux session, retyping the launch command | Session restore: cmux reruns `claude --resume <id>` / `codex resume <id>` / `opencode --session <id>` itself |
| Nothing today (moomux sessions just sit there) | Agent Hibernation: kills idle agents to free RAM, auto-resumes when you revisit the tab |
| tmux window title as the only status signal | Push notifications bridged from the agent's own tool calls |

So a no-tmux cmux backend isn't "moomux re-implements its tmux logic
against a different multiplexer" — it's closer to "moomux stops
duplicating machinery cmux already owns, and narrows down to what only
moomux does": git worktree creation, cross-project/cross-window session
organization, and ticket/PR tagging.

## Proposed shape

**What moomux still owns**: worktree lifecycle (create/prune), the project
list, ticket/PR metadata, and the cross-project dashboard view aggregating
sessions cmux itself doesn't know how to group.

**What moomux hands to cmux per session**:

- Create: `cmux workspace create --cwd <worktree> --name <session-name>`
  then `cmux new-surface --type agent-session --provider <agent> --workspace
  <ref> --working-directory <worktree> --focus true` instead of `tmux
  new-session` + typing the agent command.
- Open/reopen: `cmux workspace select <ref>` (same shape as the `OpenTab` we
  already built) — but if the workspace was closed, there's no tmux session
  to recreate; this would rely on cmux's own resume (or recreating the
  surface and letting cmux's hook-recorded session id resume it).
- Status: poll `cmux workspace list --json` / `cmux tree --json` per
  window, reading the todo-lane cmux already infers, instead of moomux's own
  watcher/sqlite/hooks.
- Close: `cmux workspace close <ref>` instead of `tmux kill-session`.

**Code shape**: today `App.Tmux` is a concrete `*tmux.Client`, not an
interface (`internal/app/app.go:28`) — the whole `App` is hard-wired to
tmux (`NewSession`, `HasSession`, `PaneCwd`, `CapturePane`, `SetWindowName`,
`KillSession`, etc., `internal/tmux/tmux.go`). A cmux-native path means
either:

(a) carving out a `SessionBackend` interface both `tmux.Client` and a new
`cmuxBackend` satisfy, with `session.Session` gaining a backend kind + cmux
workspace/surface ref instead of `TmuxSession` — the honest fix, but touches
most of `app.go`; or

(b) a narrower parallel path just for cmux-launched sessions that bypasses
`App.Tmux` entirely for that session kind — smaller, but leaves two
divergent code paths.

## Real unknowns to spike before committing

1. **Can moomux inject the first prompt into an agent-session surface?**
   `new-surface` has no `--command`/initial-text flag for `--type
   agent-session` — unclear if `cmux send --surface <ref> <text>` even
   works against it, since it's a custom React/Solid-rendered surface, not a
   raw PTY like a terminal surface. moomux's `StartFirstPrompt` flow depends
   on this.
2. **Auth, again**: creating/managing these workspaces still goes through
   the same restricted socket — this only works when moomux's own process
   is a direct cmux-spawned child, never nested in tmux (which, per the
   `TabReopener` work, is moomux's current default habitat). Going
   tmux-less for sessions doesn't help if moomux's own TUI still runs
   nested.
3. Does `workspace list --json`'s status lane give enough granularity to
   distinguish moomux's three states (`working`/`needs-input`/`done`), or
   does it collapse detail moomux currently relies on?

## Suggested next step

Spike #1 and #2 first — they gate whether this is viable at all versus a
dead end:

- Create a real agent-session surface and try sending it a prompt via
  `cmux send`/`cmux send-key`, to see if `StartFirstPrompt` has any
  equivalent.
- Confirm whether moomux's own process can run un-nested from tmux in
  practice (i.e. whether users would accept running the moomux TUI itself
  directly in a cmux workspace rather than inside a tmux session).
