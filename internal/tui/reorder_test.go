package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func newTestModel(be *fakeBackend) *Model {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, statusCh, func() {})
	m.width, m.height = 80, 24
	return m
}

// drainCmd runs a tea.Cmd synchronously and feeds its resulting message back
// into Update, mirroring what the Bubble Tea runtime does for the async
// MoveSession dispatch.
func drainCmd(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		m.Update(msg)
	}
}

func TestShiftUpMovesSessionUpAndFollowsCursor(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
	}}
	m := newTestModel(be)
	m.cursor = 1 // on "b"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if cmd == nil {
		t.Fatalf("expected a command to dispatch MoveSession")
	}
	resultMsg := cmd() // runs the closure, which calls backend.MoveSession
	if len(be.moveSessionCalls) != 1 {
		t.Fatalf("expected 1 MoveSession call, got %d", len(be.moveSessionCalls))
	}
	if got := be.moveSessionCalls[0]; got.id != "demo:b" || got.delta != -1 {
		t.Fatalf("MoveSession called with %+v, want {demo:b -1}", got)
	}

	// Backend reorders "b" to the front; simulate the refreshed session list.
	be.sessions = []session.Session{
		{ID: "demo:b", Project: "demo", Name: "b"},
		{ID: "demo:a", Project: "demo", Name: "a"},
	}
	m.Update(resultMsg)

	if m.sessions[0].ID != "demo:b" {
		t.Fatalf("expected demo:b first after reorder, got %s", m.sessions[0].ID)
	}
	if m.cursor != 0 {
		t.Fatalf("expected cursor to follow moved session to 0, got %d", m.cursor)
	}
}

func TestShiftUpAtTopIsNoOp(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
	}}
	m := newTestModel(be)
	m.cursor = 0 // already at top

	m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if len(be.moveSessionCalls) != 0 {
		t.Fatalf("expected no MoveSession call at top of list, got %d", len(be.moveSessionCalls))
	}
}

func TestShiftDownAtBottomIsNoOp(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
	}}
	m := newTestModel(be)
	m.cursor = 1 // already at bottom

	m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if len(be.moveSessionCalls) != 0 {
		t.Fatalf("expected no MoveSession call at bottom of list, got %d", len(be.moveSessionCalls))
	}
}

func TestMoveSessionErrorSetsFlashWithoutReordering(t *testing.T) {
	be := &fakeBackend{
		sessions: []session.Session{
			{ID: "demo:a", Project: "demo", Name: "a"},
			{ID: "demo:b", Project: "demo", Name: "b"},
		},
		moveSessionErr: errors.New("disk full"),
	}
	m := newTestModel(be)
	m.cursor = 1

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	drainCmd(m, cmd)

	if m.flashKind != "error" {
		t.Fatalf("expected error flash, got kind=%q text=%q", m.flashKind, m.flash)
	}
	if m.cursor != 1 {
		t.Fatalf("expected cursor unchanged on error, got %d", m.cursor)
	}
}
