package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
