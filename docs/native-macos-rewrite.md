# moomux as a native macOS app — options and trade-offs

Planning doc. No code changes. Question: what would rewriting moomux as a
native macOS app actually look like, and how does the language/framework
choice change (a) how hard it is to reproduce what we already have and
(b) how fast we can add the things we want next.

---

## 1. What actually exists today

Non-test Go, by package:

| Area | LOC | Survives a native rewrite? |
|---|---:|---|
| `internal/tui` | 7,019 | **No** — 100% thrown away |
| `cmd/uishot` | 572 | **No** — screenshot harness for the TUI |
| `internal/terminal` | 810 | **No** — exists only to find/drive an external terminal emulator |
| `internal/tmux` + `tmuxconf` + `layout` | 722 | Maybe — depends on substrate (§2) |
| `internal/app` | 1,554 | Yes, with edits — orchestration core |
| `internal/gitwt` | 356 | Yes — `git` subprocess wrapper |
| `internal/watcher` | 370 | Yes, improvable — agent state polling |
| `internal/session` / `config` / `atomicfile` | 471 | Yes — JSON/TOML state store |
| `internal/claudehook` / `codexhook` | 746 | Yes — installs agent hooks/skills |
| `internal/prompt` / `prstatus` / `userscript` | 465 | Yes |
| `main.go` (CLI: `spawn`, `hook`, `kill`, `tag`, `reseed`) | 712 | Yes — and **still required** (§5) |
| **Total non-test** | **13,945** | ~**8,400** portable, ~**5,500** is UI/terminal plumbing |

Test code is another 15k lines, roughly 40% of it (`internal/tui`, `cmd/uishot`)
tied to the TUI and equally disposable.

The honest read: **moomux is a ~4k-line orchestrator wearing a 7k-line
terminal UI.** The domain logic is small, well-factored behind interfaces
(`tmux.Runner`, `gitwt.Runner`, `prstatus.Runner`, `watcher.Watcher`), and has
nothing macOS-hostile in it. The rewrite cost is almost entirely the UI plus
whatever new substrate you pick underneath it.

### What we'd be giving up

This deserves to be first, not a footnote:

- **Phone/remote use.** `narrowWidthBreak` at 40–60 columns, the OSC 52
  copy-mode fallback, the Moshi/mosh guide, the SSH detection — that whole
  seam exists because moomux is driven from a phone over SSH. A `.app` bundle
  cannot be attached to from a phone. Ever.
- **Linux and WSL.** `internal/terminal` has hand-written support for GNOME
  Terminal, Konsole, WezTerm, kitty remote control, Ghostty, Alacritty, foot,
  Tilix, xterm, Windows Terminal. All of it dies.
- **`brew install moomux` + `go install`.** Replaced by notarization, code
  signing, Sparkle or the App Store, and a paid Apple Developer account.
- **Terminal-agnosticism.** Today moomux is a guest in whatever terminal you
  already use. A native app decides for you.

If any of those matter, the answer is not "rewrite" but "add a second front
end" (§6).

---

## 2. The real decision is the substrate, not the language

Three genuinely different products hide under "native macOS app". Pick this
before picking a language — the language choice is downstream and much less
consequential.

### A. Native shell, tmux stays (control mode)

`tmux -CC attach` puts tmux in **control mode**: instead of drawing a screen it
emits a line protocol (`%output`, `%layout-change`, `%window-add`, …) that a
GUI parses and maps onto its own native windows/tabs/splits. This is exactly
how iTerm2's tmux integration works, and it's the only path that keeps every
property that makes moomux moomux.

- Sessions survive app quit, crash, and reboot-of-the-GUI.
- The same session is still `tmux attach`-able from a phone. **Your mobile
  workflow keeps working**, in parallel with the Mac app.
- `internal/tmux` mostly survives; you add a control-mode client (~800–1,500
  LOC — it's a stateful line protocol, not a weekend).
- You still need a terminal *renderer* in-app for `%output` bytes.

Cost: medium. Risk: medium (control-mode parsing is fiddly, and layout
sync between tmux and native splits is where iTerm2 has historically had bugs).
Payoff: highest — it's additive rather than a replacement.

**Correction, from building it:** control mode does *not* escape tmux's shared
window size, which an earlier draft of this doc assumed. A `-CC` client sets its
size with `refresh-client -C` and the window follows it, letterboxing every
other client, exactly as a plain `tmux attach` does. Grouped sessions
(`new-session -t`) don't help either — a group shares the windows themselves.
Both measured. What control mode actually buys is per-pane native views (real
selection, scrollback and copy per pane) and the app *knowing* the layout, which
is the substrate every native feature in §5 wants. It is worth doing for that,
not for sizing.

### B. App owns the PTYs, tmux is gone

Forkpty per pane, terminal emulation in-process, panes are native views.

- Simplest mental model, best perf, best copy/paste/scrollback/search.
- Deletes `internal/tmux`, `tmuxconf`, `layout`, `terminal` (~1,530 LOC) and
  the entire tmux config-nagging flow.
- **Kills persistence.** Quit the app (or crash it) and every agent dies
  mid-run. Today the agent survives everything except a reboot. For long
  autonomous runs this is a serious regression, and it's the reason Conductor
  and Crystal both feel fragile compared to a tmux setup.
- Kills the "attach from anywhere" story completely.
- You now own PTY lifecycle, signal/winsize handling, and shell integration.

Cost: medium-low to build, high to *get right*. Only defensible if paired
with a supervisor daemon that outlives the UI — at which point you've
rebuilt a worse tmux.

### C. No terminal at all — headless agents

Drive `claude -p --output-format stream-json` (or the Agent SDK) and render a
native chat + diff + review UI. This is the Conductor / Crystal / Nimbalyst
shape, and it's the fastest-growing category in this space.

- Biggest product upside: structured events instead of screen-scraping, so
  per-tool-call UI, inline diffs, cost/token display, permission prompts as
  real dialogs, and no `internal/watcher` polling heuristics at all.
- Biggest cost: **you no longer have Claude Code's TUI**, so you re-implement
  it, forever, chasing every upstream feature (plan mode, skills, `/commands`,
  MCP UI, thinking display, image paste). Codex and opencode each need their
  own adapter with a different protocol.
- Also loses interactive shell-in-a-pane, which is half of why the right pane
  exists.

Cost: highest, and it's a permanent treadmill against three moving upstreams.

**Verdict on substrate: A.** It's the only one that adds capability without
subtracting the two things (persistence, remote attach) that make the current
tool good. B and C are different products, not rewrites.

---

## 3. Terminal rendering — the one hard dependency

Any of A or B needs a VT renderer. Options as of now:

| Option | Fit | Notes |
|---|---|---|
| **libghostty** (`libghostty-spm` / GhosttyKit xcframework) | Best ceiling | Metal-rendered, best-in-class emulation, Swift bindings + a pure-Swift Metal renderer landed. **API explicitly still in flux — "use with caution in production".** You'd pin a version and eat breakage. |
| **SwiftTerm** | Best today | Mature AppKit `NSView` (`TerminalView`, `LocalProcessTerminalView`), shipping in Secure Shellfish, La Terminal, CodeEdit. CoreText-rendered, so heavy agent redraws are the perf question. Actively maintained. Lowest-risk choice. |
| **xterm.js in a WebView** | Only if you go web-UI | Battle-tested, worst perf, and it drags the whole UI into a WebView. What Crystal/Nimbalyst do. |

Recommendation: **start on SwiftTerm, keep libghostty as the swap-in.** Both
are `NSView`-shaped, so isolate them behind one protocol and the swap is a day.

---

## 4. Language / framework matrix

Assumes substrate A. "Reproduce" = getting back to today's feature parity.
"Extend" = velocity on the enhancements in §5.

| Stack | Reproduce | Extend | Terminal | Notes |
|---|---|---|---|---|
| **Swift + SwiftUI (+AppKit where needed)** | Medium | **High** | SwiftTerm / libghostty native | Best native feel; only stack where App Intents, Shortcuts, menu-bar extras, `UNNotification` actions, Quick Look, Spotlight are free. Rewrite *everything* (~4k LOC of Go logic re-expressed). SwiftUI still weak on `List` reorder/drag and text fields — expect AppKit escapes. |
| **Swift UI shell + existing Go core as a sidecar/daemon** | **Low** | High | same | Go binary keeps `app`/`gitwt`/`watcher`/`session`/`config`/hooks/CLI; Swift talks to it over a unix socket with JSON, or via c-archive + cgo. **Reuses ~8,400 tested LOC and keeps the CLI and TUI alive for free.** Cost is one IPC boundary. |
| **Tauri v2 (Rust + WebView)** | Medium | Medium | xterm.js only | ~5–10 MB bundles, 40–80 MB RAM, locked-down permission model, and a path to iOS. But you rewrite the core in Rust *and* the UI in web *and* accept xterm.js. Cross-platform is its pitch — and moomux already has cross-platform via the TUI. |
| **Electron (Crystal/Nimbalyst's choice)** | Medium | Medium | xterm.js | 120–200 MB, 150–400 MB RAM. Mature signing/auto-update. Fastest if the team lived in TS, which it doesn't. No reason to pick this over Tauri here. |
| **Go + Wails / Fyne / Gio** | Low | **Low** | none usable | Keeps all the Go, which is seductive — but there is no credible Go terminal widget, native macOS integration is thin-to-absent, and you end up with an app that looks like a port. This is the trap option. |
| **Flutter / KMP** | High | Low | none usable | Same terminal problem, plus non-native everything. No. |

Two observations that matter more than the table:

1. **The Go core is not the expensive part to keep — it's the expensive part
   to rewrite.** `internal/app` alone carries 2,580 lines of tests. Rewriting
   it in Swift or Rust means rewriting those too, for zero user-visible gain.
   Every option except the sidecar pays that bill.
2. **The terminal widget constrains the language, not the reverse.** Wanting a
   good terminal view means Swift (SwiftTerm/libghostty) or a WebView
   (xterm.js). That collapses the matrix to "Swift" vs "web UI", and for a
   Mac-only app aiming at native polish, Swift wins on every axis except
   familiarity.

---

## 5. What a native app unlocks — and what it breaks

### Unlocked (and cheap in Swift, expensive elsewhere)

- **Menu-bar extra** with a live count of sessions in `needs-input`. This is
  the single highest-value feature the TUI structurally cannot have.
- **Actionable notifications** — "session X needs input" with Approve/Open
  buttons, replacing the current webhook/terminal-bell story.
- **FSEvents instead of polling.** `internal/watcher` ticks a directory scan
  and shells out to sqlite; FSEvents on `~/.claude/sessions` makes state
  changes instant and near-free. Works in any stack, trivial in Swift.
- **Native diff review** before merge (the one thing Conductor genuinely does
  better than us) — `NSTextView`-based or via a diff view; plus stage/commit/PR
  without leaving the app.
- **Multi-window / grid of live sessions** — `internal/tui/multiview.go`
  already wants to be this and is fighting the terminal to do it.
- App Intents / Shortcuts / Spotlight for `spawn`, drag-and-drop a folder to
  add a project, Quick Look on changed files, Keychain for tokens.

### Broken, and needing deliberate work to unbreak

- **`moomux spawn` / `/kill` / `/tag` / `/reseed`.** These are invoked *by the
  agent, from inside its own shell*. A `.app` can't be shelled out to. You
  need a CLI shim that talks to the running app (unix socket or URL scheme) —
  and that shim is, essentially, today's binary. Another argument for the
  sidecar architecture.
- **Hooks.** `moomux hook` is registered in `~/.claude/settings.json` and runs
  as a subprocess. Same problem, same answer: keep the binary.
- **Userscripts.** Fine — but only if the app runs them with a real login
  shell environment. GUI apps don't inherit your shell `PATH`; this bites
  every Mac dev tool that shells out and would need explicit handling.
- **Distribution.** Notarization, signing, Sparkle, and losing `brew install`.

---

## 6. Recommendation

**Don't rewrite. Add a Mac front end on the existing core, over tmux control
mode.**

Status: **all three are done.** `internal/ipc` / `moomux serve` / `moomux ui -socket` carry the
boundary; `macos/` is a SwiftUI app on SwiftPM that lists sessions over the socket, streams live
agent state, attaches a session's tmux over **control mode** with each pane in its own native view
and a tab bar for its windows (plain `tmux attach` kept as a fallback), creates / renames / tags /
archives / reorders / deletes sessions, and carries every §5 payoff feature it is going to carry —
the menu-bar count, notifications, review and the session grid. See `macos/CLAUDE.md` for how to
work on it, and its "Deliberately not done" list for the shape the last three landed in and why.

What is left is not on this doc's list: **distribution** (no signing identity, no `dist`/`notarize`,
no icon — the gate on anyone else running it), the write paths past the session row
(`SetSessionAgent`, project CRUD, the settings toggles — all on the socket already, pure appetite),
and **FSEvents instead of the 2s poll**, the one §5 item nothing has claimed. The New Session sheet
asks three of the TUI's thirteen questions on purpose; growing it wants an IPC method that hands
over the lists the core validates against, which does not exist yet.

What the sizing table below got roughly right: the control-mode client landed near the bottom of
its 2–3 week estimate, because the protocol is small once you stop guessing at it and read
`man tmux`'s CONTROL MODE section. What it got wrong is where the time actually goes — not the
parser (a day, and it is pure and test-covered) but the four undocumented behaviours around it,
each of which presents as a rendering bug and none of which is in any documentation: the client
ignoring its pty size, the unsolicited `%begin` block on attach, views painted before they have a
frame being silently reset, and stale column counts from `getOptimalFrameSize`. Budget for that
class of thing, not for the protocol.

Next, in the order they are worth doing:

1. ~~**Windows as tabs.**~~ Done. Cheap as predicted, though the claim above that `%window-add`
   "already arrives and is parsed" was wrong — it was reaching `.unhandled`, and the discovery
   actually piggybacks on the `list-windows` call the layout code already made.
2. ~~**Scrollback.**~~ Done: a pane's first paint restores `capture-pane -S -1000` into SwiftTerm's
   own scroll buffer. Later repaints stay visible-only.
3. ~~**Then §5's payoff features**~~ Done, two of the three not as described: notifications as
   banners plus a dock badge; diff review as a `new-window -n review` rather than a native patch
   viewer; the multi-session grid as periodic read-only `capture-pane` snapshots rather than live
   views, because every tmux client on a session sets the shared window size.

Concretely:

1. **Extract a headless core.** Move the TUI's calls to `internal/app` behind a
   JSON-RPC-over-unix-socket server (`moomux serve`). ~300 LOC. The existing
   TUI becomes its first client; nothing user-visible changes. This is
   valuable on its own and is the only irreversible-ish step.
2. **Ship `Moomux.app`** in Swift: SwiftUI for lists/detail/forms, AppKit where
   SwiftUI fights back, SwiftTerm for panes, driving `tmux -CC` for session
   attach. Talks to the core over the socket. The CLI binary keeps serving
   `spawn`/`hook`/`tag` and the TUI keeps serving the phone.
3. **Then** add the native-only features (menu bar, notifications, diff
   review), which is the actual reason to do any of this.

This gets ~8,400 lines of tested logic and the entire remote/Linux/CLI surface
for free, keeps two front ends honest against one core, and makes the
"native rewrite" a UI project instead of a product rewrite.

If the goal is instead **"a Conductor competitor"** — headless agents, diff-first,
no terminal — then say so, because that's substrate C and a different tool that
happens to share a config file. Building it does not require rewriting moomux;
it requires deciding to stop maintaining moomux.

### Known gaps in the spike

One left for the real build:

- **The local TUI aliases `a.Cfg`.** `runProgram` hands the TUI the same
  `*config.Config` App mutates, which is how config edits reach its render
  loop — so its 23 direct `m.cfg.…` reads sit outside App's lock. Narrow
  (those reads are all on Bubble Tea's single event-loop goroutine) but real.
  Closing it means the TUI reading config through `Backend` instead of
  through a shared pointer, which a native client would do anyway.

Three others were fixed in the spike, and the shape of the fix is worth
carrying into a native client:

- **A failed call must not read as "empty".** `tui.Backend`'s
  `Sessions()`/`Projects()`/`TmuxAliveAll()` have no error return — they're
  called from rendering paths that couldn't use one — and `update.go` prunes
  `m.states` against whatever `Sessions()` returns, so a single nil wipes
  every agent state badge. The client caches the last good answer and returns
  that instead; connection health is reported through the snapshot stream,
  which the TUI already flashes once per distinct error.
- **The stream reconnects.** Disconnects emit an error-only snapshot and the
  client redials with backoff, so a `moomux serve` restart heals itself
  rather than leaving frozen state on screen looking live.
- **`app.App` owns a lock on `Cfg`.** `cfgMu` write-locks the mutators across
  their I/O (which also closes a TOCTOU between the duplicate-name check and
  the save) and read-locks the readers, which copy what they need rather than
  holding it across seconds of git/tmux work. Anything outside App reads
  config via `ConfigSnapshot()`, so the ipc server never holds a pointer into
  live state. This was already a latent race in the local TUI, since
  `CreateSession` and `WorktreeStatus` read `Cfg.Projects` from `tea.Cmd`
  goroutines while `AddProject` writes it from the Update loop.

### Rough sizing (substrate A + sidecar)

| Piece | Estimate |
|---|---|
| `moomux serve` + client refactor | 1 week |
| tmux control-mode client (Swift) | 2–3 weeks |
| SwiftTerm pane view + layout sync | 1–2 weeks |
| Session list / detail / forms / project picker in SwiftUI | 3–4 weeks |
| Settings, themes, help, tagging, archive | 1–2 weeks |
| Signing, notarization, updater | 1 week |
| **Parity** | **~9–13 weeks** solo |
| Menu bar + notifications + diff review | +3–4 weeks |

Full Swift rewrite (no sidecar) adds roughly 4–6 weeks to re-express and
re-test the core, and permanently forks the CLI.

---

## Sources

- [iTerm2 tmux integration](https://iterm2.com/documentation-tmux-integration.html) · [protocol notes](https://deepwiki.com/gnachman/iTerm2/5.2-tmux-integration)
- [SwiftTerm](https://github.com/migueldeicaza/SwiftTerm)
- [libghostty is coming](https://mitchellh.com/writing/libghostty-is-coming) · [libghostty-vt docs](https://libghostty.tip.ghostty.org/) · [awesome-libghostty](https://github.com/Uzaaft/awesome-libghostty)
- [Conductor vs Crystal (2026)](https://superset.sh/compare/conductor-vs-crystal) · [Conductor](https://conductor.build)
- [Tauri v2 vs Electron 2026](https://www.buildmvpfast.com/blog/tauri-v2-vs-electron-desktop-apps-2026)
- [Claude Code headless mode](https://www.buildthisnow.com/blog/guide/development/claude-code-headless-mode) · [Swift Agent SDK (community)](https://swiftpackageindex.com/kiliczsh/claude-agents-sdk-swift)
