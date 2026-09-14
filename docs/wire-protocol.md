# The wire protocol and how data flows

moomux's core — worktrees, tmux, agent state — runs in one process. Front
ends attach to it: the Go TUI in-process, or anything at all over a unix
socket (`moomux serve`). This describes what crosses that boundary and why
it's shaped the way it is.

The short version: **the core computes, clients render.** If a front end
would have to work something out in order to draw it, that's a bug in this
protocol, not a thing for the front end to implement.

## One core, many front ends

`moomux serve` runs the core; everything else attaches. That includes a plain
`moomux`: it probes the default socket first and only builds its own core
when nothing answers.

That is not a convenience — it is the whole reason the protocol holds. Two
cores over one `config.toml` each keep their own in-memory copy, and only the
*write* paths re-read the file (`config.Reload`, on all eleven of them). The
read paths never do. So a project added in one process was invisible to the
other until it restarted. The session list was the exception, and the tell:
`App.Sessions` calls `Store.Reload()` on every read, which is exactly why
sessions stayed in step while everything else drifted.

Attaching means there is one copy of the truth, and the stream below is how
every front end stays level with it.

Two consequences worth knowing:

- A plain `moomux` behaves differently depending on whether a serve is up.
  That is intended, but it does mean an old `moomux serve` left running after
  an upgrade gets attached to by the new binary, and there is no version
  handshake (see the folder migration note below for why). Restart serve when
  you upgrade.
- The one-time first-run prompts (tmux.conf setup, auto-tmux) write
  `config.toml` directly, so they only run on the path that owns the core.

## The two channels

Everything moomux serves goes over one of two paths, and which one it uses is
the most important thing to understand about the protocol.

**Pull — one request, one response, connection closes.** Used for the things
a person *does*: create a session, rename one, add a project, change the
theme. 36 methods, dispatched by name.

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
  "cfg": { /* the whole config.Config: projects, folders, theme, settings */ },
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
| `cfg` | Notice that *another* front end changed the config — there is no other way to find out |

The last one is subtle and worth stating plainly: **`sessions` is already in
display order.** A client filters it (by project, by archived — those are the
client's own view state) and renders the result. It does not sort. Half the
ordering rule used to live in the core and half in each client, which meant
two front ends could list the same project differently.

### `cfg` is pushed, not just pulled

`Config` is also a pull method, and that's how a front end gets its first
copy. But a config a client only refreshed after *its own* writes went stale
the moment a second client attached: add a project in the Mac app and the
TUI would keep listing the old set, with the wrong theme and a stale folder
table, until it was restarted. So the config rides every snapshot, same as
everything else that is simply true right now.

Two rules for a client applying it:

- **`cfg` is absent, not empty, when there is no answer.** It's a pointer on
  the Go side and `omitempty` on the wire. The error-only snapshot
  `ipc.Client` emits when the connection drops carries none, and a client
  must not apply an empty config over the real one.
- **Ignore a snapshot older than your own last write.** Snapshots are built
  on the core's timer, so one built moments before your `SetTheme` can land
  moments after it and revert what the user just did for a whole interval.
  Compare `poll_time` against when you applied your own mutation's result —
  `applyStreamedCfg` in `internal/tui/update.go` is the reference.

What does *not* ride the stream is `config.Client` (`client.toml`): that
describes the machine a person is sitting at, not the sessions being
orchestrated, and the core has no method for it at all. See the front-end
split further down.

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
union each beats 36 pairs of structs at this size; split them if the surface
doubles, or if two methods ever want the same field to mean different things.

`code` names a sentinel error the client branches on, since `errors.Is` can't
survive a string round trip. Only sentinels a front end actually tests for
need one; today that's `not_git_repo`, which drives the "init it here / add
as plain folder" dialog.

### Methods

**Read** — `Config`, `Sessions`, `AgentOptions`, `Themes`, `SuggestedProject`,
`WorktreeStatus`, `ChangeSummary`.

**Session lifecycle** — `CreateSession`, `EnsureTmux`, `DeleteSession`,
`KillTmux` (park).

### The core never opens a terminal

Not on any method. Which emulator to launch, which of its tabs a session is
already in, and whether closing one is even meaningful are facts about the
machine a human is sitting at — and the core is not reliably that machine's
process. A `moomux serve` core started by launchd has no `TERM_PROGRAM` and
no `$TMUX` at all, so `terminal.Detect()` over there answers for the wrong
process.

So every front end goes through `terminalBackend` (`main.go`), which
decorates a backend — the core in-process, or the core over the socket,
identically — and does the terminal work itself. The local TUI, `moomux ui
-socket` and the `moomux park` worker all use it. `internal/app` doesn't
import `internal/terminal`.

What that leaves on the wire:

- `EnsureTmux` revives a parked session's tmux and agent, stamps
  `LastOpened`, and returns the `hint`. This is the whole of the core's
  "make this session usable" job.
- `OpenSession` **is** `EnsureTmux` — same call, kept because `tui.Backend`
  has an "open" verb that `terminalBackend` overrides, and `App` and
  `ipc.Client` have to satisfy it to be wrapped. There is no `OpenSession`
  method on the wire any more.
- `CreateSession`'s `req.OpenTerminal` is a statement, not an instruction:
  *a terminal is being opened for this session by the caller*. The core acts
  on it only by stamping `LastOpened` and skipping the "started in
  background — attach with…" hint that would otherwise be the user's only
  way in. It never opens anything.
- `KillTmux` and `DeleteSession` kill tmux and stop there. Closing the tab
  is `terminalBackend`'s, which resolves it *before* the call (the lookup
  below joins on tmux's attached clients, so after the kill there's nothing
  to find) and closes it *after* (closing first can take the tmux client
  down before the kill is issued).

There is no tab handle at all. `session.TermTabID` is gone, and so is the
`TabReopener` interface that produced the handles: each terminal finds the
tab a session is open in by joining `tmux list-clients` to what it can
report about its own tabs — tty (`#{client_tty}`) for iTerm2 and
wezterm, foreground-process pid (`#{client_pid}`) for kitty, which exposes
no tty. Nothing is persisted, and the tab survives a restart of the front
end or of the terminal. A stored handle would have done neither: kitty tab
ids and wezterm pane ids restart from zero with the process, so a remembered
one can name a stranger's tab.

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
`SetFolderCollapsed`, `DeleteFolder`, `ReorderFolders`,
`SetProjectCollapsed`.

Folders are **one flat global namespace**, not one per project: the name
*is* the id across every project (trimmed, and rejected if empty or
carrying a control character — see `config.CleanFolderName`; validation is
here rather than in a form handler because a form is one front end's
business). None of these methods takes a project, and `CreateFolder`
therefore refuses a name any project is already using. The old
`projects.*.folders` table is a migration source only: `config.Load` folds
it up into `config.folders` (merging identical names, AND-ing their
collapsed flags) and clears it, so a client reading the per-project map
sees nothing. That migration is destructive to old readers the moment a new
binary saves, which is why there is no version handshake and no
accepted-and-ignored `project` argument here.

The split is otherwise unchanged: a folder's own display state lives in
config under `Config.Folders`, while *membership* lives on each session as
`Session.Folder` — a bare global name now, same JSON, wider scope. So
`RenameFolder` and `DeleteFolder` return a config snapshot but also rewrite
every member session, in every project, and `SetSessionFolder` — which
creates the folder on its first use — is the one session mutator that
returns both a session and a config snapshot. A member pointing at a folder
name that isn't in `Config.Folders` renders as if the metadata were
zero-valued rather than disappearing, which is what keeps those two writes
not needing to be one transaction.

A folder now *does* carry a position: `FolderMeta.Order`, set by
`ReorderFolders(names)`, which takes the complete order and numbers it
`1..N` — the same "the client sends the order it is displaying, the core
never guesses one from a delta" contract as `ReorderSessions`. 0 means
never positioned and sorts last; ties break by name.

Unlike `ReorderSessions`, though, it is **total on its own**: it keeps
counting past `names`, numbering every folder it wasn't handed after them
in their existing relative order. A session left out of a reorder is
normally a *hidden* row the client still knows about and still sends back,
but there is no `Hidden` affordance at the folder level at all — a client
showing a subset of folders is showing its own filtered view and has
nothing to send for the rest. Renumbering only the named ones would leave
two disjoint `1..N` spaces interleaved. Unknown names are skipped rather
than rejected, so a reorder racing someone else's `DeleteFolder` doesn't
fail wholesale.

That position indexes **only the folder-first top level**
(`Snapshot.FolderRows`). The project-first `Rows` still anchor a folder to
its first member, and that is the point. An earlier version stored a
per-folder order in the same units as `Session.Order`, and the two had to
*interleave in one list*: a folder header and a loose session competed for
the same slot in one project's rows, while `ReorderSessions` renumbered
only the sessions it was handed. So they drifted the moment anything was
reordered while a folder was collapsed, and in a project nobody had
manually reordered (every `Order` is 0 there) a collapsed folder rendered
at the bottom of the list instead of where its members were. Folder
`Order` indexes a different *level of the tree* — folders against folders,
in a list `Session.Order` has no entries in — so there is no shared list
for the two spaces to drift apart inside of. That is the structural
difference, not a tidier implementation of the same idea.

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

`Snapshot.FolderRows` is the same layout turned inside out, for a client
that groups by folder first: one global list (not keyed by project) of
`folder` / `project` / `session` rows — folders in `Order`, a project
subheader under each for every project with members there, and that
project's members under it. Memberless folders live here, and only here: a
project's `Rows` emit a header only for a folder with a member in that
project, or every project would sprout a dead header for every folder in
existence. Collapsing a folder marks its subheaders and sessions `hidden`,
the same contract as `Rows`.

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

The terminal is the one step that isn't in there — see "The core never
opens a terminal" above.

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

Terminals are the other worked example, and they were the counter-example
until recently: the core opened them itself, because a tab looked like
session state — `CreateSession` opened one mid-transaction and a
`TermTabID` was stored on the session. It isn't. A tab is a thing on
somebody's screen, and the core is not reliably that machine's process; the
`browser.Remote()` guard it needed was the tell. All of it lives behind
`terminalBackend` in `main.go` now — see "The core never opens a terminal"
above. `internal/app` does not import `internal/terminal`, and
`TestAppDoesNotDependOnTerminal` keeps it that way.

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
| `OpenSession` (the core spawns a terminal for "Open in terminal") | **removed** — the app opens its own; see "The core never opens a terminal" |
| `session.Session.term_tab_id` | **removed** — never decoded on the Swift side, and nothing stores a tab handle now |

A version handshake would be cheap insurance against the next one; there
isn't one today.

### The global folder namespace

The folder reshape ([global-folders.md](global-folders.md)) is the second
such break, and it breaks the same way: `projects.*.folders` doesn't error,
it just decodes to nothing, so a user with folders sees none at all. There is no dual-write period — a new binary's first `Save` stops
writing the old table — so this has to land in the same release window.

| Was | Now |
|---|---|
| `config.projects.<name>.folders` — one table per project | `config.folders` — one global table keyed by name. The per-project field is `json:"-"` now and never reaches the wire again: it survives only as the source `config.Load` migrates out of `config.toml` and then nils |
| a folder had no position | `config.folders.<name>.order` — int; 0 means never positioned and sorts last, ties by name |
| — | `snapshot.folder_rows` — the folder-first layout (`kind` is `folder`/`project`/`session`), one global list alongside the project-keyed `rows` |
| `CreateFolder`/`RenameFolder`/`SetFolderCollapsed`/`DeleteFolder` took a `project` | no `project` argument. `Args.project` still exists for other methods, so an old call **succeeds** and acts on the global folder — nothing errors, so check the call sites rather than waiting for one |
| — | `ReorderFolders` with `Args.names`, the complete folder order |
| `session.folder`, scoped to the session's project | same field, same tag, no Swift edit — but the name is global now, so `auth` is one folder across every project |

So every struct here with a `json` tag is a contract: `sessionview.Snapshot`,
`View`, `Row` and `FolderRow`, `session.Session` and `CreateRequest`, `config.Config`,
`config.Project`, `config.AgentOption`, `config.Theme`, `prstatus.Info`, and
the `Args`/`Result` unions. Check `Sources/Moomux/Core/Models.swift` by hand
when you touch any of them.

The same goes for two things that aren't structs at all: the Mac app attaches
with `tmux -CC` and parses tmux's line protocol, so it depends on the **names**
moomux gives tmux sessions — restructuring `internal/tmux`'s naming is a
change the Swift side feels invisibly. And `watcher.State`'s serialized names
are shared with the theme table's colour keys.
