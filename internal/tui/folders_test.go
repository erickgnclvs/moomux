package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

// TestFoldersOverlayNewCreatesFolder covers the Folders (G) overlay's "n"
// action end to end: it must open the create form (not the assign/rename
// one), and submitting a name must call backend.CreateFolder with the active
// project rather than silently doing nothing.
func TestFoldersOverlayNewCreatesFolder(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
	}}
	m := newTestModel(be)

	m.Update(runeKey('G'))
	if m.mode != ModeFolders {
		t.Fatalf("expected 'G' to open ModeFolders, got %v", m.mode)
	}

	m.Update(runeKey('n'))
	if m.mode != ModeFolderForm || m.folderFormKind != "create" {
		t.Fatalf("expected 'n' to open ModeFolderForm(create), got mode=%v kind=%q", m.mode, m.folderFormKind)
	}

	for _, r := range "auth" {
		m.folderForm.input, _ = m.folderForm.input.Update(runeKey(r))
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command to dispatch CreateFolder")
	}
	cmd()
	if len(be.createFolderCalls) != 1 {
		t.Fatalf("expected 1 CreateFolder call, got %d", len(be.createFolderCalls))
	}
	if got := be.createFolderCalls[0]; got.project != "demo" || got.name != "auth" {
		t.Fatalf("CreateFolder called with %+v, want {demo auth}", got)
	}
	if m.mode != ModeFolders {
		t.Fatalf("expected to return to ModeFolders after submit, got %v", m.mode)
	}
}

// TestCollapseFolderHidesMembers is the round trip the "n creates a folder"
// test above stops short of: every folder mutation lands in config, and
// m.cfg is the Model's *own clone* of config, so a handler that persists the
// change without applying the returned snapshot leaves the list rendering
// pre-mutation folder state — collapsing a folder appears to do nothing at
// all until moomux is restarted.
func TestCollapseFolderHidesMembers(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Order: 1},
		{ID: "demo:b", Project: "demo", Name: "b", Folder: "auth", Order: 2},
	}}
	m := newTestModel(be)
	m.cfg.Projects["demo"] = config.Project{
		Repo:    "/tmp/demo",
		Folders: map[string]config.FolderMeta{"auth": {Order: 2}},
	}
	be.cfg.Projects["demo"] = config.Project{
		Repo:    "/tmp/demo",
		Folders: map[string]config.FolderMeta{"auth": {Order: 2}},
	}
	m.refreshSessions()
	if len(m.sessions) != 2 {
		t.Fatalf("expected both sessions listed while expanded, got %d", len(m.sessions))
	}

	m.Update(runeKey('G'))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter to dispatch SetFolderCollapsed")
	}
	m.Update(cmd())

	if !m.cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Fatal("m.cfg still reports the folder expanded — the mutation's Cfg snapshot was not applied")
	}
	// refreshSessions drops a collapsed folder's members from m.sessions
	// entirely, so the cursor can't walk into a hidden row.
	if len(m.sessions) != 1 || m.sessions[0].ID != "demo:a" {
		t.Fatalf("expected only the loose session listed once collapsed, got %+v", m.sessions)
	}
}

// TestAssignFolderRefreshesSessionMembership covers the other half: folder
// *membership* lives on each Session, not in config, and the Model renders
// the last streamed snapshot in preference to a backend read — so a handler
// that refreshes without invalidating that cache keeps showing the session
// at top level after it was filed away.
func TestAssignFolderRefreshesSessionMembership(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Order: 1},
	}}
	m := newTestModel(be)
	// Stand in for a Watch snapshot having already been streamed in, which
	// is what allSessions renders from until something invalidates it.
	m.snapSessions = []session.Session{{ID: "demo:a", Project: "demo", Name: "a", Order: 1}}
	m.refreshSessions()

	m.Update(runeKey('g'))
	if m.mode != ModeFolderForm || m.folderFormKind != "assign" {
		t.Fatalf("expected 'g' to open the assign form, got mode=%v kind=%q", m.mode, m.folderFormKind)
	}
	for _, r := range "auth" {
		m.folderForm.input, _ = m.folderForm.input.Update(runeKey(r))
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command to dispatch SetSessionFolder")
	}
	m.Update(cmd())

	if len(m.sessions) != 1 {
		t.Fatalf("expected the session to stay listed, got %+v", m.sessions)
	}
	if m.sessions[0].Folder != "auth" {
		t.Fatalf("session still reads Folder=%q — the stale session snapshot was not dropped", m.sessions[0].Folder)
	}
	if _, ok := m.cfg.Projects["demo"].Folders["auth"]; !ok {
		t.Fatal("m.cfg is missing the folder created on first use — the Cfg snapshot was not applied")
	}
}
