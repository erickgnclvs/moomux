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
4. **The bind error shown is the first one** `ListenTailnetRetry` reports
   (that is its existing contract). Ceiling: after a long retry the error can
   be old; the retry's backoff caps at a minute, so "try again in a moment"
   holds.
5. **Verification** is `AGENTS.md`'s, not just `go test`/`go vet`: both suites
   (`go test ./...` and `go test -tags e2e ./e2e/...`, `-race -shuffle=on`),
   `gofmt -l`, `staticcheck`, and `scripts/check_dep_age.sh` for the QR
   dependency.

## Tests

The contract's tests, verbatim where it gives them; every failing-`Pair`
test also asserts `enable` was never called.

## A0 result

_Filled in after the spike: library, quiet zone, how it was scanned._
