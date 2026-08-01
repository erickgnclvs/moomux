# CodeRabbit full-repo review findings

Generated from `coderabbit review --agent --base-commit 810bee4` (full repo vs. root commit) on 2026-08-01.
54 findings total: 1 critical, 34 major, 19 minor. Of those, 31 are in real code and itemized below
(18 major, 13 minor); the remaining 23 — including the 1 critical — are inside
`docs/superpowers/plans/*.md` and `docs/superpowers/specs/*.md`, reviewing example code in
future-design docs, not live code, and are tracked at the bottom for reference only rather than
broken out by severity. Four of those informational findings turned out to also describe real,
still-open defects in the current implementation under a different description — those are fixed
and itemized in that section too.

## Majors — real code (fixing one per commit)

All 18 fixed, each in its own commit with a regression test.

- [x] `internal/terminal/remote.go:27-30` — tmux session target needs exact match, not prefix match
- [x] `internal/browser/browser.go:12-21` — `Open()` builds `exec.Command` from unvalidated input; restrict to http/https URLs before exec (security)
- [x] `scripts/screenshot.sh:44-86` — no test coverage for the screenshot pipeline
- [x] `.github/workflows/deploy.yml:24-51` — missing concurrency group, can race concurrent deploys
- [x] `.github/workflows/release.yml:24-27` — `workflow_dispatch` `inputs.tag` not validated before use (security)
- [x] `.github/workflows/release.yml:24-40` — actions pinned by tag, not SHA (supply-chain risk)
- [x] `scripts/next_version.sh:13-24` — release-tag detection should only consider exact `vX.Y.Z` tags reachable from HEAD
- [x] `scripts/next_version.sh:20-35` — breaking-change detection should read commit body, not just subject
- [x] `internal/session/session.go:86-100` — `Store.save` reuses a fixed `.tmp` path, concurrent-write clobber risk
- [x] `internal/session/session.go:186-197` — `Reorder` mutates against a stale map instead of the freshly reloaded one
- [x] `internal/config/config.go:132-148` — same fixed-`.tmp`-path issue as session.go
- [x] `internal/watcher/sqlite.go:48` — context not propagated into the `sqlite3` subprocess call
- [x] `internal/prompt/prompt.go:66-69` — same missing context-threading through sqlite3 calls (fixed as a fixed per-query timeout, not a threaded context — see commit)
- [x] `internal/gitwt/gitwt.go:66-74` — `execRunner.Run` uses `exec.Command` not `exec.CommandContext`
- [x] `internal/gitwt/gitwt.go:150-175` — `RemoveWorktree` treats any stat error as "already removed", should check `os.IsNotExist` specifically
- [x] `internal/tmux/tmux.go:14-19` — same missing `CommandContext`/timeout issue
- [x] `internal/tmux/tmux.go:131-136` — `SendKeys` needs exact-match session target
- [x] `internal/tui/form.go:115-121` — `renderFormHint` missing `MaxHeight` enforcement

## Minors — real code

- [x] `README.md:15` — docs claim `claude` is always required; should reflect per-agent executable
- [x] `internal/terminal/window.go:126-131` — opener hint plumbing
- [x] `internal/terminal/terminal_test.go:122-175` — tests need updating alongside window.go hint change
- [x] `internal/browser/browser.go:44-47` — OSC 52 clipboard write bypasses Bubble Tea's synchronized-output path — evaluated, accepted as-is: closing the remaining race requires `tea.Program.ReleaseTerminal`/`RestoreTerminal`, which exits raw mode and redraws (visible flicker on every link copy). The write is already synchronous in `Update()` rather than a `tea.Cmd` (see `internal/tui/update.go:264-268`), and OSC 52 payloads here are well under typical tty write-atomicity limits, so real corruption is unlikely. Not worth the flicker for a low-probability, low-severity residual risk.
- [x] `internal/config/config.go:161-170` — `ExpandHome` over-matches `~`-prefixed strings that aren't home-relative
- [x] `internal/tui/update.go:32-55` — flash-message helper reuse
- [x] `internal/tui/view.go:311-315` — missing `MaxWidth` alongside existing `MaxHeight`
- [x] `internal/tui/view.go:390-404` — tab overflow in wide-mode header
- [x] `internal/app/app.go:67-77` — doc comment inaccurate about port allocation start
- [x] `internal/app/app.go:271-275` — empty hint returned on error instead of existing hint
- [x] `internal/app/app.go:527-529` — agent-name resolution inconsistency with validateProject
- [x] `internal/app/app_test.go:1029-1059` — test fixtures need exact tmux pane match — already using `=moomux-feat` exact-match syntax; no change needed
- [x] `main.go:107-137` — `config.Save` errors silently discarded in setup flow
- [x] `main.go:143-169` — same silent-discard issue in `promptAutoTmux`

## Informational only — planning docs, not live code

Findings in `docs/superpowers/plans/2026-05-19-curral-implementation.md`,
`docs/superpowers/plans/2026-05-21-multi-agent-support.md`,
`docs/superpowers/plans/2026-05-19-cross-terminal-support.md`, and matching specs describe future
design, not current behavior. Worth revisiting if/when that work is implemented; not queued here —
except the four below, which turned out to describe defects the real implementation still has,
just under a different description than any of the majors/minors above.

- [x] `internal/tmux/tmux.go:69-79` (ex "Distinguish missing sessions from other tmux failures") —
  `HasSession` treated every exit-1 tmux failure as "session absent", swallowing permission/protocol
  errors too; now only "can't find session"/"error connecting to" (no server yet) count as absent.
- [x] `internal/app/app.go:270-283` (ex "Clean up partial resources when session creation fails") —
  a `Tmux.NewSession` failure after `git worktree add` succeeded left the worktree orphaned; now
  removed on that failure path. (The later `Store.Put` failure is deliberately left as-is — the
  live tmux session it left behind is a usable resource via the manual-attach hint, not an orphan.)
- [x] `internal/terminal/terminalapp.go` (ex "Implement a real Terminal.app opener") —
  Terminal.app opened via `open -a Terminal` with no way to pass a startup command, so it never
  auto-attached; now uses AppleScript's `do script`, same mechanism as the iTerm2 opener.
- [x] `internal/app/app.go:71-91` (ex "Make OpenCode port allocation actually unique") —
  `nextOpenCodePort` only reasoned about ports recorded in the session store, never checking whether
  the OS actually had the candidate port free; now binds-and-releases to confirm before returning it.
