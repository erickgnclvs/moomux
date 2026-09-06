package tui

import (
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
)

// Regression for: a session's liveness changes (a new session comes up, or
// a killed one goes down) but the visible list keeps its old live-float
// order until something unrelated happens to call refreshSessions() again —
// at which point the session appears to jump into the middle of the list
// out of nowhere. The snapshot stream is the source of truth for liveness,
// so applying one must re-sort m.sessions itself rather than leaving the
// list to catch up later.
func TestStatusTickResortsSessions(t *testing.T) {
	be := &fakeBackend{
		sessions: []session.Session{
			{ID: "a", Project: "demo", Name: "a"},
			{ID: "b", Project: "demo", Name: "b"},
			{ID: "c", Project: "demo", Name: "c"},
		},
		tmuxAlive: map[string]bool{"b": true, "c": true},
	}
	m := newTestModel(be)

	// Stale snapshot: b and c were alive when it was taken; a (just opened)
	// hadn't been picked up yet.
	seedViews(m, be, nil)
	got := []string{m.sessions[0].ID, m.sessions[1].ID, m.sessions[2].ID}
	if got[0] == "a" {
		t.Fatalf("expected tmux-alive float to bury 'a' despite it being most recent, got order %v", got)
	}

	// Real world catches up: b and c's tmux died (so the core reports them
	// parked), a's tmux is now up. This is what a routine tick delivers.
	be.tmuxAlive = map[string]bool{"a": true}
	seedViews(m, be, nil)

	got2 := []string{m.sessions[0].ID, m.sessions[1].ID, m.sessions[2].ID}
	if got2[0] != "a" {
		t.Fatalf("expected list to re-sort as soon as the snapshot lands, got %v", got2)
	}
}
