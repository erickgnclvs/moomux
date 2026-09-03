# Moomux.app

The native macOS front end. SwiftUI, SwiftPM executable, one dependency (SwiftTerm), driving the
Go core over the `moomux serve` unix socket — see `docs/native-macos-rewrite.md` for why this shape
and not a rewrite. This file is for working on it; the Go side's `AGENTS.md` still governs
everything outside `macos/`.

Today it lists sessions, streams their live agent state, shows detail, banners and dock-badges the
ones that start waiting on you, attaches a session's tmux inside the app — by default over tmux
**control mode**, with each pane in its own native view and the session's windows as tabs, and
optionally as one plain `tmux attach`
— shows every live session at once as read-only snapshots, hands a session to the user's terminal,
opens its diff for review in a tmux window of its own, and
creates, renames, tags, archives, reorders, kills and deletes them. Creating one here takes a
project, a name and a first prompt; anything more unusual is still the TUI's dialog.

## The environment decides more than you'd think

**There is no Xcode on this machine** — command line tools only (`xcode-select -p` →
`/Library/Developer/CommandLineTools`). This is not a preference, it is the constraint the whole
harness is built around:

- `xcodebuild` is unavailable. `swift build` is the only build, so there is **no `.xcodeproj`** and
  there cannot be one. `make app` assembles and signs the bundle by hand.
- **`#Preview` does not compile.** Its macro plugin ships with Xcode, not the toolchain. So there
  are no SwiftUI previews, and any dependency that uses `#Preview` internally cannot be adopted.
- **There is no test framework.** `import XCTest` → `no such module 'XCTest'`; `import Testing` →
  `no such module 'Testing'`. Both ship inside Xcode. See "the harness is `demo()`" below.
- SwiftUI, AppKit, `@Observable`, `MenuBarExtra`, `Settings`, UserNotifications and Network all
  compile fine. Only the Xcode-only macros don't.
- Notarization would still work without Xcode (`notarytool` and `stapler` are in CommandLineTools);
  only a Developer ID certificate is missing. Nothing here ships yet, so `dist`/`notarize` targets
  are deliberately absent — copy them from `~/tmp/mergeright/Makefile` when there is something to
  ship.

## Commands

```sh
make build                                   # swift build -c release. Must stay at zero warnings.
make selfcheck                               # the assert-based demo() checks — run after touching logic
make dev ARGS="--socket /tmp/mmx.sock"       # debug bundle, sign, relaunch
make run                                     # same, release
make shot OUT=/tmp/x.png                     # screenshot the running app
```

The app needs a running core:

```sh
go build -o /tmp/moomux . && /tmp/moomux serve -socket /tmp/mmx.sock
make dev ARGS="--socket /tmp/mmx.sock"
```

With no `--socket` it uses `ipc.DefaultSocket` (`~/.local/share/moomux/moomux.sock`), same as
`moomux ui`.

The bundle is not optional for anything that wants a bundle identifier — `swift run` produces an
unbundled binary, and `UNUserNotificationCenter.current()` **traps** without one. `Notifier` is
guarded on `Bundle.main.bundleIdentifier` for exactly that reason: `make selfcheck` runs the
unbundled `.build` binary.

## UI changes

Same rule as the Go TUI, different tool: you cannot see SwiftUI render by reading it. After any
change under `UI/`, run the app and look at a screenshot before calling it done, then send the
user a clickable `file://` link to the PNG.

```sh
make dev ARGS="--socket /tmp/mmx.sock" && sleep 8
make shot OUT=/tmp/moomux-macos.png
swift Scripts/ui.swift dump                  # the AX tree: labels, roles, frames
swift Scripts/ui.swift click "moo-hardness"  # drive it without a mouse
```

`Scripts/ui.swift` walks the AXUIElement tree directly — System Events' `entire contents` returns
an empty list against SwiftUI windows. It is how a screen other than the empty state gets
photographed at all.

Two traps when checking by screenshot:

- A **crash on launch is silent** through `open`. Confirm the process is still alive a few seconds
  later: `make dev && sleep 6 && pgrep -x Moomux`.
- Against a **locked screen**, `screencapture` photographs the lock screen and `osascript ... get
  count of windows` returns 0 for a perfectly healthy app. Neither is evidence of a problem.
- Other apps' menu-bar popovers float above ours and land in the shot. Retake rather than debug a
  layout that is not ours.

## Verifying a terminal change

A screenshot proves a terminal *drew* something. It does not prove keystrokes arrive, that the
right pane got them, or that detaching left the session alone — and all three have broken here.
tmux is the oracle for those, so ask it rather than squinting at pixels.

```sh
S=moomux-<session>-<hash>                       # from the session's Info pane
tmux list-clients -t $S                         # ours is the one marked control-mode
tmux list-panes  -t $S -F '#{pane_id} #{pane_width}x#{pane_height} active=#{pane_active}'
tmux list-windows -t $S -F '#{window_width}x#{window_height} #{window_layout}'
tmux display-message -t $S -p '#{pane_id}'      # which pane tmux thinks is active
tmux capture-pane -p -t $S.2 | tail -3          # what a pane actually contains
```

### Test against a throwaway session, not the user's

**Do not attach the app to a real moomux session to try something out.** Those are live agent
sessions someone is working in: attaching resizes them, synthetic keystrokes land in an agent's
prompt box, and a split or a kill is not yours to make. Stand up an isolated one instead — both the
config and the session store honour `XDG_CONFIG_HOME`, so a scratch core sees only what you give
it, and the real one is untouched:

```sh
rm -rf /tmp/mmxtest && mkdir -p /tmp/mmxtest/moomux /tmp/mmxtest/wt
tmux new-session -d -s cmtest -x 120 -y 40 && tmux split-window -h -t cmtest

cat > /tmp/mmxtest/moomux/config.toml <<'EOF'
[projects.testproj]
repo = "/tmp/mmxtest/wt"
EOF

cat > /tmp/mmxtest/moomux/sessions.json <<'EOF'
{"version":1,"sessions":{"testproj:panes":{
  "id":"testproj:panes","project":"testproj","name":"scratch","branch":"test/panes",
  "worktree_path":"/tmp/mmxtest/wt","tmux_session":"cmtest",
  "created_at":"2026-09-02T12:00:00Z","last_opened":"0001-01-01T00:00:00Z"}}}
EOF

XDG_CONFIG_HOME=/tmp/mmxtest moomux serve -socket /tmp/mmx2.sock &
make dev ARGS="--socket /tmp/mmx2.sock"
```

The session record is hand-written on purpose: nothing has to exist for it but a directory and a
tmux session of that name, so you can build any layout you want to test — four panes, two windows,
a zoomed pane — in seconds. Tear down with `tmux kill-session -t cmtest && rm -rf /tmp/mmxtest`.

Three things to know about that scratch home:

- **`moomux serve` caches `config.toml` in process.** Editing it and waiting for the app's 2s poll
  changes nothing — restart the core. `sessions.json` is the opposite: re-read per request, which is
  what makes "unknown session" reproducible by deleting an entry under a running server.

- **`XDG_CONFIG_HOME` isolates more than moomux.** `gh` keeps its credentials in
  `$XDG_CONFIG_HOME/gh`, so the core's `PRStatus` silently comes back "unknown" — the scratch home
  has logged `gh` out. `ln -s ~/.config/gh /tmp/mmxtest/gh` fixes it. Anything else the core shells
  out to that reads XDG will have the same problem.
- **Give the worktree a real git repo** if you want the worktree rows to say anything:
  `git init`, one commit, then dirty it. `WorktreeStatus` and `ChangeSummary` return `ok=false` for
  a plain directory, which correctly renders as no row at all — indistinguishable from a bug you
  did not write.

If you ever do have to touch a real session, **type into the shell pane, never the agent pane** —
text at a `zsh` prompt is harmless and erasable, text in an agent's prompt box is not. Select it
first (`tmux select-pane -t $S.2`), check with `capture-pane`, clear the line with `C-u`, and put
the active pane back. Leave it as you found it.

**Synthetic key events need a real event source.** `CGEvent(keyboardEventSource: nil, …)` silently
drops modifier flags, so a scripted `C-b` arrives as a bare `b` and whatever you are testing looks
broken when it is fine. Pass `CGEventSource(stateID: .hidSystemState)`. This cost an hour chasing a
focus bug that had already been fixed.

```swift
let src = CGEventSource(stateID: .hidSystemState)
let e = CGEvent(keyboardEventSource: src, virtualKey: 11, keyDown: true)!  // 'b'
e.flags = .maskControl
e.post(tap: .cghidEventTap)
```

Useful oracles that do not require reading the screen: `#{client_prefix}` goes to 1 after the tmux
prefix key reaches our client, and `#{pane_in_mode}` goes to 1 in copy or clock mode. Both are
unambiguous where a screenshot is a judgement call.

**Count the characters before believing a wrap is wrong.** A repainted pane can legitimately wrap
where tmux already did: `capture-pane -p` hands back screen rows pre-wrapped to the pane's width, so
a row that fills the pane exactly is tmux's wrap and not ours. Several rounds went into a
one-column bug that did not exist; `python3 -c "print(len(line), repr(line[:56]))"`
would have settled it immediately.

## Verification tricks that matter here

**A warning count from an incremental build is meaningless.** `swift build` only re-emits
diagnostics for files it recompiles, so a second build reports zero while the warning is still in
the source — and a clean build double-reports (module-emit and compile passes both). Count distinct
causes:

```sh
rm -rf .build && swift build 2>&1 | grep "warning:" | sed 's/.*warning: //' | sort -u
```

**The harness is `demo()`, because there is no test framework.** Each file with non-trivial pure
logic gets a `static func demo()` full of `assert`s, called from nowhere in production and run
through the real binary by `Moomux --selftest` (`App/SelfTest.swift`). When you add one, add its
call there.

**`assert` is compiled out entirely at `-O`**, so a release binary would print `selftest: ok`
having executed nothing. `SelfTest` proves asserts are live before running anything and exits 1
if they aren't; `make selfcheck` builds debug for this reason. Both directions are verified —
the release binary refuses, and inverting one assertion fails with a file and line. Do that
inversion check whenever you add a check that matters.

**Do not run concurrent `swift build`s** in this directory — they corrupt the shared `.build`. Use
`swiftc -parse <file>` for a syntax check while something else is building.

**Spike a dependency before adopting it.** A throwaway package settles in under a minute what the
README won't tell you — this is how `#Preview`-using libraries get caught. SwiftTerm was spiked this
way before it went into `Package.swift` (builds clean under CommandLineTools, `LocalProcessTerminalView`
instantiates). It is the only dependency; keep it that way for as long as possible.

**`NSLog` does not reach `log show` / `log stream` from this bundle.** Two rounds of debugging went
into a predicate that was never going to match. When you need to trace something inside the running
app, append to a file from the code and `cat` it — crude, instant, and it actually works. Every
non-obvious bug in the terminal work was found this way and by nothing else, usually by logging one
number (a frame, a column count, a pending-command depth) rather than by reading harder.

## Architecture

One-way flow, no exceptions:

```
moomux serve  (unix socket, JSON)
      |
MoomuxClient           one request line in, one response line out; Watch streams
      |
AppState.refresh()     @MainActor @Observable root store — poll + watch loops
      |
Views                  read AppState and nothing else
```

```
Core/UnixSocket.swift    AF_UNIX plumbing; blocking, closed to cancel
Core/Models.swift        the wire types + JSON coding + Wire.demo()
Core/MoomuxClient.swift  the Swift half of internal/ipc
Core/ToolPath.swift      finding tmux without a shell's PATH
Tmux/ControlProtocol.swift  the `tmux -CC` line protocol, pure
Tmux/TmuxLayout.swift    the layout string -> pane rectangles, pure
Tmux/ControlClient.swift the control-mode transport: forkpty, commands, events
App/AppState.swift       the single root store, poll loop, watch loop
App/Notifier.swift       the only file allowed to touch UNUserNotificationCenter
App/MoomuxApp.swift      scenes: main window + MenuBarExtra
App/SelfTest.swift       --selftest
UI/RootView.swift        split view, rows, detail, inspector, menu-bar content
UI/TerminalPane.swift    SwiftTerm hosting a plain `tmux attach`
UI/ControlModeView.swift native panes over control mode
UI/SessionGrid.swift     every live session at once, as capture-pane snapshots
```

Both `Tmux/` parsers are pure and checked by `demo()` against output captured from a real
`tmux -CC attach` — paste new captures in rather than inventing plausible-looking lines.

SwiftTerm is reached **only** through `UI/TerminalPane.swift`, so swapping it for libghostty later
is one file — which is the arrangement the plan doc assumes.

`internal/ipc/client.go` is the reference implementation of `MoomuxClient`. Keep the two honest
against each other — anything the Swift side cannot do over the socket is a hole in the boundary
to fix in Go, not a reason to link the core.

## Things that will bite you

- **The Go core spells its JSON keys two different ways.** `session.Session` has `json:"..."` tags
  and is snake_case; `config.Config` and `config.Project` have only *TOML* tags, so Go's encoder
  falls back to the Go **field names** (`Projects`, `BranchPrefix`). One `keyDecodingStrategy`
  cannot serve both, which is why every type in `Models.swift` declares explicit `CodingKeys`. If a
  decode starts returning nils, check the Go struct's tags first.
- **Watcher snapshots must be merged, never assigned.** `watcher.MultiWatcher` fans out one
  snapshot per sub-watcher, each carrying only its own agent's paths, so replacing wholesale wipes
  every other agent's sessions on every tick. Merge, then prune against live worktree paths — the
  watcher also reports on `/` and the home directory. `AppState.merge` and
  `internal/tui/update.go`'s `StatusTickMsg` are the same logic; change them together.
- **A failed call must not read as "empty".** `AppState.refresh` leaves the last-good lists in place
  and reports the failure through `connection`. The Go client caches for the same reason: one nil
  `Sessions()` would otherwise read as "every session was deleted".
- **Go's zero `time.Time` arrives as a real timestamp.** `omitempty` does nothing for a struct, so a
  never-opened session sends `"0001-01-01T00:00:00Z"` rather than omitting the key. The date
  strategy is lenient on purpose — a strict one would fail the whole `Sessions` call over it.
- **Go emits up to nine fractional digits**, `ISO8601DateFormatter` accepts three and returns nil
  on the rest. `Wire.parseTimestamp` drops the fraction; nothing displays sub-second time.
- **`close` on a socket class shadows the global.** Write `Darwin.close(fd)` inside a type that has
  its own `close()`, or the compiler resolves to the instance method and errors.
- **`sockaddr_un.sun_path` is 104 bytes.** Scratchpad paths blow past that — `UnixSocket` refuses
  rather than connecting to a truncated path, and this already happened once in practice. Put test
  sockets somewhere short, e.g. `/tmp/mmx.sock`.
- **Every `MoomuxClient` call blocks.** Anything touching git or tmux takes seconds on the Go side.
  Run them through `Task.detached` (`withoutBlockingTheUI`), never on the main actor.
- **`@main` conflicts with a file named `main.swift`**, which is why the entry point is
  `App/MoomuxApp.swift`.
- **`@Observable` fires on any assignment, equal or not.** Guard writes that repeat every tick
  (`AppState.set(statusError:)`) or every row re-renders on each watcher poll.
- **The app is not sandboxed**, which is the only reason `NSHomeDirectory()` finds the real socket —
  and the only reason the terminal pane can spawn anything at all. Sandboxing it means finding
  another way to the core.
- **A GUI app does not inherit your shell's `PATH`.** Launched from Finder it gets launchd's
  (`/usr/bin:/bin:/usr/sbin:/sbin`), so Homebrew's `tmux` is invisible and `Process` just reports
  that the executable does not exist. `ToolPath` searches `PATH` and then the usual prefixes. Every
  tool this app ever shells out to goes through it.
- **Every tmux client on a session shares one window size.** While the app is attached, the user's
  iTerm and phone are letterboxed down to the app's dimensions, and it only springs back on detach —
  not when the bigger client is used again. Grouped sessions (`new-session -t`) do **not** fix this;
  a group shares the windows themselves. Control mode does not fix it either: a `-CC` client sets
  its size with `refresh-client -C` and the window follows exactly the same way. All three measured.
  This is why attaching is an explicit action and not a consequence of selecting a row.
- **A control-mode client does not take its size from the pty.** `list-clients` reports it as `80x`
  no matter what winsize the forkpty was given; `refresh-client -C WxH` is the only lever, and until
  it lands the panes are laid out against the wrong grid.
- **tmux emits one unsolicited `%begin`/`%end` block right after attaching**, before
  `%session-changed`. Command replies are matched to commands by order, so that block has to be
  dropped — which it is, by not sending anything until `%session-changed` arrives and ignoring a
  reply that nothing is waiting for.
- **A new `NSView` has a zero frame, and a terminal soft-resets when it is later given a real
  grid.** Anything painted before that is wiped. A pane producing output repaints itself and hides
  the bug; an idle shell pane just stays black, which is how it was found. Paint on the first
  `sizeChanged`, never at registration.
- **`getOptimalFrameSize()` reports the terminal's *current* cols, which lag a resize**, so
  deriving a cell size from it inside `sizeChanged` is short by a column's worth. The container
  divided by tmux's own grid is the self-consistent measure, and its rounding error can only make a
  pane a hair *wide*, which is harmless.
- **Every SwiftTerm view silently reserves ~17pt of its width for a scroller.** In 1.20.0
  `reservedScrollerWidth` is `scroller?.isHidden == true ? 0 : scrollerWidth` — it ignores
  `scrollerStyle`, and nothing in the library ever hides the scroller. `getEffectiveWidth` takes it
  off before dividing by the cell width, so it is noise in a wide ruler (156 columns either way)
  and a **whole column** in a half-width pane. That is what "lines wrap when they shouldn't" looks
  like. `NoScrollerTerminalView` hides it; every terminal view here must inherit from it, or the
  ruler and the panes disagree about how many cells fit.
- **Read the dependency's source from `.build/checkouts/`, not a fresh clone.** The scroller bug
  above cost an extra round because the property was read from SwiftTerm's `main`, where it *does*
  check `scrollerStyle` and setting `.overlay` would have been enough. The pinned version behaves
  differently. `git clone --branch <tag>` also falls back to the default branch if the tag is
  missing, silently giving you the wrong source.
- **SwiftTerm marks most overrides `public`, not `open`** — `keyDown` cannot be overridden from
  this module, `mouseDown` and `viewDidMoveToWindow` can.
- **`capture-pane -p` returns screen rows, not logical lines** — already wrapped to the pane's
  width. `-J` is the flag that joins them back into logical lines, and the repaint does not pass it.
  Measured on tmux 3.7c in a 40-column pane: a 50-character line comes back as a 40-char row plus a
  10-char row plain, and as one 50-char line under `-J`. So a wrap in a repainted pane is usually
  tmux's own rather than a layout bug — count the characters against the pane width before believing
  it is ours. (An hour went into talking myself out of a wrap that was fine.)
  It also returns the pane's **full height**, blank rows below the cursor included — never trimmed —
  which is what makes `paintSequence`'s "H history lines + R screen rows into an R-row terminal"
  arithmetic exact.
- **Copy-mode is invisible to a control-mode client.** Entering it and scrolling emits
  `%pane-mode-changed` and `%window-renamed` and **zero** `%output` bytes: tmux renders copy-mode
  client-side for a normal client and sends a `-CC` client nothing to draw. Measured against a
  scratch session (`#{pane_in_mode}` was 1 throughout, so tmux really was in the mode). That is why
  scrollback is a `capture-pane` problem and not a scroll-gesture-proxying one — proxying would
  still need the capture, plus a mode to get stuck in and the user's own copy-mode fighting ours.
- **SwiftTerm marks its overrides `public`, not `open`.** `keyDown` and friends cannot be overridden
  from this module — the compiler says "overriding non-open instance method outside of its defining
  module". `viewDidMoveToWindow` is fine because it comes from `NSView`.
- **SwiftUI does not give the terminal first responder.** Focus stays on the sidebar list, so
  everything typed after "Attach" goes to the list instead. `updateNSView` is too early (no window
  yet); `AttachedTerminalView.viewDidMoveToWindow` is the hook that works.
- **`open` against an app that is still terminating does nothing at all**, which reads exactly like
  a crash on launch. `make run`/`make dev` wait out the old process for this reason — do not
  "simplify" that loop away.
- **Notification authorization needs a bundle LaunchServices has registered, and that means `open`
  from a real location.** Exec'ing the binary inside the bundle
  (`./Moomux.app/Contents/MacOS/Moomux`) fails instantly with `UNErrorDomain Code=1 "Notifications
  are not allowed for this application"` even though `Bundle.main.bundleIdentifier` is right, and so
  does a bundle sitting under `/tmp` — no prompt, same error, both measured. `.build/Moomux.app`
  opened by `make dev` is fine, and so is `~/Applications`. That error is very easy to misread as
  "ad-hoc signing doesn't work" (it isn't — see the signing bullet below).
- **Clicking a window tab moves the session, not just our view.** `select-window` sets the
  *session's* current window, and every client attached to it follows — the user's iTerm and phone
  jump to whatever tab was clicked here. Same class as the shared-size trap above and just as
  unfixable client-side: a control-mode client has no private notion of "which window I am looking
  at". It is the price of the tab bar, not a bug in it.
- **`%window-renamed` fires far more often than a window is renamed.** With tmux's automatic-rename
  on (the default), it arrives every time a window's foreground command changes — once per shell
  command. So the handler patches the name in the `windows` array in place, guarded on the name
  actually differing, rather than re-running `list-windows`: unguarded it is a round trip and a full
  view re-render per command typed in any window of the session.
- **The Dock badge is not free of authorization.** `NSDockTile.badgeLabel` looks like a plain
  property and reads like the fallback for when notifications are denied, but macOS silently drops
  it unless the app holds the **badge** permission — System Settings → Notifications has to say
  "Badges", not just "Sounds, Desktop". Requesting `[.alert, .sound]` and assigning `badgeLabel`
  gives you a bare Dock icon and no error anywhere. `Notifier` asks for `.badge` too, and asks in
  `init` rather than at the first banner: a session already waiting when the app opens sets the
  badge without ever being a *transition*, so no banner would have done the asking. The answer is
  sticky per bundle id, so a build that already earned an alert-only grant keeps it — flip Badges on
  by hand, or reset the app in System Settings, before deciding the code is wrong.
- **`center.add` returns no error while authorization is denied.** Measured: status denied, `add`
  err nil, nothing on screen. A missing banner gives you no signal at all, and denial is *sticky*
  per bundle identifier — quitting while the prompt is up is enough to earn it permanently. Read
  `getNotificationSettings().authorizationStatus` (0 notDetermined, 1 denied, 2 authorized) before
  believing the code is broken, and reset in System Settings → Notifications. Focus/DND does the
  same thing for a different reason.

## Conventions

- `public` on anything crossing a file boundary; one module, so this documents intent.
- Comments explain *why*, not *what*. Be sparing.
- `MARK:` sections in files over ~200 lines.
- Semantic colors only, via `Theme` in `UI/RootView.swift`. No literal hex — it breaks dark mode.
  Every primary action gets a `.keyboardShortcut`.
- New pure logic gets a `static func demo()` with `assert`s and a call in `SelfTest`. Trivial code
  gets nothing.
- Deliberate shortcuts get a plain comment naming the ceiling and the upgrade path.

## Deliberately not done

Decisions, not oversights. Don't "fix" these without being asked.

- **tmux's prefix key does not work in control mode, and cannot.** Keystrokes reach a pane through
  `send-keys -t %id`, which writes straight to that pane's pty, so tmux never sees `C-b` and every
  prefix binding is dead — `C-b o` types a literal `o`. Verified. iTerm2's tmux integration has the
  same property. The replacement is the **Pane menu** (⌘] / ⌘[ / ⌘D / ⌘⇧D / ⌘⇧↵), which issues
  `select-pane`, `split-window` and `resize-pane -Z` as commands. Anything else tmux can do is a
  one-line method on `TmuxControlClient` away; the plain attach is the fallback for muscle memory.
- **Control mode is the default; the plain attach is the escape hatch.** The "Native panes" toggle
  switches between them and is not persisted between runs. Keep the plain path working — it is one
  `if` in `SessionDetail`, it costs nothing, and it is the fallback for anything control mode
  renders badly.
- **The window tab bar switches windows and nothing else.** No new/close/rename affordance and no
  ⌘1–⌘9: tmux windows in a moomux session are made by the user's own workflow, and the bar exists to
  see and reach them. It is hidden entirely at one window, and it does not scroll or overflow — a
  segmented picker squeezes, and a session with enough windows to need scrolling is not a session
  this app is the right front end for. Pane views are also not cached per window: leaving a window
  dismantles its `PaneView`s and returning repaints them from `capture-pane`, which is the same
  first-paint path an attach uses and costs one round trip.
- **Scrollback is restored once, on a pane's first paint, and never refreshed.** `capture-pane -S
  -1000` feeds history plus the visible screen into SwiftTerm's own scroll buffer; later repaints
  (every `sizeChanged`, i.e. every frame of a window drag) are visible-only, because re-streaming
  ~110KB per pane per frame is the cost and tmux's rewrapped history would have to be diffed
  against ours anyway. So after a resize the restored history keeps its **pre-resize wrapping**.
  There is no lazy paging past 1000 lines — the pane is a monitor, not an archive, and both the
  plain attach and the user's own terminal have full copy-mode. A pane on the alternate screen
  (vim, less) gets no history, because neither tmux nor SwiftTerm keeps any for it.
- **No visible scrollbar in control-mode panes**, and there cannot be one: `NoScrollerTerminalView`
  has to keep the scroller hidden for the column-width reason above. Wheel and trackpad scrolling
  are unaffected — `MacTerminalView.scrollWheel` calls `scrollUp`/`scrollDown` directly and never
  goes through the `NSScroller`.
- **No mouse reporting or drag-to-resize panes in control mode.** Clicking selects a pane; that is
  all. Resizing goes through tmux's own keys.
- **No terminal settings** — font, size, colours and cursor are all SwiftTerm's defaults.
- **The write paths stop at the session row.** Create, rename, tags, archive, move, kill tmux and
  delete are there; `SetSessionAgent`, project CRUD
  (`AddProject`/`UpdateProject`/`RemoveProject`/`MoveProject`) and the settings toggles
  (`SetTheme`/`SetAutoTmux`/…) are not. All of them already exist on the socket — this is appetite,
  not a missing boundary. **No drag-to-reorder**, either: `MoveSession` takes a ±1 delta, the sidebar
  is a sectioned `List`, and `onMove` wants index sets over a bound array, so ⌃⌘↑/↓ is the whole
  feature for a day less work.
- **The New Session sheet asks three questions: project, name, first prompt.** The TUI's dialog asks
  thirteen. Agent, model, thinking level, base branch, "resume this existing branch",
  ticket/PR and auto-submit are all left to the core, which defaults them from
  the project's config — and the picker-shaped ones cannot be offered without a second copy of
  `internal/tui`'s `agentNames` / `thinkingNamesFor` / `modelNamesFor` tables living on this side of
  the socket, to drift silently the next time Go gains an agent. **The rule for growing this form is
  that the core must be able to hand over the list it validates against** (there is no IPC method for
  it today); a field the app can fill from `Config` or from free text is fair game, one that needs a
  hard-coded table is not. Ticket and PR are a Tags sheet away a second later. `open_terminal` is
  deliberately unset, so a new session lands in the sidebar rather than in iTerm.
  **`dangerous` is the exception and the app has to send it**: `App.CreateSession` takes it as a
  plain `bool` and hands it straight to `buildAgentCmd`, with no fallback to `proj.Dangerous` — the
  defaulting lives in `internal/tui/update.go`, not in the core. Leaving it unset created sessions in
  a `dangerous = true` project *without* `--dangerously-skip-permissions`, so the same project's
  agents behaved differently depending on which front end made them. `AppState.create` reads it off
  `Config` before the call. Anything else added here needs the same check: "the core defaults it" is
  true of most fields and not of all of them.
- **A slow write reports in the toolbar, not in its sheet.** `AppState.busy` is set by `mutate` for
  every action and rendered by `ConnectionBadge`, so the New Session sheet closes on Create rather
  than sitting there for the tens of seconds a worktree plus the worktree-create userscripts take.
  A refusal still lands in the "Couldn't do that" alert. `StartFirstPrompt` failing is folded into
  the *hint* rather than the error, because by then the worktree and the tmux session already exist
  — reporting it as a failed creation invites a retry that answers "session already exists".
- **No app icon**, so the bundle shows the generic one. `~/tmp/mergeright/Scripts/make-icon.swift`
  draws one from code when it's wanted.
- **No `dist`/`notarize`, no Sparkle, no signing identity.** Ad-hoc signing is fine until something
  depends on a stable designated requirement — launch-at-login, which is still unproven.
  **Notification authorization is not one of those things**: measured with a throwaway bundle of
  exactly `make app`'s shape (hand-assembled, `codesign --force --sign -`), the prompt appears
  normally, `add` succeeds, and the grant survives a rebuild that changes the CDHash and a move
  between `~/Applications` and `.build/`. It is keyed by bundle identifier, not by signature.
- **Config is re-fetched on every 2s poll** rather than only after a change. One extra socket
  round trip, and it keeps project order and emoji fresh with no invalidation logic.
- **Review happens in a tmux window, not in a patch viewer.** "Review Changes" runs
  `new-window -n review` in the session with `git diff --merge-base <base>` plus a
  `git status --short --branch`, and leaves a shell behind (`AppState.reviewScript`). Reviewing
  twice reuses that window (`respawn-window -k`, then `select-window`; `new-window` only when there
  is nothing to reuse) — two tabs both called "review" with nothing to tell them apart is worse than
  either. `respawn-window` and not kill-then-create, because killing the last window of a session
  kills the session. A native
  viewer was designed and rejected: ~400 lines across two languages — a new `gitwt.Diff`, an
  `App.Diff`, a `Patch` field on `ipc.Result`, a `Server` hook, a `main.go` line and a two-pane
  SwiftUI sheet — to end up a *worse* pager than the shell pane this app already renders natively,
  with no colour config, no word diff and no `delta`. Going through tmux costs one `Process` call,
  and the tab bar is what makes it readable. The tradeoffs it keeps: no diff without a live tmux
  session (`canReview`), the base branch comes from the *project*, not the session, so a session
  created with an explicit `-base` diffs against the project default, and untracked files show as a
  `git status` listing rather than as patches — `git add -N` would get them into the diff and is not
  worth mutating a live worktree's index for.
- **The session grid is snapshots, not live views, and that is the feature.** ⌘⇧G swaps the detail
  column for a tile per live session, each one a `tmux capture-pane` fed into a read-only
  `NoScrollerTerminalView` every five seconds. A grid of *attached* clients — the obvious reading of
  "several sessions at once" — would be **destructive**: every tmux client on a session sets the
  shared window size (see the bullet above; measured for plain attach, `-CC` and grouped sessions
  alike), so six live tiles would letterbox six real sessions someone else is working in until the
  grid closed. `capture-pane` attaches nothing. What that costs is one short-lived tmux process per
  visible tile per tick, which is why the interval is 5s rather than the store's 2s and why the task
  belongs to the tile, so closing the grid stops it; the upgrade if it ever bites is one invocation
  with `;`-joined captures, never a client per tile. What it gives up: typing into a tile (the thing
  the size problem makes unaffordable anyway — click one, then Attach), multi-pane tiles (the
  session's active pane is the agent), zoom, and scrollback. `TmuxSnapshot.screen` **truncates** each
  captured row to the tile's column count rather than letting it wrap: an agent pane is 150-210
  columns and a tile is nearer 50, so wrapping shows the bottom quarter of the last few lines as
  mush. It also drops the trailing blank rows `capture-pane` returns below the cursor, which would
  otherwise scroll the content out of a short tile.
- **No notification actions, and no "Approve" button.** Tapping a banner selects the session and
  brings the app forward; that is the whole interaction. An action button would have to send keys
  into the agent's pane, and there is no write path for that. A banner already *is* an Open button.
- **SwiftUI views have no coverage** and cannot have any. Screenshots are the check.
