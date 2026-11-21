package tui

import (
	"errors"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
)

// The delete dialog acts on the session it opened for, not on the cursor.
func TestDeleteConfirmUsesDialogSessionNotCursor(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	pressDelete(m) // opens for demo:a
	m.cursor = 1   // a late result refocused the list
	run(m, keyRune("y"))
	if !slices.Equal(be.deleteCalls, []string{"demo:a"}) {
		t.Fatalf("deleteCalls = %v, want [demo:a]", be.deleteCalls)
	}
}

func TestDeleteConfirmClosesWhenSessionLeaves(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	pressDelete(m)
	be.sessions = be.sessions[1:] // another front end removed demo:a
	m.sessionsChanged()
	m.refreshSessions()
	if m.mode != ModeList {
		t.Fatalf("mode = %v, want the dialog closed", m.mode)
	}
	run(m, keyRune("y"))
	if len(be.deleteCalls) != 0 {
		t.Fatalf("y deleted %v", be.deleteCalls)
	}
}

func TestSingleDeleteHidesRowAndRestoresOnFailure(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	pressDelete(m)
	_, cmd := m.Update(keyRune("y"))
	if m.flash != "deleting…" || m.indexOfSession("demo:a") >= 0 {
		t.Fatalf("in flight: flash=%q sessions=%v", m.flash, m.sessions)
	}
	be.deleteErr = errors.New("boom")
	m.Update(cmd())
	if m.indexOfSession("demo:a") < 0 || m.flashKind != "error" {
		t.Fatalf("after failure: kind=%q sessions=%v", m.flashKind, m.sessions)
	}
}

func TestTagFormSavesToItsOwnSession(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	m.Update(keyRune("t"))
	if m.mode != ModeTagForm {
		t.Fatalf("mode = %v", m.mode)
	}
	m.cursor = 2
	run(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(be.tagCalls) != 1 || be.tagCalls[0].id != "demo:a" {
		t.Fatalf("tagCalls = %v, want demo:a", be.tagCalls)
	}
}

func TestLateResultsDoNotMoveCursor(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	m.cursor = 2
	m.Update(SessionPinnedMsg{ID: "demo:a", Pinned: true})
	m.Update(SessionOpenedMsg{ID: "demo:a"})
	m.Update(SessionArchivedMsg{ID: "demo:a", NextID: "demo:b"})
	m.Update(SessionDeletedMsg{ID: "demo:a", NextID: "demo:b"})
	if got := m.sessions[m.cursor].ID; got != "demo:c" {
		t.Fatalf("cursor moved to %s", got)
	}
}

func TestNewFormProjectFollowsNameWhenProjectsChange(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // alpha, beta
	m.Update(keyRune("n"))
	m.newFormProjIdx = indexOfProject(m.projects, "beta")
	m.cfg.Projects["aaa"] = config.Project{Repo: "/tmp/aaa"}
	m.cfg.Order = []string{"aaa", "alpha", "beta"}
	m.refreshProjects()
	if got := m.projectAt(m.newFormProjIdx); got != "beta" {
		t.Fatalf("form project = %q, want beta", got)
	}
	delete(m.cfg.Projects, "beta")
	m.refreshProjects()
	run(m, tea.KeyMsg{Type: tea.KeyEnter}) // must not panic
	if m.newFormProjIdx != -1 {
		t.Fatalf("newFormProjIdx = %d, want -1", m.newFormProjIdx)
	}
}

func TestUpgradeWaitsForInFlightCreateAndKeepsDraft(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	m.creating = []string{"p"}
	m.Update(keyRune("n"))
	_, cmd := m.Update(UpdateAppliedMsg{})
	if cmd != nil || m.Relaunch {
		t.Fatalf("quit while a create was in flight")
	}
	_, cmd = m.Update(SessionCreatedMsg{Prompt: "p"})
	if cmd == nil || !m.Relaunch {
		t.Fatalf("no relaunch once the create finished")
	}
}

func TestFailedCreateReturnsToMultiView(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	m.mode = ModeMultiView
	m.Update(CreateFailedMsg{Err: errors.New("x")})
	if m.mode != ModeNewForm || m.sessionDialogReturn != ModeMultiView {
		t.Fatalf("mode=%v return=%v", m.mode, m.sessionDialogReturn)
	}
}

func TestLateSaveDoesNotCloseUnrelatedOverlay(t *testing.T) {
	m := newTestModel(threeSessions())
	m.mode = ModeHelp
	m.Update(SessionAgentUpdatedMsg{Session: m.sessions[0]})
	m.Update(ProjectUpdatedMsg{Name: "demo"})
	if m.mode != ModeHelp {
		t.Fatalf("mode = %v, want help still open", m.mode)
	}
}
