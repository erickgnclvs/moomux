# Moomux.app

The native macOS front end. SwiftUI, SwiftPM executable, one dependency (SwiftTerm), driving the
Go core over the `moomux serve` unix socket — see `docs/native-macos-rewrite.md` for why this shape
and not a rewrite. This file is for working on it; the Go side's `AGENTS.md` still governs
everything outside `macos/`.

Today it lists sessions, streams their live agent state, shows detail, attaches a session's tmux
inside the app — by default over tmux **control mode**, with each pane in its own native view, and
optionally as one plain `tmux attach` — and hands a session to the user's terminal. Every write
path beyond `OpenSession` is still missing.

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
unbundled binary, and `UNUserNotificationCenter.current()` **traps** without one. That bites the
moment notifications land.

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

**Type into the shell pane, never the agent pane.** Synthetic keystrokes land in a real session
someone is using; text typed at a `zsh` prompt is harmless and erasable, text typed into an agent's
prompt box is not. Select the shell pane first (`tmux select-pane -t $S.2`), type, check with
`capture-pane`, then clear the line with `C-u` and put the active pane back. Leave the session as
you found it.

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

**Count the characters before believing a wrap is wrong.** `capture-pane` returns *logical* lines,
which can be far longer than the pane, so a repaint legitimately wraps where tmux would. Several
rounds went into a one-column bug that did not exist; `python3 -c "print(len(line), repr(line[:56]))"`
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
  deriving a cell size from it inside `sizeChanged` is short by a column's worth and every pane
  comes out one column narrow. The container divided by tmux's own grid is the self-consistent
  measure, and its rounding error can only make a pane a hair *wide*, which is harmless.
- **SwiftTerm marks most overrides `public`, not `open`** — `keyDown` cannot be overridden from
  this module, `mouseDown` and `viewDidMoveToWindow` can.
- **`capture-pane` returns logical lines, not screen rows**, so a repainted pane wraps long lines
  itself rather than reproducing tmux's exact screen. That is not a bug and cost an hour to talk
  myself out of — count the characters before believing a wrap is wrong.
- **SwiftTerm marks its overrides `public`, not `open`.** `keyDown` and friends cannot be overridden
  from this module — the compiler says "overriding non-open instance method outside of its defining
  module". `viewDidMoveToWindow` is fine because it comes from `NSView`.
- **SwiftUI does not give the terminal first responder.** Focus stays on the sidebar list, so
  everything typed after "Attach" goes to the list instead. `updateNSView` is too early (no window
  yet); `AttachedTerminalView.viewDidMoveToWindow` is the hook that works.
- **`open` against an app that is still terminating does nothing at all**, which reads exactly like
  a crash on launch. `make run`/`make dev` wait out the old process for this reason — do not
  "simplify" that loop away.

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

- **Control mode is the default; the plain attach is the escape hatch.** The "Native panes" toggle
  switches between them and is not persisted between runs. Keep the plain path working — it is one
  `if` in `SessionDetail`, it costs nothing, and it is the fallback for anything control mode
  renders badly.
- **Control mode shows one window's panes, not tabs.** tmux windows exist in the protocol
  (`%window-add`, `%window-close`) and are not surfaced; switching windows inside tmux follows
  along, but there is no tab bar.
- **No scrollback in control-mode panes.** The initial paint is `capture-pane` of the visible
  screen only; tmux's copy-mode still works through the keyboard.
- **No mouse reporting or drag-to-resize panes in control mode.** Clicking selects a pane; that is
  all. Resizing goes through tmux's own keys.
- **No terminal settings** — font, size, colours and cursor are all SwiftTerm's defaults.
- **No write paths beyond `OpenSession`.** Create/rename/delete/tag/settings all exist on the socket
  already; the scaffold only proves one round trip.
- **No app icon**, so the bundle shows the generic one. `~/tmp/mergeright/Scripts/make-icon.swift`
  draws one from code when it's wanted.
- **No `dist`/`notarize`, no Sparkle, no signing identity.** Ad-hoc signing is fine until something
  depends on a stable designated requirement (launch-at-login, notification authorization).
- **Config is re-fetched on every 2s poll** rather than only after a change. One extra socket
  round trip, and it keeps project order and emoji fresh with no invalidation logic.
- **No notifications, no diff review, no multi-window grid.** The payoff features from the plan
  doc's §5, all downstream of the terminal pane.
- **SwiftUI views have no coverage** and cannot have any. Screenshots are the check.
