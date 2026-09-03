package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// newTestModel and newMultiProjectTestModel both force ModeList after
// construction: New() now defaults to ModeMultiView (see model.go), but
// these two are the shared single-project-view test fixtures — most callers
// are exercising ModeList-specific dialogs/behavior and don't want every one
// of them rewritten for a default that's orthogonal to what they're testing.
// Tests that specifically want ModeMultiView set m.mode back explicitly.
func newTestModel(be *fakeBackend) *Model {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList
	return m
}

func newMultiProjectTestModel(be *fakeBackend) *Model {
	cfg := &config.Config{Projects: map[string]config.Project{
		"alpha": {Repo: "/tmp/alpha"},
		"beta":  {Repo: "/tmp/beta"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList
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

// TestOpenSessionFollowsSessionThatSortsToTop covers the "recently opened"
// sort: opening a session bumps its LastOpened and the backend re-sorts it
// to the front on the next Sessions() read. The cursor must follow it there
// rather than staying on the old index, which would now point at whatever
// session slid down into its place.
func TestOpenSessionFollowsSessionThatSortsToTop(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
	}}
	m := newTestModel(be)
	m.cursor = 1 // on "b"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a command to dispatch OpenSession")
	}
	resultMsg := cmd() // runs the closure, which calls backend.OpenSession

	// Opening "b" bumped its LastOpened past "a"'s; simulate the backend
	// now returning it first.
	be.sessions = []session.Session{
		{ID: "demo:b", Project: "demo", Name: "b"},
		{ID: "demo:a", Project: "demo", Name: "a"},
	}
	m.Update(resultMsg)

	if m.sessions[0].ID != "demo:b" {
		t.Fatalf("expected demo:b first after reorder, got %s", m.sessions[0].ID)
	}
	if m.cursor != 0 {
		t.Fatalf("expected cursor to follow opened session to 0, got %d", m.cursor)
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

func TestShiftUpNoOpWhenSortRecentFirst(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
	}}
	m := newTestModel(be)
	m.cfg.SortRecentFirst = true
	m.cursor = 1 // not at top, so this would move if manual reorder were active

	m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if len(be.moveSessionCalls) != 0 {
		t.Fatalf("expected shift+up to no-op while sorting by recent, got %d MoveSession calls", len(be.moveSessionCalls))
	}
}

func TestSettingsSortRowTogglesConfigAndPersists(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
	}}
	m := newTestModel(be)
	if m.cfg.SortRecentFirst {
		t.Fatal("expected SortRecentFirst to start false")
	}

	m.Update(runeKey('s'))
	if m.mode != ModeSettings {
		t.Fatalf("expected 's' to open ModeSettings, got %v", m.mode)
	}
	// Sort mode is the first settings row.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.cfg.SortRecentFirst {
		t.Fatal("expected enter on the sort row to flip SortRecentFirst on")
	}
	if len(be.setSortRecentFirstCalls) != 1 || !be.setSortRecentFirstCalls[0] {
		t.Fatalf("expected backend.SetSortRecentFirst(true), got %v", be.setSortRecentFirstCalls)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cfg.SortRecentFirst {
		t.Fatal("expected a second enter to flip SortRecentFirst back off")
	}
	if len(be.setSortRecentFirstCalls) != 2 || be.setSortRecentFirstCalls[1] {
		t.Fatalf("expected backend.SetSortRecentFirst(false), got %v", be.setSortRecentFirstCalls)
	}
}

func TestShiftLeftMovesProjectLeftAndFollowsCursor(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 1 // on "beta"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	if cmd == nil {
		t.Fatalf("expected a command to dispatch MoveProject")
	}
	resultMsg := cmd()
	if len(be.moveProjectCalls) != 1 {
		t.Fatalf("expected 1 MoveProject call, got %d", len(be.moveProjectCalls))
	}
	if got := be.moveProjectCalls[0]; got.name != "beta" || got.delta != -1 {
		t.Fatalf("MoveProject called with %+v, want {beta -1}", got)
	}

	// Backend reorders "beta" to the front; simulate the persisted order.
	m.cfg.Order = []string{"beta", "alpha"}
	m.Update(resultMsg)

	if m.projects[0] != "beta" {
		t.Fatalf("expected beta first after reorder, got %s", m.projects[0])
	}
	if m.activeProj != 0 {
		t.Fatalf("expected activeProj to follow moved project to 0, got %d", m.activeProj)
	}
}

func TestShiftLeftAtFrontIsNoOp(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 0 // already at front

	m.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	if len(be.moveProjectCalls) != 0 {
		t.Fatalf("expected no MoveProject call at front of list, got %d", len(be.moveProjectCalls))
	}
}

func TestShiftRightAtEndIsNoOp(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 1 // already at end

	m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	if len(be.moveProjectCalls) != 0 {
		t.Fatalf("expected no MoveProject call at end of list, got %d", len(be.moveProjectCalls))
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

// TestRefreshSessionsSortsLiveStatusFirst guards the active project's sort
// order: a session with a live tmux window floats to the top of the list.
func TestRefreshSessionsSortsLiveStatusFirst(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a1", Project: "demo", Name: "a1"},
		{ID: "demo:a2", Project: "demo", Name: "a2"},
	}}
	m := newTestModel(be)
	m.tmuxAlive = map[string]bool{"demo:a2": true}
	m.refreshSessions()

	got := make([]string, len(m.sessions))
	for i, s := range m.sessions {
		got[i] = s.ID
	}
	want := []string{"demo:a2", "demo:a1"}
	if len(got) != len(want) {
		t.Fatalf("sessions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sessions = %v, want %v (the live session should float above the non-live one)", got, want)
		}
	}
}
