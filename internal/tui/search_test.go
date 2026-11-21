package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/session"
)

func TestMatchSessionsFiltersByNameCaseInsensitive(t *testing.T) {
	all := []session.Session{
		{ID: "1", Project: "moomux", Name: "feature-auth"},
		{ID: "2", Project: "moomux", Name: "bugfix-login"},
		{ID: "3", Project: "eg_system", Name: "FEATURE-billing"},
	}

	got := matchSessions(all, "feature")

	if len(got) != 2 {
		t.Fatalf("matchSessions(%q) = %d results, want 2 (got %+v)", "feature", len(got), got)
	}
	for _, s := range got {
		if s.ID != "1" && s.ID != "3" {
			t.Errorf("unexpected match %+v", s)
		}
	}
}

func TestMatchSessionsIncludesArchivedSessions(t *testing.T) {
	all := []session.Session{
		{ID: "1", Project: "moomux", Name: "old-feature", Archived: true},
		{ID: "2", Project: "moomux", Name: "new-feature", Archived: false},
	}

	got := matchSessions(all, "feature")

	if len(got) != 2 {
		t.Fatalf("matchSessions(%q) = %d results, want 2 (archived sessions must be included)", "feature", len(got))
	}
}

func TestMatchSessionsEmptyQueryReturnsAllSortedByProjectThenName(t *testing.T) {
	all := []session.Session{
		{ID: "1", Project: "zeta", Name: "b-session"},
		{ID: "2", Project: "alpha", Name: "z-session"},
		{ID: "3", Project: "alpha", Name: "a-session"},
	}

	got := matchSessions(all, "")

	if len(got) != 3 {
		t.Fatalf("matchSessions(\"\") = %d results, want 3", len(got))
	}
	wantOrder := []string{"3", "2", "1"} // alpha/a-session, alpha/z-session, zeta/b-session
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("position %d: got id %s, want %s (order=%+v)", i, got[i].ID, id, got)
		}
	}
}

func TestMatchSessionsArchivedSortedLast(t *testing.T) {
	all := []session.Session{
		{ID: "1", Project: "alpha", Name: "a-session", Archived: true},
		{ID: "2", Project: "zeta", Name: "z-session", Archived: false},
		{ID: "3", Project: "alpha", Name: "b-session", Archived: false},
	}

	got := matchSessions(all, "")

	wantOrder := []string{"3", "2", "1"} // active sessions (project/name order) before archived
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("position %d: got id %s, want %s (order=%+v)", i, got[i].ID, id, got)
		}
	}
}

func TestMatchSessionsNoMatchReturnsEmpty(t *testing.T) {
	all := []session.Session{{ID: "1", Project: "moomux", Name: "feature-auth"}}

	got := matchSessions(all, "nonexistent")

	if len(got) != 0 {
		t.Fatalf("matchSessions with no match = %d results, want 0", len(got))
	}
}

func TestMatchSessionsSearchesEveryField(t *testing.T) {
	all := []session.Session{
		{ID: "branch", Name: "a", Branch: "alan/zebra-fix"},
		{ID: "project", Name: "b", Project: "zebra-app"},
		{ID: "folder", Name: "c", Folder: "Zebra"},
		{ID: "ticket", Name: "d", Ticket: "https://linear.app/x/ZEBRA-1"},
		{ID: "pr", Name: "e", PR: "https://github.com/o/zebra/pull/9"},
		{ID: "prompt", Name: "f", Prompt: "Fix the ZEBRA crossing"},
		{ID: "none", Name: "g", Branch: "main"},
	}
	got := matchSessions(all, "zebra")
	if len(got) != 6 {
		t.Fatalf("got %d matches, want 6: %+v", len(got), got)
	}
	for _, s := range got {
		if s.ID == "none" {
			t.Errorf("unrelated session matched")
		}
	}
}

// ctrl+u inside a text field must edit the text (delete to line start), not
// scroll the overlay viewport underneath it.
func TestCtrlUInSearchFieldEditsTextNotViewport(t *testing.T) {
	m := newTestModel(&fakeBackend{sessions: []session.Session{{ID: "a", Project: "p", Name: "alpha"}}})
	run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.mode != ModeSearch {
		t.Fatalf("mode = %v, want ModeSearch", m.mode)
	}
	typeText(m, "alp")
	run(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if got := m.searchInput.Value(); got != "" {
		t.Fatalf("search input after ctrl+u = %q, want cleared", got)
	}
}
