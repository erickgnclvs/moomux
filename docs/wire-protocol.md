# The wire protocol and how data flows

moomux's core — worktrees, tmux, agent state — runs in one process. Front
ends attach to it: the Go TUI in-process, or anything at all over a unix
socket (`moomux serve`). This describes what crosses that boundary and why
it's shaped the way it is.

The short version: **the core computes, clients render.** If a front end
would have to work something out in order to draw it, that's a bug in this
protocol, not a thing for the front end to implement.

## The two channels

Everything moomux serves goes over one of two paths, and which one it uses is
the most important thing to understand about the protocol.

**Pull — one request, one response, connection closes.** Used for the things
a person *does*: create a session, rename one, add a project, change the
theme. 28 methods, dispatched by name.

**Push — the `Watch` stream.** Used for the things that *are true right now*:
what every session is doing, what its worktree looks like, what order to show
them in. Opened with `{"method": "Watch"}`, then one
`sessionview.Snapshot` per line until the client hangs up.

A front end reads state off the stream and never polls for it. Rendering
makes zero requests.

```
                  ┌─────────────────────────────────────────┐
   agent logs ───▶│                                         │
   (fsevents /    │   internal/watcher    raw agent state    │
    sqlite /      │          │            keyed by path      │
    hook markers) │          ▼                               │
                  │   internal/sessionview                   │
   tmux ─────────▶│     · joins with tmux liveness           │
                  │     · labels, quips                      │
   git / gh ─────▶│     · git + PR status (cached, jittered) │
                  │     · first-prompt recovery              │
                  │     · display order                      │
                  │     · keeps tmux window titles in step   │
                  │          │                               │
                  │          ▼                               │
                  │      Snapshot  ──────┬──────────────┐    │
                  │                      │              │    │
                  │   internal/app ◀─────┼── requests ──┼──┐ │
                  └──────────────────────┼──────────────┼──┼─┘
                                         │              │  │
                              in-process │       socket │  │ socket
                                         ▼              ▼  │
                                    Go TUI         Mac app ─┘
```

Both front ends read the identical `Snapshot`. In-process it's a Go channel;
over the socket it's the same struct as JSON. There is deliberately no wire
type in between — a translation layer is where the last drift came from (the
served quip was picked from the raw state while the TUI picked its own from
the joined state, so a parked session's cow told the Mac app it was working).

## The push channel: `Snapshot`

```jsonc
{
  "sessions": [ /* session.Session, in display order */ ],
  "views": {
    "moomux:a": {
      "id":       "moomux:a",
      "state":    "needs-input",          // never an integer — see below
      "label":    "mooing for you",
      "quip":     "moo-ve this along, please",
      "prompt":   "fix the thing",
      "git_ok":   true,
      "dirty":    true,
      "pr":       {"state": "OPEN", "mergeable": "MERGEABLE", "ci": "PASSING", "unresolved": 0}
    },
    "moomux:b": {
      "id": "moomux:b", "state": "parked",
      "label": "in the barn", "quip": "mootering off for now"
    }
  },
  "poll_time": "2026-09-06T18:00:00Z",
  "err": ""                                // a scan failure, as text
}
```

A snapshot is **absolute state, never a delta**. A client that misses one
loses nothing; the next supersedes it. That's why coalescing a burst is just
"take the newest" (`mergeSnapshots` in `internal/tui/model.go`), and why the
TUI replaces its whole view map on every tick rather than merging.

### What each field spares a client

| Field | What a client would otherwise have to do |
|---|---|
| `state` | Join the watcher's **path**-keyed states against a **session-id**-keyed tmux-liveness map, from two independently-timed polls, and know that "tmux is gone" *is* what parked means |
| `label`, `quip` | Keep its own copy of the wording, and a hash-pick over the quip pools |
| `prompt` | Parse Claude's JSONL, codex's sqlite, and opencode's sqlite — then fall back to the stored prompt |
| `git_ok`/`dirty`/`unpushed` | Run `git status` and `rev-list` per session, on its own staleness schedule |
| `pr` | Run `gh pr view` per session, network-bound and rate-limited |
| `sessions` order | Apply the live-first tiebreak on top of the manual/recent-first sort |

The last one is subtle and worth stating plainly: **`sessions` is already in
display order.** A client filters it (by project, by archived — those are the
client's own view state) and renders the result. It does not sort. Half the
ordering rule used to live in the core and half in each client, which meant
two front ends could list the same project differently.

### `state` is a name, not an integer

`watcher.State` marshals as `"unknown"` / `"parked"` / `"done"` / `"working"`
/ `"needs-input"`.

The Go enum behind it is deliberately *ranked* — `NeedsInput` sits above
`Working` so a stale "busy" write can't hide it — which means those integers
exist to be reordered. As bare ints, one re-rank for a reason having nothing
to do with the wire would silently reassign every state a second front end
displays, with no compile error on either side. The names also match the
per-state colour keys `Themes` serves, so a client gets one vocabulary
instead of two.

### Nudge: the client's half of the stream

The stream isn't one-way. Any line a client writes on a live `Watch`
connection is a nudge: *send a snapshot now, don't wait for the tick.*

```json
{"nudge": true}
```

It exists for the moments where waiting out the interval is visible — a
session just created, or just parked. It is not a substitute for the core's
own timer, which catches three things no event can announce:

1. **tmux dying.** A pane exits, a tab closes, someone runs `tmux
   kill-session` elsewhere. tmux notifies moomux of none of it.
2. **Time-based decay.** `Working → Done` fires when a timestamp gets old.
   The transition has no triggering event *by definition* — the thing that
   happened is that nothing happened.
3. **Staleness.** A worktree goes dirty from another terminal; CI flips on
   GitHub. Outside moomux entirely.

Where an event *does* exist, it's already used: the agent watchers are
fsevents-driven on darwin with a debounce, and needs-input markers are
written by installed agent hooks (`internal/claudehook`,
`internal/codexhook`). The timer is a floor, not the primary signal.

## The pull channel

One JSON request line in, one response line out, connection closes. No
pooling and no multiplexing — a unix connect is tens of microseconds, and
independent connections mean a slow `CreateSession` never queues behind
anything.

```jsonc
// request
{"method": "RenameSession", "args": {"id": "moomux:a", "name": "new-name"}}

// response
{"result": {"session": { /* ... */ }}}

// error response
{"err": "not a git repository: /tmp/nope", "code": "not_git_repo"}
```

`Args` and `Result` are unions — each method fills the subset it needs. One
union each beats 28 pairs of structs at this size; split them if the surface
doubles, or if two methods ever want the same field to mean different things.

`code` names a sentinel error the client branches on, since `errors.Is` can't
survive a string round trip. Only sentinels a front end actually tests for
need one; today that's `not_git_repo`, which drives the "init it here / add
as plain folder" dialog.

### Methods

**Read** — `Config`, `Sessions`, `AgentOptions`, `Themes`, `SuggestedProject`,
`WorktreeStatus`, `ChangeSummary`.

**Session lifecycle** — `CreateSession`, `OpenSession`, `EnsureTmux`,
`DeleteSession`, `KillTmux` (park).

`OpenSession` = `EnsureTmux` + open a terminal window on it. `EnsureTmux`
alone revives a parked session's tmux and agent and stamps `LastOpened`,
returning the same `hint`, but never touches iTerm — that's what a front end
that attaches tmux itself (the macOS app) calls, so only its explicit "open
in terminal" action spawns a tab.

**Session edits** — `SetSessionTags`, `SetSessionPrompt`, `SetSessionAgent`,
`RenameSession`, `SetSessionArchived`, `ReorderSessions`.

`ReorderSessions` takes the caller's fully-resolved final order (`ids`), not
a session plus a delta. A front end already knows the exact order it is
displaying, including the swap the user's keypress just made; re-deriving
sibling order in the core meant guessing at a list the client had already
filtered and sorted differently, and two rapid moves each carrying their own
delta could complete out of order and revert one another. Sessions not named
in `ids` are untouched, and an id that no longer exists is skipped rather
than failing the whole write.

**Folders** — `CreateFolder`, `SetSessionFolder`, `RenameFolder`,
`SetFolderCollapsed`, `DeleteFolder`, `SetProjectCollapsed`.

Folders are one flat level per project, and the name *is* the id (trimmed,
and rejected if empty or carrying a control character — see
`config.CleanFolderName`; validation is here rather than in a form handler
because a form is one front end's business). The split is deliberate: a
folder's own display state lives in config under `Project.Folders`, while
*membership* lives on each session as `Session.Folder`. So `RenameFolder`
and `DeleteFolder` return a config snapshot but also rewrite every member
session, and `SetSessionFolder` — which creates the folder on its first use
— is the one session mutator that returns both a session and a config
snapshot. A member pointing at a folder name that isn't in `Project.Folders`
renders as if the metadata were zero-valued rather than disappearing, which
is what keeps those two writes not needing to be one transaction.

What a folder does *not* carry is a position. A folder sits wherever its
first member sits, and one with no members at all sorts last by name. An
earlier version stored a per-folder order in the same units as
`Session.Order`; because `ReorderSessions` renumbers only the sessions it is
handed, the two number spaces drifted the moment anything was reordered
while a folder was collapsed, and in a project nobody had manually reordered
(every `Order` is 0 there) a collapsed folder rendered at the bottom of the
list instead of where its members were. A derived anchor can't drift.

`SetProjectCollapsed` is the same idea one level up: whether a project's own
group is collapsed, for a client that renders projects as a tree.
`config.Project.Collapsed` is served like any other config field. The TUI
has no caller for it — it shows one project at a time — and that is fine:
display state belongs with the rest of the state, not in one front end's
private preferences.

### Rows: the list layout, derived once

`Snapshot.Rows` is `Sessions` laid out as display rows, keyed by project:
each entry is a folder header or a session, in the order a client draws
them. Headers carry their collapse state and their member counts for both
the active and archived views; a session inside a collapsed folder is
present but marked `hidden`, and one inside any folder names it, so a
client can indent without joining back to the session.

This exists for the same reason `Sessions` is served in order rather than
sorted per client: grouping sessions under a folder, deciding where a
header goes, and knowing what a collapsed folder is hiding are all
derivations, and a second front end doing them in its own language is a
second chance to disagree about what a project looks like. Clients walk
`Rows` and render.

A hidden row is still a row. Manual reorder sends back the project's
*entire* order — hidden and archived rows included — because
`Store.Reorder` numbers what it is handed `1..N`, so any session left out
keeps a stale `Order` that then interleaves with the renumbered ones.
`sessionview.Blocks`/`Reorder` are the in-process helpers for computing
that; over the wire it is still just `ReorderSessions` with the full list.

**Projects** — `AddProject`, `InitProjectAndAdd`, `AddPlainProject`,
`UpdateProject`, `RemoveProject`, `MoveProject`.

**Settings** — `SetTheme`, `SetAutoSubmitDefault`, `SetSortRecentFirst`,
`SetAutoTmux`, `SetCompactDetail`.

Front-end-owned settings deliberately have no method here — see "act on the
right one" below.

Every config-mutating method returns the **post-mutation config snapshot** in
its own response, so a settings change costs one round trip rather than two.
Session mutators do the same with the updated session.

### `CreateSession` is a transaction, not a call

It takes one `session.CreateRequest` and the core runs the whole sequence:

```
worktree + branch  →  tmux pane + agent
                   →  attach PR tag
                   →  compose the first prompt
                   →  store it on the session
                   →  type it into the pane (submit if asked)
```

Composing that prompt means: prefix the thinking level *for agents with no
launch-time flag for it*, then append the ticket and PR URLs. Which agents
those are is derived from `reasoningEffortFlag`, not from a hardcoded name,
so "codex gets a real `-c model_reasoning_effort` flag, everyone else gets
the magic word" has one source.

**Once the pane exists, the session is real.** Nothing after that point may
turn a partial failure back into a failed create — a PR tag or a first prompt
that doesn't land comes back as a `hint` on an otherwise successful result. A
client reporting it as a failure would show nothing while the session, its
worktree and its branch all sat there.

This is a transaction rather than six ordered calls because it is the
sequence a front end most easily gets subtly wrong. It used to be six calls
made in order by the TUI, and `moomux spawn` — the only other caller in this
repo — had already drifted: it composed the prompt differently and never
stored it, so a spawned session showed no prompt in the list, ever.

### Things that stay a pull, on purpose

- **`Sessions`** — the cold start, before the first snapshot, and the moment
  right after a mutation this client made, where the streamed list is a beat
  behind the store.
- **`WorktreeStatus`** — the delete dialog's own check. The snapshot carries
  a status, but it can be up to a minute old, and this is the guard before a
  destructive action. It also refreshes the remote ref, which
  `ChangeSummary` doesn't.
- **`ChangeSummary`** — file and commit counts for that same dialog.

## Rules for adding to this

**Serve resolved values, not raw ones.** A Go method that encodes semantics
the JSON doesn't carry is a rule every other front end has to reimplement.
`View.label`, `View.prompt` and the served display order are this pattern
done right; `config.Themes()` is the same idea for colour.

**Ask on the right machine.** Over the socket, the client and the core can be
different hosts, and anything derived from `$HOME`, `os.Getwd()`,
`runtime.GOOS` or the filesystem must be answered by the core.
`SuggestedProject` (the add-project prefill) and `AddProject`'s path warning
both exist because the front end was answering for itself.

**...and act on the right one.** The mirror image: a few things happen on the
*viewer's* machine, not the orchestrator's — opening a window on their
screen, reading their clipboard, launching a GUI app they can see. Those are
the front end's to run, and their settings are the front end's to store, in
`config.Client` (`client.toml`) rather than `Config`. The test is whether the
value describes the machine someone is sitting at or the sessions being
orchestrated. Get this backwards and you get a setting that configures one
host and executes on another: the TUI's diff tool (`diff_tool`, the `D`
shortcut — see `internal/tui/difftool.go`) is the worked example, and the
core has no method for it on purpose. Nothing in `internal/app` or
`internal/ipc` should ever mention `config.Client`.

The terminal opener is the apparent counter-example and is worth
understanding before citing it: `App.Terminal` shells out to iTerm from the
core because the tab is *session state* — `CreateSession` opens it mid
transaction and `TermTabID` is stored on the session — and it handles the
split-host case with a guard instead (`OpenSession` skips it when
`browser.Remote()`). Lifecycle state, not viewer preference.

**`omitempty` on a meaningful `false`/`0`/`""` erases it.** A client decoding
into optionals can't tell "absent" from "the answer is no". Keep it off
fields that *are* the answer.

**No new client-side sorting.** If two front ends could disagree about an
order, the order belongs in the snapshot.

## Cross-repo contract

The Mac app ([afitzgerald/moomux-mac](https://github.com/afitzgerald/moomux-mac))
is a second consumer, in a separate repo, with **no CI link back to this
one**. It decodes into optionals, so a changed field name doesn't fail — it
just goes quietly nil.

### Migrating the Mac client

The reshape that produced this document **breaks the existing Swift client
silently** — it decodes into optionals, so nothing throws; fields just go
nil. What changed:

| Was | Now |
|---|---|
| `snapshotWire` — `states` (keyed by worktree path), `quips` | `sessionview.Snapshot` — `sessions` (ordered) + `views` (keyed by session id) |
| `"state": 3` | `"state": "working"` |
| `TmuxAliveAll` + a client-side join for "parked" | `View.state`, already joined |
| `PRStatus` per session | `View.pr` |
| `WorktreeStatus` polled per session | `View.git_ok` / `dirty` / `unpushed` |
| `SetSessionStatusTitle` called by the client | the core keeps window titles in step |
| `StartFirstPrompt` as a separate call | folded into `CreateSession` |
| `CreateSession` with a dozen flat args | `Args.req` (`session.CreateRequest`) |
| `Projects` | read `Config.projects` / the served order |
| `prstatus.Info` as `{"State":…}` | `{"state":…}` — lowercase, like everything else |

A version handshake would be cheap insurance against the next one; there
isn't one today.

So every struct here with a `json` tag is a contract: `sessionview.Snapshot`
and `View`, `session.Session` and `CreateRequest`, `config.Config`,
`config.Project`, `config.AgentOption`, `config.Theme`, `prstatus.Info`, and
the `Args`/`Result` unions. Check `Sources/Moomux/Core/Models.swift` by hand
when you touch any of them.

The same goes for two things that aren't structs at all: the Mac app attaches
with `tmux -CC` and parses tmux's line protocol, so it depends on the **names**
moomux gives tmux sessions — restructuring `internal/tmux`'s naming is a
change the Swift side feels invisibly. And `watcher.State`'s serialized names
are shared with the theme table's colour keys.
