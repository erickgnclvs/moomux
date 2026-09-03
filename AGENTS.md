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

Send the resulting PNG(s) to the user so they can see the change, the same way you'd report a code diff — surface it as a clickable `file://` link (e.g. `[all-sessions.png](file:///tmp/all-sessions.png)`) rather than just viewing it inline, since inline rendering isn't guaranteed to reach the user.

Forms like the new-session dialog track focus as a plain int (`newFormFocus`) and key several independent switches off it — tab-cycling, typing/left-right handling, and focus-in in `internal/tui/update.go`, but also `focusedOverlayLine` in `internal/tui/view.go` (which decides where the overlay viewport scrolls to keep the focused field visible). When adding, removing, or reordering a field, `grep -rn "newFormFocus" internal/tui/*.go` and update every hit, not just the ones in the file you're already touching — a stale `default:` case doesn't fail to compile, it just silently scrolls to the wrong field at runtime, which reads to a user as the whole dialog "resizing" or jumping.

## The macOS app

`macos/` is a separate SwiftUI app that drives this same core over the `moomux serve` unix socket
(`internal/ipc`). It has its own working notes in `macos/CLAUDE.md` — read those before touching
anything in there; none of the Go rules above apply to it.

**There is no Xcode on this machine** (`xcode-select -p` → `/Library/Developer/CommandLineTools`),
so a Mac app here is a **SwiftPM executable plus a Makefile that assembles and signs the `.app`
bundle by hand — never an `.xcodeproj`**. `xcodebuild` doesn't exist, `#Preview` doesn't compile
(its macro plugin ships with Xcode), and neither XCTest nor swift-testing is importable, so the
test harness is `assert`-based `demo()` functions run through the real binary via `--selftest`.
Take "scaffold the Xcode project" as "scaffold the Mac app" and build it this way; don't go looking
for a project file that cannot exist. `macos/` and `~/tmp/mergeright` are both worked examples.

Two rules reach back across the boundary:

- `internal/ipc` is a contract with a second client now. Changing a method's shape, or
  `session.Session`'s JSON tags, breaks the Swift side silently — it decodes into optionals. Grep
  `macos/Sources/Moomux/Core/Models.swift` when you touch either.
- The Mac app attaches sessions with `tmux -CC` (control mode) and parses tmux's line protocol, so
  it depends on the *names* moomux gives tmux sessions and on nothing else about how the TUI drives
  tmux. Renaming or restructuring `internal/tmux`'s session naming is a change the Swift side feels.

## Releases and commit messages

Every merge to `main` auto-tags and deploys a new version (`.github/workflows/deploy.yml` computes the next tag via `scripts/next_version.sh`, then `release.yml` builds and publishes it) — there's no manual release step, and no way to land a commit on `main` without it shipping.

The version bump is inferred from commit subjects since the last tag: an explicit breaking-change marker (`type!:` or a `BREAKING CHANGE` footer) bumps major; everything else (`feat:`, `fix:`, `chore:`, ...) is a patch bump. `feat:` used to bump minor, but it's the de facto default prefix for nearly every commit in this repo, so that rule fired on almost every release and minor-bumped constantly (0.2.x -> 0.3.0 -> 0.4.0 -> 0.5.0 within days) — see `scripts/next_version.sh` and `scripts/next_version_test.sh`. Don't reintroduce a "feat: -> minor" rule; a minor bump is a manual tag now, not something to infer from commit type.

## Bug fixes and logic changes

Every bug fix or non-trivial logic change needs a test that fails without the fix and passes with it — check this by temporarily reverting the fix and confirming the test goes red before restoring it. Add the test in the same commit/PR as the fix, not as a follow-up. Skip only for pure UI/copy tweaks (covered by the screenshot rule above) or one-line changes with no meaningful branch/edge case.
