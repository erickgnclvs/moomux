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
		Folders: map[string]config.FolderMeta{"auth": {}},
	}
	be.cfg.Projects["demo"] = config.Project{
		Repo:    "/tmp/demo",
		Folders: map[string]config.FolderMeta{"auth": {}},
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

// TestMultiViewPanelAndCursorAgreeAboutCollapsedFolders is the regression
// test for the worst bug folders shipped with: multi-view panels listed a
// collapsed folder's members while the keyboard's own session list dropped
// them, so the row highlighted in a panel and the session a keypress acted
// on were two different sessions. Every key multi-view delegates to the
// list — park, archive, delete worktree — landed on the wrong one.
func TestMultiViewPanelAndCursorAgreeAboutCollapsedFolders(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "alpha:a", Project: "alpha", Name: "a"},
		{ID: "alpha:b", Project: "alpha", Name: "b", Folder: "auth"},
		{ID: "alpha:c", Project: "alpha", Name: "c"},
		{ID: "beta:z", Project: "beta", Name: "z"},
	}}
	m := newMultiProjectTestModel(be)
	proj := m.cfg.Projects["alpha"]
	proj.Folders = map[string]config.FolderMeta{"auth": {Collapsed: true}}
	m.cfg.Projects["alpha"] = proj
	be.cfg = *m.cfg
	m.mode = ModeMultiView
	m.sessionsChanged()
	m.refreshSessions()

	panel := m.multiViewSessionsFor("alpha")
	for _, s := range panel {
		if s.ID == "alpha:b" {
			t.Fatal("a collapsed folder's member is still listed in the multi-view panel")
		}
	}
	// Whatever row the panel highlights must be the session the delegated
	// keypress acts on, at every cursor position.
	for i := range panel {
		m.multiCursors["alpha"] = i
		m.multiFocus = 0
		m.enterSingleProjectContext("alpha")
		if got := m.sessions[m.cursor].ID; got != panel[i].ID {
			t.Fatalf("panel row %d is %s but the cursor selects %s", i, panel[i].ID, got)
		}
	}
}

// TestSearchIntoCollapsedFolderExpandsAndSelects covers the same class of
// bug from the search overlay: jumping to a session inside a collapsed
// folder used to leave the cursor wherever it already was — silently, so
// the next keypress acted on an unrelated session.
func TestSearchIntoCollapsedFolderExpandsAndSelects(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:hidden", Project: "demo", Name: "hidden", Folder: "auth"},
		{ID: "demo:c", Project: "demo", Name: "c"},
	}}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{"auth": {Collapsed: true}}
	m.cfg.Projects["demo"] = proj
	be.cfg = *m.cfg
	m.sessionsChanged()
	m.refreshSessions()

	m.Update(runeKey('f'))
	for _, r := range "hidden" {
		m.Update(runeKey(r))
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected jumping into a collapsed folder to dispatch an expand")
	}
	drainCmd(m, cmd)

	if m.cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Error("expected the target's folder to be expanded")
	}
	if got := m.sessions[m.cursor].ID; got != "demo:hidden" {
		t.Fatalf("cursor selects %s, want demo:hidden", got)
	}
}

// A folder header isn't a cursor stop, so the mouse and the z key are how a
// folder gets opened without going through the Folders overlay.
func TestClickingAFolderHeaderTogglesIt(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b", Folder: "auth"},
	}}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{"auth": {}}
	m.cfg.Projects["demo"] = proj
	be.cfg = *m.cfg
	m.sessionsChanged()
	m.refreshSessions()
	m.View() // populates the hit boxes

	if len(m.folderHits) == 0 {
		t.Fatal("expected the rendered list to record a folder header hitbox")
	}
	hit := m.folderHits[0]
	_, cmd := m.Update(tea.MouseMsg{X: hit.x0, Y: hit.y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatal("expected clicking a folder header to dispatch a collapse")
	}
	drainCmd(m, cmd)
	if !m.cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Error("expected the click to collapse the folder")
	}
}

func TestZCollapsesTheSelectedSessionsFolder(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Folder: "auth"},
	}}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{"auth": {}}
	m.cfg.Projects["demo"] = proj
	be.cfg = *m.cfg
	m.sessionsChanged()
	m.refreshSessions()

	_, cmd := m.Update(runeKey('z'))
	if cmd == nil {
		t.Fatal("expected z to dispatch a collapse for the selected session's folder")
	}
	drainCmd(m, cmd)
	if !m.cfg.Projects["demo"].Folders["auth"].Collapsed {
		t.Error("expected z to collapse the folder")
	}
}

// Deleting a folder un-files every member in one keystroke, so it confirms
// first like the other deletes do.
func TestFolderDeleteAsksForConfirmation(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Folder: "auth"},
	}}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{"auth": {}}
	m.cfg.Projects["demo"] = proj
	be.cfg = *m.cfg
	m.sessionsChanged()
	m.refreshSessions()

	m.Update(runeKey('G'))
	m.Update(runeKey('d'))
	if m.mode != ModeConfirmDeleteFolder {
		t.Fatalf("expected d to open the delete confirmation, got mode %v", m.mode)
	}
	if len(be.deleteFolderCalls) != 0 {
		t.Fatal("expected nothing deleted before confirming")
	}
	m.Update(runeKey('n'))
	if m.mode != ModeFolders || len(be.deleteFolderCalls) != 0 {
		t.Fatal("expected n to cancel without deleting")
	}
	m.Update(runeKey('d'))
	_, cmd := m.Update(runeKey('y'))
	if cmd == nil {
		t.Fatal("expected y to dispatch the delete")
	}
	cmd()
	if len(be.deleteFolderCalls) != 1 {
		t.Fatalf("expected 1 DeleteFolder call, got %d", len(be.deleteFolderCalls))
	}
}
