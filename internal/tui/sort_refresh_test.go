package tui

import (
	"testing"

	"github.com/erickgnclvs/moomux/internal/session"
)

// Regression for: the served order changes (another front end reorders the
// project, or a session is pinned elsewhere) but the visible list keeps its
// old order until something unrelated happens to call refreshSessions()
// again — at which point a session appears to jump into the middle of the
// list out of nowhere. The snapshot stream is the source of truth for
// display order, so applying one must re-sort m.sessions itself rather than
// leaving the list to catch up later.
func TestStatusTickResortsSessions(t *testing.T) {
	be := &fakeBackend{
		sessions: []session.Session{
			{ID: "a", Project: "demo", Name: "a"},
			{ID: "b", Project: "demo", Name: "b"},
			{ID: "c", Project: "demo", Name: "c"},
		},
	}
	m := newTestModel(be)
	seedViews(m, be, nil)
	if got := m.sessions[0].ID; got != "a" {
		t.Fatalf("first session = %q, want a (stored order)", got)
	}

	// Another front end moves c to the top. This is what a routine tick
	// delivers.
	be.sessions = []session.Session{be.sessions[2], be.sessions[0], be.sessions[1]}
	seedViews(m, be, nil)

	got := []string{m.sessions[0].ID, m.sessions[1].ID, m.sessions[2].ID}
	if got[0] != "c" {
		t.Fatalf("expected list to re-sort as soon as the snapshot lands, got %v", got)
	}
}
