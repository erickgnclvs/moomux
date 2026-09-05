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

`TestScreens` pins the working directory to `/` and `HOME` to `/home/moo` so the forms that prefill or expand a path render the same on every machine — a new scenario that shows some other host-specific value (a hostname, a real timestamp) needs the same treatment, or it will pass locally and fail in CI.

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

Two version pins to be aware of: `staticcheck` and `govulncheck` are run via
`go run tool@version`, and their newer releases require a newer Go toolchain
than `go.mod` asks for, so the `lint` job installs `go-version: stable` rather
than `go.mod`'s version. Bump the tool pins there, not in `go.mod`.
