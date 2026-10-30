# iPhone pairing — core implementation plan

The cross-repo contract (names, wire keys, link format, user-facing copy) is
Part A of `moomux-mac`'s
[`docs/superpowers/plans/2026-10-30-iphone-pairing.md`](/Users/erickgoncalves/.local/share/moomux/worktrees/moomux-mac/thinking-least-ios-be/docs/superpowers/plans/2026-10-30-iphone-pairing.md),
arguing from its spec,
[`docs/superpowers/specs/2026-10-30-iphone-pairing-design.md`](/Users/erickgoncalves/.local/share/moomux/worktrees/moomux-mac/thinking-least-ios-be/docs/superpowers/specs/2026-10-30-iphone-pairing-design.md).
Values live there; this file links to them rather than copying. This file is
authoritative for how the core implements it: layout, order, tests.

## Layout

| File | What it gains |
|---|---|
| `internal/ipc/tailnet.go` | `TailnetSelf`, `tailnetSelf`, `tailscaleRunFn`, the Tailscale sentinel errors, `PairLink`; `Tailnet` (`Start`, `Pair`), `(*tailnetListener).Owner` |
| `internal/ipc/ipc.go` | `Pairing`, `Result.Pairing` |
| `internal/ipc/server.go` | `Server.Pair`, the unix-only check in `handle`, the `"Pair"` case |
| `internal/ipc/client.go` | `Client.Pair` |
| `internal/app/app.go` | `SetTailnetListen` |
| `pair.go` (package main) | `runPair`, `printPairing`, `renderQR` |
| `main.go` | `runServe` builds the `Tailnet`; `pair` registered; `printUsage` |
| `docs/wire-protocol.md`, `README.md` | `Pair`, unix-only; turning the listener on with `moomux pair` |

## Order

Contract tasks A0 → A3, one task per commit or more, committed as it lands so
the app session can build a scratch core from this worktree. A0 (QR spike) is
throwaway in the scratchpad; only its result lands, below and in the PR body.

## Where this plan fills in or departs from the contract's implementation

1. **Commit subjects** carry the repo's `feat:` prefix (see `AGENTS.md`,
   "Releases"); otherwise the contract's wording. No attribution trailer.
2. **Sentinels go out bare**, never wrapped, so the string that crosses the
   wire is exactly the contract's copy; `errors.Is` still holds server-side.
3. **The flag is saved last.** The contract's first draft saved it before the
   bind wait and staleness check, contradicting its own "a failed `Pair`
   saves nothing"; raised with `thinking-least-ios-be`, who moved `enable()`
   to after both (contract A2, spec steps 3–5). A listener that binds late,
   after a timed-out `Pair`, runs unsaved until the next successful `Pair`
   saves and announces it; a restart before then just means it isn't
   listening, the safe direction.
4. **The bind error shown is the latest** `ListenTailnetRetry` reports, and
   `Pair` wakes a retry asleep in its backoff before it waits. This plan first
   kept the retry's old contract, the first error only; final review moved it,
   because the user's own machine (flag on, Tailscale stopped at login) would
   have shown "signal: killed" for up to a minute after Tailscale came back.
   Only the log stays first-only.
5. **Verification** is `AGENTS.md`'s, not just `go test`/`go vet`: both suites
   (`go test ./...` and `go test -tags e2e ./e2e/...`, `-race -shuffle=on`),
   `gofmt -l`, `staticcheck`, and `scripts/check_dep_age.sh` for the QR
   dependency.

## Tests

The contract's tests, verbatim where it gives them; every failing-`Pair`
test also asserts `enable` was never called.

## A0 result

rsc.io/qr v0.2.0, error correction M, quiet zone 2 modules,
black-on-bright-white (`\x1b[30;107m`). Checked by (a) decoding the rendered
half-block text back to the link with CoreImage's CIDetector, including a
`%27`/`%20`-escaped name, and (b) a real iPhone Camera reading it in a dark
terminal theme ("No usable data found" — the code read, no app registered
for moomux:// yet); (c) the real-tailnet `moomux pair` output decoded to the exact link, whose
machine name needed `%20`, `%28` and `%29`.
Light theme not separately scanned: colours are explicit, so the code itself
is identical.
