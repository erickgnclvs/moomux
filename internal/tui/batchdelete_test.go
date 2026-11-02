package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
)

func threeSessions() *fakeBackend {
	return &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
		{ID: "demo:c", Project: "demo", Name: "c"},
	}}
}

func space(m *Model) { m.Update(tea.KeyMsg{Type: tea.KeySpace}) }

func TestBatchDeleteDeletesEveryMarkedSession(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)

	space(m) // marks a, steps to b
	m.Update(keyRune("j"))
	space(m) // marks c
	pressDelete(m)
	if v := m.View(); !strings.Contains(v, "Delete 2 sessions?") || !strings.Contains(v, "demo/a") || !strings.Contains(v, "demo/c") {
		t.Fatalf("batch dialog:\n%s", v)
	}
	run(m, keyRune("y"))
	if !slices.Equal(be.deleteCalls, []string{"demo:a", "demo:c"}) {
		t.Fatalf("deleteCalls = %v", be.deleteCalls)
	}
	if len(m.marked) != 0 || m.mode != ModeList {
		t.Fatalf("after delete: marked=%v mode=%v", m.marked, m.mode)
	}
	if len(m.sessions) != 1 || m.sessions[0].ID != "demo:b" {
		t.Fatalf("sessions left = %v", m.sessions)
	}
}

// The batch dialog is the same guard as the single one: a session with
// uncommitted or unpushed work makes the first y an acknowledgement.
func TestBatchDeleteWarnsBeforeDeletingRiskyWork(t *testing.T) {
	be := threeSessions()
	be.changeSummary = map[string]changeSummary{
		"demo:a": {filesChanged: 2, ok: true},
		"demo:b": {ok: true},
	}
	m := newTestModel(be)
	space(m)
	space(m)
	pressDelete(m)
	if v := m.View(); !strings.Contains(v, "1 with uncommitted changes") || !strings.Contains(v, "2 files changed") {
		t.Fatalf("batch dialog:\n%s", v)
	}
	m.Update(keyRune("y"))
	if len(be.deleteCalls) != 0 {
		t.Fatalf("first y deleted: %v", be.deleteCalls)
	}
	run(m, keyRune("y"))
	if len(be.deleteCalls) != 2 {
		t.Fatalf("deleteCalls = %v", be.deleteCalls)
	}
}

func TestBatchDeleteIgnoresYUntilChecked(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	space(m)
	m.Update(keyRune("d")) // check left in flight
	run(m, keyRune("y"))
	if len(be.deleteCalls) != 0 || m.mode != ModeConfirmDelete {
		t.Fatalf("y while checking: deleteCalls=%v mode=%v", be.deleteCalls, m.mode)
	}
	// A late single-session check for the cursor row must not stand in for
	// the batch's own.
	m.Update(ChangeSummaryMsg{ID: m.sessions[m.cursor].ID, Summary: changeSummary{ok: true}})
	run(m, keyRune("y"))
	if len(be.deleteCalls) != 0 || m.mode != ModeConfirmDelete {
		t.Fatalf("y after a stray single check: deleteCalls=%v mode=%v", be.deleteCalls, m.mode)
	}
}

func TestBatchDeleteCancelKeepsMarks(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	space(m)
	pressDelete(m)
	m.Update(keyRune("n"))
	if m.mode != ModeList || !m.marked["demo:a"] || len(be.deleteCalls) != 0 {
		t.Fatalf("after n: mode=%v marked=%v deleteCalls=%v", m.mode, m.marked, be.deleteCalls)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.marked) != 0 {
		t.Fatalf("esc left marks: %v", m.marked)
	}
}

func TestSpaceTogglesMark(t *testing.T) {
	m := newTestModel(threeSessions())
	space(m)
	m.Update(keyRune("k"))
	space(m)
	if len(m.marked) != 0 {
		t.Fatalf("marked = %v, want a toggled back off", m.marked)
	}
}

func TestMarkMergedMarksOnlyFinishedPRs(t *testing.T) {
	be := threeSessions()
	be.sessions = append(be.sessions, session.Session{ID: "demo:d", Project: "demo", Name: "d"})
	m := newTestModel(be)
	putView(m, "demo:a", sessionview.View{PR: &prstatus.Info{State: "MERGED"}})
	putView(m, "demo:b", sessionview.View{PR: &prstatus.Info{State: "OPEN"}})
	putView(m, "demo:c", sessionview.View{PR: &prstatus.Info{State: "CLOSED"}})

	m.Update(keyRune("M"))
	if len(m.marked) != 2 || !m.marked["demo:a"] || !m.marked["demo:c"] {
		t.Fatalf("marked = %v, want a and c", m.marked)
	}
}

func TestMarkedRowShowsMark(t *testing.T) {
	s := session.Session{ID: "demo:a", Name: "a"}
	row, _ := renderRow(s, sessionview.View{}, 40, false, true, "")
	if !strings.Contains(row, "✗") {
		t.Fatalf("marked row = %q", row)
	}
}

// A batch taller than the terminal scrolls its list, never the warning or
// the y/n line: those are what you're agreeing to.
func TestBatchDeleteKeepsControlsVisibleWhenTall(t *testing.T) {
	be := &fakeBackend{}
	for i := range 30 {
		id := fmt.Sprintf("s%02d", i)
		be.sessions = append(be.sessions, session.Session{ID: "demo:" + id, Project: "demo", Name: id})
	}
	m := newTestModel(be)
	m.width, m.height = 40, 20
	for range 30 {
		space(m)
	}
	pressDelete(m)
	v := m.View()
	if !strings.Contains(v, "y to confirm") {
		t.Fatalf("controls scrolled out of view:\n%s", v)
	}
	if h := lipgloss.Height(v); h > m.height {
		t.Fatalf("view is %d rows on a %d-row terminal", h, m.height)
	}
}

// The deletes run one after another in the background; the rows they
// remove must not trickle out of the list with them. They go the moment
// the delete is confirmed.
func TestBatchDeleteHidesRowsAtOnce(t *testing.T) {
	be := threeSessions()
	m := newTestModel(be)
	space(m)
	space(m)
	pressDelete(m)
	_, cmd := m.Update(keyRune("y")) // the deletes haven't run yet
	if len(be.deleteCalls) != 0 {
		t.Fatalf("deletes ran synchronously: %v", be.deleteCalls)
	}
	if len(m.sessions) != 1 || m.sessions[0].ID != "demo:c" {
		t.Fatalf("rows still listed while deleting: %v", m.sessions)
	}
	drainAll(m, cmd)
	if len(m.sessions) != 1 || m.sessions[0].ID != "demo:c" {
		t.Fatalf("after the batch: %v", m.sessions)
	}
}

// A session that fails to delete is still there, so it comes back.
func TestBatchDeleteFailureReappears(t *testing.T) {
	be := threeSessions()
	be.deleteErr = errors.New("boom")
	m := newTestModel(be)
	space(m)
	pressDelete(m)
	run(m, keyRune("y"))
	if len(m.sessions) != 3 {
		t.Fatalf("failed delete stayed hidden: %v", m.sessions)
	}
}

// A protected session can't be deleted from the TUI: d refuses it, space
// won't mark it, and a mark made before it was protected is dropped.
func TestProtectedSessionCannotBeDeleted(t *testing.T) {
	be := threeSessions()
	be.sessions[0].Protected = true
	m := newTestModel(be)

	pressDelete(m)
	if m.mode != ModeList || !strings.Contains(m.flash, "protected") {
		t.Fatalf("d on protected: mode=%v flash=%q", m.mode, m.flash)
	}
	space(m)
	if m.marked["demo:a"] {
		t.Fatal("protected session was marked")
	}

	m.marked = map[string]bool{"demo:a": true, "demo:b": true}
	pressDelete(m)
	run(m, keyRune("y"))
	if !slices.Equal(be.deleteCalls, []string{"demo:b"}) {
		t.Fatalf("deleteCalls = %v", be.deleteCalls)
	}
}
