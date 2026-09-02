# Moomux.app

The native macOS front end. SwiftUI, SwiftPM executable, **zero external dependencies**, driving
the Go core over the `moomux serve` unix socket — see `docs/native-macos-rewrite.md` for why this
shape and not a rewrite. This file is for working on it; the Go side's `AGENTS.md` still governs
everything outside `macos/`.

Right now this is a scaffold: it lists sessions, streams their live agent state, shows detail, and
opens a session in the user's terminal. The terminal pane, tmux control mode, and every write path
beyond `OpenSession` are not built yet.

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

**Spike a dependency before adopting it** (there are none today, and `Package.swift` should stay
that way for as long as possible). A throwaway package settles in under a minute what the README
won't tell you — this is how `#Preview`-using libraries get caught.

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
App/AppState.swift       the single root store, poll loop, watch loop
App/MoomuxApp.swift      scenes: main window + MenuBarExtra
App/SelfTest.swift       --selftest
UI/RootView.swift        split view, rows, detail, menu-bar content
```

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
- **The app is not sandboxed**, which is the only reason `NSHomeDirectory()` finds the real socket.
  Sandboxing it means finding another way to the core.

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

- **No terminal pane, no tmux control mode.** That is the next big piece (SwiftTerm behind a
  protocol, `tmux -CC`), and the reason the plan picks substrate A.
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
