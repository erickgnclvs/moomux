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

## Re-assessment (2026-09-07): close this. It's answered twice over.

Checked against `~/personal/moomux` @ `2bc0082` (the worktree this doc lives
in is orphaned — its base repo moved out of `~/tmp/moomux`, so `git` here is
dead) and against `~/personal/moomux-mac` @ `cd84c2f`. This doc was never
merged; upstream `docs/` doesn't have it.

**Code drift since the doc: none that matters.** `cmuxArgs`
(`internal/terminal/window.go:205`) is byte-identical; upstream only added a
wezterm opener next to it. `App.Tmux` is still a concrete `*tmux.Client`
threaded through ~20 call sites in `internal/app/app.go`, so option (a)'s
"touches most of app.go" cost estimate still stands.

### 1. cmux remote tmux does this from cmux's side

[Remote tmux](https://cmux.com/docs/remote-tmux) (beta, `cmux ssh-tmux
<destination>`) speaks tmux control mode (`tmux -CC`), parses the stream
itself, and maps **tmux sessions → workspaces, windows → tabs, panes → native
splits**, round-tripping splits/reorders as `split-window`/`swap-window`, plus
cwd on tabs, mouse and paste forwarding.

So moomux gets cmux-native surfaces without a cmux-native backend: it keeps
tmux, and cmux renders tmux. Both gating unknowns evaporate —
`StartFirstPrompt` keeps using `send-keys` (#1 moot), and nothing calls cmux's
socket, because cmux drives tmux from outside over SSH (#2 moot: moomux living
in a tmux pane is the topology the mirror *expects*).

### 2. moomux already owns a control-mode client — moomux-mac

`moomux-mac` is a shipping SwiftUI/SwiftPM app (signed, notarized, on a
Homebrew cask) whose whole architecture is the one cmux just described: a
`tmux -CC` client, per-pane SwiftTerm views, windows as tabs, layout sync,
scrollback restored via `capture-pane -S -1000`. Its design doc
(`moomux-mac/docs/native-macos-rewrite.md`) picked that as **Option A —
"native shell, tmux stays"** and explicitly rejected **Option B — "app owns
the PTYs, tmux is gone"**, which is exactly what this plan proposes, for
reasons that apply here unchanged: it kills session survival across
app/GUI crash and reboot, and kills the attach-from-your-phone story that is
the reason the tmux layer exists at all.

That doc also records the correction that took real building to find, and it
is the caveat my first pass at this re-assessment missed:

> control mode does *not* escape tmux's shared window size. A `-CC` client
> sets its size with `refresh-client -C` and the window follows it,
> letterboxing every other client, exactly as a plain `tmux attach` does.
> Grouped sessions don't help — a group shares the windows themselves. Both
> measured.

Nothing about cmux's implementation can dodge that; it's tmux's model, not the
client's. Practical consequence: pointing cmux at the same tmux session
moomux's TUI or a phone is attached to will resize/letterbox them. It's the
same wall that forced moomux-mac's multi-session grid to be periodic
read-only `capture-pane` snapshots instead of live views. Expect cmux's mirror
to be a one-viewer-at-a-time proposition in practice.

### Verdict

- **Don't build the cmux-native backend.** Neither option (a) nor (b). It's
  Option B of a decision already made against, and the payoff it was chasing
  now arrives free from two directions.
- **Keep `cmuxArgs` as is** (24 lines, works). Under the mirror it's mostly
  redundant — moomux's tmux sessions show up as workspaces unaided — but it
  costs nothing and covers cmux users who haven't enabled the beta. The manual
  `tmux attach` degradation for tmux-nested moomux stops being worth
  engineering around; at most it gains a line pointing at `cmux ssh-tmux`.
- **The duplication table above is still real but no longer worth paying for.**
  Removing it means giving up tmux, and both cmux's mirror and moomux-mac say
  we don't have to.
- **Where cmux's mirror genuinely adds something moomux-mac doesn't**: remote
  boxes. moomux-mac talks to a local `moomux serve` socket; `ssh-tmux` is
  SSH-first by construction. That's the case for documenting it, not for
  building against it.

Caveats to know before recommending it to anyone: beta and opt-in (Settings →
Beta Features), tmux 3.2+, SSH-shaped (a local tmux server means sshing to
localhost — untested here), no attachment restore on cmux restart, primary
screen scrollback doesn't re-wrap on resize, multi-line paste goes as
keystrokes, and the shared-window-size letterboxing above.

**Next step (30 minutes, not a spike):** enable the beta, `cmux ssh-tmux` into
this machine, attach one *throwaway* session (not a live one — see
moomux-mac's `CLAUDE.md` for the isolated-core recipe), and confirm moomux
sessions appear as workspaces and take input. Then close this doc as solved
upstream.

## "How do we get cmux to talk back to moomux?"

Mostly you can't — and the direction that matters already flows, through tmux
rather than through an API.

### Upward (moomux → cmux): already wired, zero code

cmux labels tabs from tmux window names, and moomux already sets those to
`<status glyph> <project emoji> <name>` (`internal/app/app.go:821`,
`SetSessionStatusTitle` at `app.go:1177`). With cmux's own cwd-on-tab
tracking, its sidebar ends up showing moomux's model — project, session, live
status — without either side integrating.

The one rough edge is the workspace *label*, which comes from the tmux session
name: `moomux-<name>-<hash>`. Renaming the session would fix it and isn't
worth it — `TmuxSession` is the store's identity key (`HasSession`,
`CapturePane`, `KillTmux` all key off it), so that trades a cosmetic win for a
rename-migration bug. Leave it.

### Downward (cmux → moomux): no channel we can build

cmux is a closed third-party app, so we can only use hooks it already offers.
Three candidates, all dead or upstream:

| Channel | Verdict |
|---|---|
| A pane calls `moomux` / the serve socket | Works, and already happens — `internal/claudehook` reports agent status into the store. But that's the *agent* talking, not cmux. cmux adds nothing. |
| moomux polls `cmux workspace list --json` | Needs `CMUX_SOCKET_CAPABILITY`, deliberately withheld from anything reached through tmux. A launchd/`brew services` `moomux serve` doesn't help either: the capability goes to processes cmux *spawns*, and a login daemon isn't one. Dead by design. |
| A cmux workspace lifecycle hook (run a command on open/close/rename with the tmux session name in env) | Doesn't exist. This is the thing that would make it work, and it's a one-sentence feature request to cmux. |

### Collision to check first

cmux's agent-session hooks write into Claude's/codex's hook config
(`~/.cmuxterm/<agent>-hook-sessions.json` and friends) — and so does moomux's
`claudehook`/`codexhook`. Two owners of one settings file is a likelier source
of "cmux and moomux disagree about session state" than any missing API. Verify
that before chasing integration.

### The thing worth saying plainly

A GUI that talks to moomux properly already exists and is ours: moomux-mac
drives the full `moomux serve` API (36 methods, `internal/ipc/server.go:226`+)
*and* attaches the same tmux control mode cmux does. If the goal is "native
front end that knows about worktrees and PRs", it's built. cmux's mirror is
the version for machines the Mac app isn't on — mainly remote ones.

**So**: don't engineer a back-channel. Let tmux names be the API, file the
workspace-hook request upstream, and spend the effort on moomux-mac where we
own both ends.
