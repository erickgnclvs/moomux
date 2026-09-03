# Moomux.app

The native macOS front end. SwiftUI, SwiftPM executable, one dependency (SwiftTerm), driving the
Go core over the `moomux serve` unix socket — see `docs/native-macos-rewrite.md` for why this shape
and not a rewrite. This file is for working on it; the Go side's `AGENTS.md` still governs
everything outside `macos/`.

Today it lists sessions, streams their live agent state, shows detail, banners and dock-badges the
ones that start waiting on you, attaches a session's tmux inside the app — by default over tmux
**control mode**, with each pane in its own native view, and optionally as one plain `tmux attach`
— and hands a session to the user's terminal. Every write path beyond `OpenSession` is still
missing.

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

Two things to know about that scratch home:

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
- **Control mode shows one window's panes, not tabs.** tmux windows exist in the protocol
  (`%window-add`, `%window-close`) and are not surfaced; switching windows inside tmux follows
  along, but there is no tab bar.
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
- **No write paths beyond `OpenSession`.** Create/rename/delete/tag/settings all exist on the socket
  already; the scaffold only proves one round trip.
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
- **No diff review, no multi-window grid.** The remaining payoff features from the plan doc's §5.
- **No notification actions, and no "Approve" button.** Tapping a banner selects the session and
  brings the app forward; that is the whole interaction. An action button would have to send keys
  into the agent's pane, and there is no write path for that. A banner already *is* an Open button.
- **SwiftUI views have no coverage** and cannot have any. Screenshots are the check.
