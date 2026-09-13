# Folders are a global namespace

A folder used to belong to exactly one project. Its identity was the pair
`(project, name)`, so `auth` in repo A and `auth` in repo B were two
unrelated folders that happened to share a string.

Now there is one global, flat folder namespace — the prerequisite for a
front end that groups by folder first and project second.

Everything below is implemented in Go and served over the socket. What is
**not** done is the Swift side (§8); until that lands, `moomux-mac` reads a
`projects.*.folders` table the core no longer writes. See
[wire-protocol.md](wire-protocol.md) for the rules this had to stay inside.

## 1. The data model

```go
// config.Config
Folders map[string]FolderMeta `toml:"folders,omitempty" json:"folders,omitempty"`

// config.Project
Folders map[string]FolderMeta `toml:"folders,omitempty" json:"-"` // DEPRECATED
```

`Project.Folders` keeps its **toml** tag because it is the migration source —
dropping it would make the old table unreadable and the migration a no-op on
exactly the configs that need it. It gains `json:"-"` and is nil everywhere
past `config.Load`, which clears it on every project unconditionally, not
only when the migration fires: leaving it populated means `Save` keeps
rewriting a second, stale copy of the folder table for a future reader to
disagree with.

`session.Folder` keeps its shape and its JSON tag — only its scope changed,
from "a name within this session's project" to "a global folder name". That
is the point: the Swift `Session` model needs no edit, and neither does any
other decoder.

`Config.Clone` clones the new map, the same trap it already handled for the
per-project one.

## 2. Live migration

**Read-side, in `config.Load`, permanent.** If `cfg.Folders == nil` *and*
some project has folders, `migrateFolders` folds them up. It is a pure
function — no I/O, no clock, no map-iteration order — because `Load` is a
pure read and because the `Order` it assigns has to come out identical on
every machine that reads the same `config.toml`.

- **Merge on identical name.** `auth` in two repos becomes one `auth`.
  Nothing is lost — members keep their `Project`, so a folder-first view
  renders `auth › repoA / repoB`, which is the view this change is for.
  Splitting them apart again is one rename; the suffixing alternative
  (`auth (repoA)`) leaves the user hand-cleaning names that were right.
- **`Collapsed` merges as AND** — collapsed only if every contributing
  project had it collapsed. OR would hide sessions the user never collapsed.
  It is deliberately not membership-weighted: `config` cannot reach the
  session store, so the member counts aren't available there.
- **`Order` is assigned deterministically**: walk projects in sorted name
  order, each project's folder names in sorted order, numbering `1..N` on
  first sight of a name.
- The trigger is `cfg.Folders == nil` only. An *empty* top-level table is a
  real answer ("the user deleted their last folder"), not an unmigrated
  config.

**The backup is taken in `Save`, not `Load`.** `Load` runs at every process
start and before all ~11 `App` mutators, so a backup taken there is spent by
a process that merely starts and never writes — after which a week of
old-binary use can pass and the genuinely destructive write finds the backup
already taken. Instead `Load` stashes the raw pre-migration bytes on an
unexported `Config.preFolders` field (unexported is skipped by both
BurntSushi and `encoding/json`, survives `*cfg = *fresh` in `Reload`, and
rides the value copy in `Clone`), and `Save` writes
`config.toml.pre-folders` beside the config the first time it is about to
make the merge irreversible. Via `atomicfile.Write`, never `os.WriteFile`:
it is the user's only copy of the old state and a truncate-in-place write
can leave it zero-length. Two processes racing the `os.Stat` guard both
write identical bytes atomically, which is harmless.

**Write-side.** The migration is destructive to old readers as soon as a new
binary calls `Save`: `projects.*.folders` stops being the source of truth,
and a client still reading it decodes into optionals and quietly shows no
folders at all. So the Swift change has to land in the same release window —
there is no dual-write period and no deletion commit to forget about.
`moomux-mac` has no CI link back to this repo, so "the same window" is a
thing someone has to actually do, not something a red build will remind them
of. For the same reason there is no version handshake and no compatibility
shim in `internal/ipc`: the `project` argument came off the folder methods
outright rather than being accepted-and-ignored.

## 3. Sorting

Three levels now — folder › project › session — and only the first needed
new storage.

| Level | Stored where | Set by |
|---|---|---|
| Folders (top level) | **new** `FolderMeta.Order int64` | `ReorderFolders(names []string)` |
| Projects within a folder | existing `Config.Order` | existing `MoveProject` |
| Sessions within (folder, project) | existing `Session.Order` | existing `ReorderSessions(ids)` |

**The one total order**, used everywhere folders are sorted: `Order`
ascending, **`Order == 0` sorting last**, ties broken by **name ascending**.
It lives in exactly one place, `sessionview.FolderOrder`, which
`internal/app` calls — it briefly existed as two byte-identical copies, and
two comparators that must agree forever is the kind of drift this codebase
has already paid for once. Folder order is never left to Go map iteration: a
snapshot has to be byte-stable across two consecutive builds of unchanged
state, because a streaming front end redraws on any difference.

Both creation paths — `App.CreateFolder` and `SetSessionFolder`'s
create-on-first-use — assign `max(existing Order) + 1`, so a position always
exists and `Order == 0` only ever means "written by a binary older than the
global namespace".

**Why folders now need a stored position**, when `FolderMeta` deliberately
refused one before: the old rule was "a folder sits where its first member
sits" (`sessionview.BuildRows`), and that anchor dissolves once members span
projects — each project has its own independent `1..N` space, so "first
member" has no cross-project meaning.

**Why this doesn't reintroduce the drift** that killed the last stored
folder order (wire-protocol.md, "What a folder does *not* carry is a
position"): that bug was two number spaces that had to *interleave in one
list* — folder positions and session positions competing for slots in a
project's rows, with a reorder renumbering only a subset of one of them.
Folder `Order` indexes a different level of the tree and never interleaves
with `Session.Order`.

`ReorderFolders` takes the list the client is displaying and numbers it
`1..N`, the same complete-list contract as `ReorderSessions` — the core
never guesses an order from a delta. It then **keeps counting**, numbering
every folder absent from `names` after them in their existing relative
order. Three extra lines, and they make the call total: unlike a session, a
folder has no `Hidden` affordance, so a client showing a subset is showing
its own filtered view, and renumbering only that subset would interleave two
disjoint `1..N` spaces. Unknown names are skipped rather than rejected — a
reorder racing someone else's `DeleteFolder` shouldn't fail wholesale.

**Interaction** (the folder-first client's contract; the TUI implements none
of it, see §6). The same `shift+↑↓` idiom at every level; which level moves
depends on what's selected. A folder header selected moves the whole folder
(one `ReorderFolders` call; the client already knows the order it is
displaying, and `ReorderFolders` is total on its own, so there is no
core-side helper to compute it — one existed briefly with no caller and was
deleted). A session
selected moves within its (folder, project) bucket and persists as
`ReorderSessions` with that project's *entire* order — which needs no new
helper, because a (folder, project) bucket is exactly one folder block in
that project's project-first rows, and `sessionview.Reorder`'s existing "a
member can't leave its folder" guard is therefore also "can't leave its
project". Cross-project moves stay impossible; `SetSessionFolder` remains
the only way out of a folder.

**One deliberate inconsistency:** the project-first view keeps the
first-member anchor, unchanged. So a folder can sit third in the
folder-first view and first in a project's own list. Making folder `Order`
authoritative in both means deciding how folder blocks interleave with loose
sessions inside a project — precisely the drift that got the old ordering
deleted. Two local rules, neither able to corrupt the other, beats one rule
that can.

## 4. API surface

The `project` argument is gone from `CreateFolder`, `RenameFolder`,
`DeleteFolder` and `SetFolderCollapsed`; `App.ProjectFolders()` is now
`App.Folders()`; `App.ReorderFolders(names)` is new. `SetSessionFolder(id,
name)` is unchanged in signature and gained cross-project reach for free.
IPC `Args` gained `Names []string` and lost nothing — `Project` is still
carried for every other method.

Consequences worth knowing:

- **`CreateFolder` still errors on a duplicate**, and the namespace is
  global now, so creating `auth` while another project already has one
  fails. That is a user-visible change, not just an API one — see §7.
- **`refileSessions` lost its project filter** and retags matching sessions
  in every project. Its blast radius being every project at once is why it
  now accumulates `errors.Join` and keeps going rather than returning on the
  first `Store.Put` failure — stopping early only leaves more sessions
  pointing at a name that no longer exists.
- **`RemoveProject` deliberately does *not* clean folders up.** A sweep was
  written and then removed: `RemoveProject` refuses while any session
  (archived included) still belongs to the project, so by the time it runs
  the project is filed under nothing and contributes nothing to which
  folders are memberless. "Delete the folders that ended up empty" is
  therefore not scoped to the project being removed at all — it takes every
  empty folder in the namespace, including one the user created in the
  overlay a keystroke ago for a project they are not touching. A memberless
  folder costs nothing to keep: it draws no header (`BuildRows` emits one
  only where the folder has members in that project), it shows as `(0)` in
  the Folders overlay, and `d` deletes it there on purpose.
- **`main.go`'s `saveConfig`** now reloads first. It saves a snapshot loaded
  before a blocking stdin prompt, which was merely last-writer-wins before
  but could now erase a whole global folder table created meanwhile. A bare
  `Reload` would have discarded the very first-run flag the caller just set
  (`Reload` overwrites the whole struct), so it captures `TmuxSetupAsked` /
  `AutoTmux` / `AutoTmuxAsked`, reloads, and re-applies them. Another flag
  added to those prompts has to join that line.

These are exported `App` signature changes — per AGENTS.md exactly the case
that passes every local `./...` command and then fails CI on a build error
in `e2e/`. Both suites:

```bash
go test ./... -race -shuffle=on -count=1
go test -tags e2e ./e2e/... -race -shuffle=on -count=1
```

## 5. sessionview

- **`Core` gained `Projects() []string`** (the user's order), and it is
  required, not optional. `projectsOf` used to union "projects with a
  session" with "projects that have folders"; the second half has nothing to
  iterate once folders are global, so without it a project whose sessions
  were all deleted would silently vanish from `Snapshot.Rows`. It is also
  the project order `BuildFolderRows` needs.
- **`Core.ProjectFolders()` → `Core.Folders()`**, the global map.
- **`BuildRows` emits a header only for a folder with at least one member in
  this project.** The old "memberless folders go last, by name" behaviour is
  gone, and was *not* replaced with a "memberless anywhere" exception: with
  one global namespace that rule would stamp a dead header onto every
  project's rows, for every folder in existence, forever. Nothing would even
  render it — the TUI's `visibleList` skips a zero-count header and its
  Folders overlay enumerates config directly. A memberless folder belongs in
  the folder-first layout, and is listed there.
- **New `folderrows.go`**: a `FolderRow` type (`Kind` is `"folder"` /
  `"project"` / `"session"`, always set) and `BuildFolderRows(sessions,
  folders, projects)`, served as **`Snapshot.FolderRows`** — one global
  list, not keyed by project — alongside the existing project-keyed `Rows`,
  both built in `Watcher.build`. A new type rather than a `Kind`
  discriminator on `Row`: the layouts genuinely differ (three levels, and it
  inverts the outer two), it is purely additive, and the project-first
  clients that already ship keep rendering the rows they always did.
  - Top level is folders in the total order from §3; under each, a
    `"project"` subheader per project with members there (in the given
    project order, unlisted projects appended sorted); under each subheader
    its members in the order they arrive in `sessions`. After every folder
    come the folder-less sessions, one `"project"` subheader each with
    `Folder == ""`.
  - The top-level folder set is the **union of the map keys and the distinct
    non-empty `Session.Folder` values**. A session can name a folder the map
    has never heard of — `SetSessionFolder` writes the two in separate steps
    and the config write can fail after the session one — and that session
    has to stay visible; `BuildRows` already honours this by emitting
    headers off the session loop.
  - A collapsed folder marks its subheaders and sessions `Hidden`, same
    contract as `Row.Hidden`. Folder headers carry global counts; project
    subheaders carry their own.

This is what keeps a folder-first front end from re-deriving anything — per
"the core computes, clients render", the tree arrives pre-laid-out.

## 6. The TUI stays project-first

No folder-first mode in the TUI. What changed in `internal/tui`:

- `currentProjectFolders` is now **`currentFolders`** and enumerates
  globally. It drives the Folders overlay, which is where global folders are
  created, renamed and deleted — filtering it to the active project would
  make a folder with no members here invisible and therefore unreachable,
  including one just created. A folder with no members in this project shows
  as `auth (0)`.
- `folderCounts` stays **project-filtered**. It is read off the same
  `BuildRows` output the list renders, so the overlay and the list headers
  can't disagree; a global count would print `auth (7)` above a list showing
  two rows. Only its metadata source changed, to the global map.
- `renderConfirmDeleteFolder` goes **global**, via a new `globalMemberCount`
  helper rather than a re-parameterised `folderCounts`. `DeleteFolder`
  un-parents members in every project now, so "N session(s) move back to top
  level" has to state the global number or it understates what `y` does.
  It is unfiltered on *both* axes, not just the project one: `DeleteFolder`
  un-parents through `refileSessions`, which walks the whole store ignoring
  archived state too, so the dialog reads the same whichever view the list
  is filtered to. A dialog saying "0 session(s) move back" while `y`
  silently clears three archived sessions is the failure that buys. This is
  the one place a folder-header count is the wrong answer.

  Because the number no longer matches the `(N)` the overlay showed a
  keystroke earlier, the dialog says so — "3 session(s) across 2 projects
  move back to top level." — whenever the two differ. The gate is that
  difference and not "spans more than one project": the starkest mismatch
  is a folder whose members all sit in a project the user is *not* looking
  at, where the overlay says `(0)`. The line wraps through `wrapLines`
  rather than `truncate`, because at 40 columns it otherwise lost "level."
  — the half that says nothing is destroyed.
- `setFolderCollapsedCmd` lost its project argument, and all three callers —
  the `z` key, the click handler, and the search-jump expand — read collapse
  state from `m.cfg.Folders`.
- `FolderCreatedMsg`, `FolderCollapsedSetMsg` and `FolderDeletedMsg` lost
  their now-meaningless `Project` field, along with the dead `proj` plumbing
  (`resolvedFolderHit.project`, `folderHeaderAt`'s project return) and the
  `len(m.projects) > 0` guards on every folder path — a folder mutator needs
  no project, and a folder can now outlive every project.
- `tui.Backend` carries `ReorderFolders` because it is the interface
  `ipc.Server` dispatches against, not because the TUI calls it. Nothing in
  the TUI does.

## 7. What a user will notice

Two behaviour changes fall straight out of the namespace being global. The
plan above is the *why*; these are what someone using moomux sees, and
neither is a bug to fix later.

**Collapsing a folder collapses it everywhere.** `FolderMeta.Collapsed` is
one bit per folder, not one per (project, folder) pair, so hitting `z` on
`auth` in repoA folds `auth` in repoB too. It is visible without switching
projects at all: the all-sessions multi-view draws every project side by
side, so a single collapse — `z` in the list, or a click on a folder header
in the multi-view itself — folds that folder in every panel at once.

This is the honest reading of the data model rather than an oversight. A
folder is now *one object* whose members span projects; a per-project
collapse bit would mean a folder that is simultaneously open and shut,
which the folder-first view — where a folder is a single top-level row —
has no way to draw. It is also what makes the migration's AND rule
(collapsed only if every contributing project had it collapsed) the only
safe merge: there is one bit to land on.

**Creating a folder can now fail on a name you can't see.** `CreateFolder`
refuses a name that exists anywhere, so `auth` in repoA blocks `auth` in
repoB with `folder "auth" already exists` — for a folder whose members are
all in a project the user isn't looking at. The Folders overlay is the
mitigation: it enumerates globally (§6), so the offending folder is listed
right there, and filing into it is what the user wanted anyway. The
alternative — silently making a second `auth` — is the thing one global
namespace exists to prevent.

## 8. Order of work

1. ~~`config`: field, migration, backup, `Clone`.~~ Done.
2. ~~`app`: signatures, `Folders()`, `ReorderFolders`, the retag helper.~~ Done.
3. ~~`sessionview`: the `BuildRows` header rule, `FolderRow` /
   `BuildFolderRows`.~~ Done.
4. ~~`ipc` and [wire-protocol.md](wire-protocol.md).~~ Done.
5. ~~`tui`: §6, plus goldens and screenshots.~~ Done.
6. **Swift, in the same release window as 1-5** — the remaining work.
   `Models.swift` reads `config.folders` and `snapshot.folder_rows`, and the
   folder methods drop their `project` argument. Then the new view.

Deliberately skipped: folder nesting, cross-project session moves, and a
folder-first mode in the TUI. Add the TUI mode when the Mac view proves the
layout is worth having twice.
