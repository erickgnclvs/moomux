package tui

import (
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
)

// Regression for: a session's tmux-alive state changes (a new session comes
// up, or a killed one goes down) but the visible list keeps its old
// tmux-alive-float order until something unrelated happens to call
// refreshSessions() again — at which point the session appears to jump into
// the middle of the list out of nowhere. StatusRefreshedMsg fires every 2s
// and is the source of truth for tmuxAlive, so it must re-sort m.sessions
// itself rather than leaving the list to catch up later.
func TestStatusRefreshedMsgResortsSessions(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a", Project: "demo", Name: "a"},
		{ID: "b", Project: "demo", Name: "b"},
		{ID: "c", Project: "demo", Name: "c"},
	}}
	m := newTestModel(be)

	// Stale snapshot: b and c were marked alive before they got killed; a
	// (just opened) hasn't been picked up as alive yet.
	m.tmuxAlive = map[string]bool{"b": true, "c": true}
	m.refreshSessions()
	got := []string{m.sessions[0].ID, m.sessions[1].ID, m.sessions[2].ID}
	if got[0] == "a" {
		t.Fatalf("expected tmux-alive float to bury 'a' despite it being most recent, got order %v", got)
	}

	// Real world catches up: b and c's tmux died, a's tmux is now up. This
	// is exactly what the routine 2s StatusRefreshedMsg tick delivers.
	m.Update(StatusRefreshedMsg{TmuxAlive: map[string]bool{"a": true, "b": false, "c": false}})

	got2 := []string{m.sessions[0].ID, m.sessions[1].ID, m.sessions[2].ID}
	if got2[0] != "a" {
		t.Fatalf("expected list to re-sort as soon as StatusRefreshedMsg lands, got %v", got2)
	}
}
