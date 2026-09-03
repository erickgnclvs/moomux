package tui

import (
	"testing"
	"time"
)

// TestFetchGitStatusCmdRunsConcurrently guards against fetchGitStatusCmd
// checking each session's git status one at a time: over a socket backend
// (moomux ui -socket, or a native front end), each WorktreeStatus call is
// its own dial+encode+decode round trip on top of the underlying `git`
// shell-out, so a sequential loop makes a stale-session sweep's latency
// scale with session count instead of with the slowest single call.
func TestFetchGitStatusCmdRunsConcurrently(t *testing.T) {
	be := &fakeBackend{worktreeStatusDelay: 50 * time.Millisecond}
	ids := []string{"a", "b", "c", "d"}

	start := time.Now()
	msg := fetchGitStatusCmd(be, ids)()
	elapsed := time.Since(start)

	// Sequential would take ~4*50ms=200ms; concurrent should land close to
	// one delay. 150ms leaves generous headroom for a loaded CI box while
	// still failing hard on a sequential implementation.
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("fetchGitStatusCmd took %v for %d ids at 50ms each — looks sequential, not concurrent", elapsed, len(ids))
	}
	got, ok := msg.(GitStatusMsg)
	if !ok || len(got.Status) != len(ids) {
		t.Fatalf("msg = %#v, want a GitStatusMsg with %d entries", msg, len(ids))
	}
}

// TestFetchPRStatusCmdRunsConcurrently mirrors
// TestFetchGitStatusCmdRunsConcurrently for the PR-status fetch, whose
// `gh pr view` calls are network-bound and would compound the same way.
func TestFetchPRStatusCmdRunsConcurrently(t *testing.T) {
	be := &fakeBackend{prStatusDelay: 50 * time.Millisecond}
	ids := []string{"a", "b", "c", "d"}

	start := time.Now()
	msg := fetchPRStatusCmd(be, ids)()
	elapsed := time.Since(start)

	if elapsed >= 150*time.Millisecond {
		t.Fatalf("fetchPRStatusCmd took %v for %d ids at 50ms each — looks sequential, not concurrent", elapsed, len(ids))
	}
	got, ok := msg.(PRStatusMsg)
	if !ok || len(got.Status) != len(ids) {
		t.Fatalf("msg = %#v, want a PRStatusMsg with %d entries", msg, len(ids))
	}
}
