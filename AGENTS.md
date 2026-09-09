# moomux

A TUI (Bubble Tea) for managing Claude Code / codex / opencode sessions across git worktrees. See README.md for what it does and how to build/run it.

## UI changes

This is a terminal UI — you can't see it render just by reading the Go source. After any change to `internal/tui/` (new fields, layout tweaks, new modes, copy changes, etc.), capture a screenshot of the affected screen(s) and look at it before considering the change done:

```bash
./scripts/screenshot.sh <screen> /tmp/<screen>.png
```

`<screen>` is one of the scenarios `cmd/uishot` knows about — a sample, not the full list (it grows over time): `list`, `new-session`, `new-project`, `tag`, `confirm-delete`, `confirm-delete-project`, `all-sessions`, `edit-project-emoji`, `project-picker`. Run `go run ./cmd/uishot -screen=x` to see the current full list. It renders the real `tui.Model` against a fake backend with canned sample data, so no real projects, git repos, or tmux sessions are needed.

If a change adds a new mode or scenario that isn't covered, add it to the `screens` map in `cmd/uishot/main.go` (drive it there with the same key-press sequence a user would use) rather than skipping the screenshot.

This app is used over mobile/remote SSH clients as narrow as ~40-60 columns (see `narrowWidthBreak` in `internal/tui/view.go`), and that's a real source of bugs: overlay footers, headers, and any hint text that isn't wrapped through `overlayWidth`/a width-aware fallback (see `formFooter`/`helpFooter` for the pattern) will hard-clip mid-word instead of degrading gracefully. For any change touching an overlay, header, or footer, screenshot it at both the default width and a narrow one (`./scripts/screenshot.sh <screen> /tmp/<screen>-narrow.png 60 24`, and a tighter one like `40 20` if there's any doubt) and confirm nothing is truncated or overflowing before considering the change done.

Two of those checks are automated now, over *every* scenario in the `screens` map at both 40x20 and 100x32, in `TestScreens` (`cmd/uishot/main_test.go`): nothing may render wider or taller than its terminal, and the plain-text layout must match its golden file in `cmd/uishot/testdata/`. So a reflow or a clipped footer fails CI instead of waiting for someone to re-render a PNG. When a layout change is intended, re-run with:

```bash
go test ./cmd/uishot -update
```

`TestScreens` pins the working directory to `/`, `HOME` to `/home/moo`, and `XDG_CONFIG_HOME` to `/home/moo/.config` so the forms that prefill or expand a path — and the settings screen, which renders the front end's own `client.toml` values — look the same on every machine. A new scenario that shows some other host-specific value (a hostname, a real timestamp) needs the same treatment, or it will pass locally and fail in CI.

The same trap applies outside the goldens: `tui.New` reads `config.Client` at construction, so `internal/tui`'s `TestMain` points `XDG_CONFIG_HOME` at an empty scratch dir. Without it the suite passes or fails depending on what the developer has configured, and a test that saves would overwrite their real settings. Anything else the TUI reads from the user's home needs to be pinned there too.

Read the resulting testdata diff as part of your own review — it is the cheapest full-surface look at what a change did. The screenshots are still required: golden files strip styling, so colour, theming and emoji only show up in a PNG.

Send the resulting PNG(s) to the user so they can see the change, the same way you'd report a code diff — surface it as a clickable `file://` link (e.g. `[all-sessions.png](file:///tmp/all-sessions.png)`) rather than just viewing it inline, since inline rendering isn't guaranteed to reach the user.

Forms like the new-session dialog track focus as a plain int (`newFormFocus`) and key several independent switches off it — tab-cycling, typing/left-right handling, and focus-in in `internal/tui/update.go`, but also `focusedOverlayLine` in `internal/tui/view.go` (which decides where the overlay viewport scrolls to keep the focused field visible). When adding, removing, or reordering a field, `grep -rn "newFormFocus" internal/tui/*.go` and update every hit, not just the ones in the file you're already touching — a stale `default:` case doesn't fail to compile, it just silently scrolls to the wrong field at runtime, which reads to a user as the whole dialog "resizing" or jumping.

## The macOS app

Lives in its own repo now: [afitzgerald/moomux-mac](https://github.com/afitzgerald/moomux-mac)
(private). It's a SwiftUI app that drives this same core over the `moomux serve` unix socket
(`internal/ipc`); its working notes live in that repo's `CLAUDE.md`.

`internal/ipc` is a contract with a second client now, in a separate repo with no CI link back to
this one. Changing a method's shape, or `session.Session`'s JSON tags, breaks the Swift side
silently — it decodes into optionals, with no automated cross-repo check. Check
`moomux-mac`'s `Sources/Moomux/Core/Models.swift` by hand when you touch either. Likewise, the Mac
app attaches sessions with `tmux -CC` (control mode) and parses tmux's line protocol, so it depends
on the *names* moomux gives tmux sessions — renaming or restructuring `internal/tmux`'s session
naming is a change the Swift side feels, invisibly.

The color palettes are served now too (`config.Themes()`, the `Themes` IPC method) — agent-state
colors, the theme list, and a `system` name per color so a native front end can use `.accentColor`
et al. rather than frozen hex. The Swift side still has its own `enum Theme` and
`SettingsSheet.themes`; deleting those in favour of the served table is the open follow-up.

**The protocol is documented in [docs/wire-protocol.md](docs/wire-protocol.md)** —
what crosses the socket, why it's shaped that way, and the rules for adding to
it. Read that before changing anything in `internal/ipc` or
`internal/sessionview`.

## Derived session state belongs in the core, not in each front end

`internal/sessionview` is the answer to "what does a client render". It runs the raw agent
watchers, joins their path-keyed output with tmux liveness (that join is what "parked" *is*),
and maintains everything that costs a subprocess — git status, `gh pr view`, agent-log prompt
recovery — then streams finished `View`s keyed by session id. It also keeps the tmux window
titles in step with agent state, which is core upkeep and used to be driven by whichever front
end happened to be attached.

The rule: if a front end would have to *compute* something to draw it, the core computes it and
serves it. That covers the effective state, its label and quip, the first prompt, dirty/unpushed,
PR status, and the display order (`Snapshot.Sessions` arrives already sorted — the live-first
tiebreak used to be per-client, so two front ends could list a project in different orders).
Clients filter and render; they do not re-derive. The same `Watcher` feeds the local TUI and `moomux serve`, so there is exactly one
implementation, and `internal/ipc` serializes `sessionview.Snapshot` as-is rather than reshaping
it — a wire type in the middle is where the last drift came from.

`watcher.State` serializes by *name* (`MarshalJSON` in `internal/watcher`), not as its iota. The
enum is deliberately ranked (NeedsInput above Working), so the numbers exist to be reordered — as
bare ints a re-rank would silently reassign every state the Swift side shows, with no compile
error on either side. The names match `config.Themes`'s per-state colour keys, so a client gets
one vocabulary.

The exception is not derived state at all: settings that describe *the machine someone is
sitting at* rather than the sessions being orchestrated. Those live in `config.Client`
(`client.toml`), loaded and saved by the front end, and the core has no method for them —
`internal/app` and `internal/ipc` should never mention the type. Today that's the TUI's
diff tool (`diff_tool`, the `D` shortcut; `internal/tui/difftool.go`), which launches a GUI
window on the viewer's own screen. Anything else that opens a window there, reads their
clipboard, or shells out to something only they can see belongs on the same side. Adding it
to `config.Config` with a `Set*` method instead gives you a setting that configures one host
and runs on another. `docs/wire-protocol.md` has the longer version, including why the
terminal opener lives in the core and isn't the precedent it looks like.

What stays a pull: `Sessions`, `ChangeSummary`, and the on-demand `WorktreeStatus` the delete
dialog uses (it wants a freshly checked answer before a destructive action, and unlike
`ChangeSummary` it also refreshes the remote ref). Everything routine rides the stream.

## Releases and commit messages

Every merge to `main` auto-tags and deploys a new version (`.github/workflows/deploy.yml` computes the next tag via `scripts/next_version.sh`, then `release.yml` builds and publishes it) — there's no manual release step, and no way to land a commit on `main` without it shipping.

The version bump is inferred from commit subjects since the last tag: an explicit breaking-change marker (`type!:` or a `BREAKING CHANGE` footer) bumps major; everything else (`feat:`, `fix:`, `chore:`, ...) is a patch bump. `feat:` used to bump minor, but it's the de facto default prefix for nearly every commit in this repo, so that rule fired on almost every release and minor-bumped constantly (0.2.x -> 0.3.0 -> 0.4.0 -> 0.5.0 within days) — see `scripts/next_version.sh` and `scripts/next_version_test.sh`. Don't reintroduce a "feat: -> minor" rule; a minor bump is a manual tag now, not something to infer from commit type.

## Bug fixes and logic changes

Every bug fix or non-trivial logic change needs a test that fails without the fix and passes with it — check this by temporarily reverting the fix and confirming the test goes red before restoring it. Add the test in the same commit/PR as the fix, not as a follow-up. Skip only for pure UI/copy tweaks (covered by the screenshot rule above) or one-line changes with no meaningful branch/edge case.

## What CI enforces

`.github/workflows/test.yml` gates merges on two jobs. `test` runs on both
ubuntu and macOS — the app shells out to `tmux` and `git` and is used mostly
from macOS, and Linux-only CI already hid a real bug (tmux reports a pane cwd
with symlinks resolved, so every macOS worktree under a symlinked path looked
like a cwd mismatch and got its live session killed on open; see `samePath` in
`internal/app/app.go`). It builds, vets, and runs the unit and e2e suites with
`-race -shuffle=on`. `lint` runs `gofmt`, `staticcheck`, `govulncheck`, and
`scripts/next_version_test.sh` — the release-versioning rules that ship every
merge to `main`.

### `./...` does not mean everything

The e2e suite is behind `//go:build e2e`, so `go build ./...`, `go vet ./...`,
`go test ./...` and `staticcheck ./...` all skip `e2e/` **silently** — no
error, no "0 tests", nothing. It is a separate CI step for that reason
(`go test -tags e2e ./e2e/...`), and it is the only suite that drives the real
`App` against the real `git` and `tmux` binaries.

That gap has already shipped a red PR: a change to `App.CreateSession`'s and
`App.AddProject`'s signatures passed every local `./...` command and then
failed both `test` jobs on a build error in `e2e/`. So verifying a change means
both suites, not one:

```bash
go test ./... -race -shuffle=on -count=1
go test -tags e2e ./e2e/... -race -shuffle=on -count=1
```

Anything touching an exported signature on `App` needs the second one before
you believe the first.

Two version pins to be aware of: `staticcheck` and `govulncheck` are run via
`go run tool@version`, and their newer releases require a newer Go toolchain
than `go.mod` asks for, so the `lint` job installs `go-version: stable` rather
than `go.mod`'s version. Bump the tool pins there, not in `go.mod`.
