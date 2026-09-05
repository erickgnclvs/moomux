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
	m := New(cfg, be, statusCh, func() {})
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
	m := New(cfg, be, statusCh, func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList
	return m
}

// drainCmd runs a tea.Cmd synchronously and feeds its resulting message back
// into Update, mirroring what the Bubble Tea runtime does for the async
// ReorderSessions dispatch.
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
		t.Fatalf("expected a command to dispatch ReorderSessions")
	}
	// The optimistic local swap happens synchronously, before the persist
	// command even runs.
	if m.sessions[0].ID != "demo:b" || m.cursor != 0 {
		t.Fatalf("expected optimistic swap to put demo:b first with cursor following to 0, got sessions=%v cursor=%d", sessionIDs(m.sessions), m.cursor)
	}

	resultMsg := cmd() // runs the closure, which calls backend.ReorderSessions
	if len(be.reorderSessionsCalls) != 1 {
		t.Fatalf("expected 1 ReorderSessions call, got %d", len(be.reorderSessionsCalls))
	}
	if got := be.reorderSessionsCalls[0].ids; len(got) != 2 || got[0] != "demo:b" || got[1] != "demo:a" {
		t.Fatalf("ReorderSessions called with %v, want [demo:b demo:a]", got)
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
	if len(be.reorderSessionsCalls) != 0 {
		t.Fatalf("expected no ReorderSessions call at top of list, got %d", len(be.reorderSessionsCalls))
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
	if len(be.reorderSessionsCalls) != 0 {
		t.Fatalf("expected no ReorderSessions call at bottom of list, got %d", len(be.reorderSessionsCalls))
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
	if len(be.reorderSessionsCalls) != 0 {
		t.Fatalf("expected shift+up to no-op while sorting by recent, got %d ReorderSessions calls", len(be.reorderSessionsCalls))
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
		reorderSessionsErr: errors.New("disk full"),
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

// TestUpDownFollowVisualOrderAcrossNonAdjacentFolderMembers is a regression
// test for cursor movement indexing m.sessions directly instead of the
// visual (folder-grouped) order. Session.Folder doesn't constrain Order —
// SetSessionFolder never touches it — so two sessions filed into the same
// folder can sit at arbitrary, non-adjacent positions in m.sessions even
// though buildDisplayLines always renders a folder's members as one
// contiguous block. Here "grp" contains sessions c and e, which are NOT
// adjacent in m.sessions (b and d sit between them) — Up/Down must still
// walk the rendered order (a, b, [grp: c, e], d), not m.sessions' order.
func TestUpDownFollowVisualOrderAcrossNonAdjacentFolderMembers(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Order: 1},
		{ID: "demo:b", Project: "demo", Name: "b", Order: 2},
		{ID: "demo:c", Project: "demo", Name: "c", Order: 3, Folder: "grp"},
		{ID: "demo:d", Project: "demo", Name: "d", Order: 4},
		{ID: "demo:e", Project: "demo", Name: "e", Order: 5, Folder: "grp"},
	}}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{"grp": {}}
	m.cfg.Projects["demo"] = proj
	m.refreshSessions()

	idOf := func(idx int) string { return m.sessions[idx].ID }
	m.cursor = 1 // "b"
	if idOf(m.cursor) != "demo:b" {
		t.Fatalf("setup: cursor not on b: %s", idOf(m.cursor))
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := idOf(m.cursor); got != "demo:c" {
		t.Fatalf("down from b: cursor on %s, want c (first grp member)", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := idOf(m.cursor); got != "demo:e" {
		t.Fatalf("down from c: cursor on %s, want e (second grp member, not d — m.sessions has d between c and e)", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := idOf(m.cursor); got != "demo:d" {
		t.Fatalf("down from e: cursor on %s, want d (after the grp block closes)", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := idOf(m.cursor); got != "demo:e" {
		t.Fatalf("up from d: cursor on %s, want e", got)
	}
}

// TestMoveDownSwapsVisuallyAdjacentSessionAcrossNonAdjacentFolderMembers is
// the MoveDown/MoveUp half of the same regression: swapping m.cursor with
// its raw m.sessions neighbor (rather than its visual neighbor) would
// persist a swap between two sessions the user never saw next to each
// other.
func TestMoveDownSwapsVisuallyAdjacentSessionAcrossNonAdjacentFolderMembers(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Order: 1},
		{ID: "demo:b", Project: "demo", Name: "b", Order: 2},
		{ID: "demo:c", Project: "demo", Name: "c", Order: 3, Folder: "grp"},
		{ID: "demo:d", Project: "demo", Name: "d", Order: 4},
		{ID: "demo:e", Project: "demo", Name: "e", Order: 5, Folder: "grp"},
	}}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{"grp": {}}
	m.cfg.Projects["demo"] = proj
	m.refreshSessions()

	// Put the cursor on "c" (visually followed by "e", not "d") and move it
	// down — it must swap with "e", the visually-adjacent session.
	for i, s := range m.sessions {
		if s.ID == "demo:c" {
			m.cursor = i
		}
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if cmd == nil {
		t.Fatal("expected a command to dispatch ReorderSessions")
	}
	if got := m.sessions[m.cursor].ID; got != "demo:c" {
		t.Fatalf("cursor should still be on c after following its swap, got %s", got)
	}
	drainCmd(m, cmd)
	if len(be.reorderSessionsCalls) != 1 {
		t.Fatalf("expected 1 ReorderSessions call, got %d", len(be.reorderSessionsCalls))
	}

	// Persisted physical slot order doesn't need to match rendered order —
	// buildDisplayLines regroups by Folder on every render regardless. What
	// must hold is the actual regression: re-deriving the visual order from
	// the persisted result puts e before c (c moved past its true visual
	// neighbor) with d still right after the folder block, not reordered
	// relative to it.
	reordered := make([]session.Session, len(be.sessions))
	byID := map[string]session.Session{}
	for _, s := range be.sessions {
		byID[s.ID] = s
	}
	for i, id := range be.reorderSessionsCalls[0].ids {
		reordered[i] = byID[id]
	}
	be.sessions = reordered
	m.refreshSessions()
	lines := m.buildDisplayLines()
	var visual []string
	for _, l := range lines {
		if l.folder != "" {
			visual = append(visual, "["+l.folder+"]")
			continue
		}
		visual = append(visual, m.sessions[l.sessionIdx].ID)
	}
	want := []string{"demo:a", "demo:b", "[grp]", "demo:e", "demo:c", "demo:d"}
	if len(visual) != len(want) {
		t.Fatalf("visual order = %v, want %v", visual, want)
	}
	for i := range want {
		if visual[i] != want[i] {
			t.Fatalf("visual order = %v, want %v", visual, want)
		}
	}
}

// TestRapidReorderKeypressesCoalesceInsteadOfRacing is a regression test for
// completions arriving out of dispatch order: dispatchReorder must never
// let two ReorderSessions calls be in flight at once, or whichever finishes
// last — not whichever was dispatched last — silently wins.
func TestRapidReorderKeypressesCoalesceInsteadOfRacing(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
		{ID: "demo:b", Project: "demo", Name: "b"},
		{ID: "demo:c", Project: "demo", Name: "c"},
	}}
	m := newTestModel(be)
	m.cursor = 2 // on "c"

	_, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp}) // c above b: a, c, b
	if cmd1 == nil {
		t.Fatal("expected the first keypress to dispatch a command")
	}
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp}) // c above a: c, a, b
	if cmd2 != nil {
		t.Fatal("expected the second keypress, while the first is still in flight, to coalesce (nil cmd) instead of dispatching its own")
	}
	if len(be.reorderSessionsCalls) != 0 {
		t.Fatalf("expected no ReorderSessions call yet (only the first cmd has been drained), got %d", len(be.reorderSessionsCalls))
	}

	// Completing cmd1 completes the in-flight call and, seeing
	// reorderDirty, must return a follow-up command carrying the LATEST
	// order (both moves applied) rather than the stale one captured at
	// cmd1's dispatch time — drained here by hand (not via drainCmd, which
	// only runs one level and would silently drop this follow-up) to
	// observe it.
	msg1 := cmd1()
	if len(be.reorderSessionsCalls) != 1 {
		t.Fatalf("expected cmd1 to make exactly 1 call before completing, got %d", len(be.reorderSessionsCalls))
	}
	_, cmd1b := m.Update(msg1)
	if cmd1b == nil {
		t.Fatal("expected a follow-up command for the coalesced second move")
	}
	cmd1b()
	if len(be.reorderSessionsCalls) != 2 {
		t.Fatalf("expected the deferred move to fire once cmd1 completed, got %d calls", len(be.reorderSessionsCalls))
	}
	got := be.reorderSessionsCalls[1].ids
	if len(got) != 3 || got[0] != "demo:c" || got[1] != "demo:a" || got[2] != "demo:b" {
		t.Fatalf("second ReorderSessions call = %v, want [demo:c demo:a demo:b] (both moves applied)", got)
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
