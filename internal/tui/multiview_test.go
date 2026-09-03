package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func TestMultiViewProjectsFitsWidth(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be) // alpha, beta

	m.width = 40 // one panel fits (40/34 = 1)
	if got := m.multiViewProjects(); len(got) != 1 {
		t.Fatalf("width=40: got %d panels, want 1: %v", len(got), got)
	}

	m.width = 80 // both panels fit (80/34 = 2)
	got := m.multiViewProjects()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("width=80: got %v, want [alpha beta]", got)
	}
}

// TestMultiViewTicketIconsAreClickable is the regression test for the bug
// report that multi-view's session-list icons stopped being clickable:
// renderMultiView used to discard link hits entirely (m.linkHits = nil, never
// repopulated) since its per-panel layout never fed updateLinkHits. Clicking
// a ticket/PR icon in a visible panel must resolve to that session's URL.
func TestMultiViewTicketIconsAreClickable(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1", Ticket: "https://ticket.example/a1"},
		{ID: "b1", Project: "beta", Name: "b1", PR: "https://pr.example/b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.width, m.height = 80, 24
	m.mode = ModeMultiView

	frame := m.View()
	lines := strings.Split(frame, "\n")
	findCol := func(icon string) (line, col int) {
		for li, l := range lines {
			if idx := strings.Index(l, icon); idx >= 0 {
				return li, lipgloss.Width(l[:idx])
			}
		}
		t.Fatalf("icon %q not found in rendered frame:\n%s", icon, frame)
		return -1, -1
	}

	ticketLine, ticketCol := findCol(iconTicket)
	if got, _ := m.linkAt(ticketCol, ticketLine); got != be.sessions[0].Ticket {
		t.Errorf("click on ticket icon at (%d,%d) = %q, want %q", ticketCol, ticketLine, got, be.sessions[0].Ticket)
	}

	prLine, prCol := findCol(iconPR)
	if got, _ := m.linkAt(prCol, prLine); got != be.sessions[1].PR {
		t.Errorf("click on pr icon at (%d,%d) = %q, want %q", prCol, prLine, got, be.sessions[1].PR)
	}
}

// TestMultiViewPanelShowsCowWhenListIsShort is the regression test for the
// bug report that the detail panel's cow art was hidden off the bottom of a
// multi-view panel: renderMultiPanel used to split list/detail height as a
// flat 1/3 ratio of the whole panel regardless of how much room the detail
// section's own content actually needed, so a project with only a couple of
// sessions still starved its detail section down to minMultiDetailHeight —
// nowhere near enough rows for the detail fields plus the multi-line cowsay
// art beneath them. Sizing detail off detailContentHeight instead frees the
// rest of the panel for detail, letting the cow's fixed closing lines fit on
// screen.
func TestMultiViewPanelShowsCowWhenListIsShort(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be) // alpha, beta
	m.width, m.height = 80, 30
	m.mode = ModeMultiView

	frame := m.View()
	if !strings.Contains(frame, "||     ||") {
		t.Fatalf("detail panel's cow art got clipped off the bottom despite its short session list:\n%s", frame)
	}
}

// TestMultiViewPanelShowsCowWhenListIsLong is the second half of the same
// bug report: sizing the list off its own session count (an earlier attempt
// at this fix) solved the short-list panels but not the long-list one —
// which is the common case in practice, since a project worth watching in
// multi-view usually has more sessions than a couple. A long list still ate
// nearly all of a tall panel's height before detail got a look-in, so detail
// (and its cow) stayed starved down to minMultiDetailHeight exactly as
// before. Sizing detail off its actual rendered content (detailContentHeight)
// instead — and letting the list simply scroll further to make room — fixes
// this regardless of how many sessions the focused project has. This calls
// renderMultiPanel directly (rather than going through a two-panel m.View())
// so the assertion is scoped to the one panel actually under test — a
// same-frame sibling panel with a short list of its own would otherwise show
// a full cow regardless, masking a clipped one right next to it.
func TestMultiViewPanelShowsCowWhenListIsLong(t *testing.T) {
	var sessions []session.Session
	for i := 0; i < 20; i++ {
		sessions = append(sessions, session.Session{
			ID: fmt.Sprintf("eg%d", i), Project: "alpha", Name: fmt.Sprintf("sess-%d", i),
			Ticket: "https://ticket.example/x", PR: "https://pr.example/x",
			Prompt: "a long enough prompt that it wraps across a few lines like a real session's would",
		})
	}
	be := &fakeBackend{sessions: sessions}
	m := newTestModel(be)

	content, _, _ := m.renderMultiPanel("alpha", sessions, 19, 40, 33, true)
	if !strings.Contains(content, "||     ||") {
		t.Fatalf("detail panel's cow art got clipped off the bottom despite the panel being tall enough, just because its session list is long:\n%s", content)
	}
}

// TestMultiViewListDetailSplitStableAcrossSelection is the regression test
// for the bug report that scrolling through a project's session list in
// multi-view felt like it jumped backward: renderMultiPanel used to size its
// list/detail split off the *selected* session's own detail height, so
// moving the cursor onto a session with a longer or shorter detail block
// resized the list's viewport out from under its own scroll position — the
// highlighted row could move opposite the arrow key that was pressed. Sizing
// the split off the detail panel's structural worst case (every optional
// field, every wrapped prompt line) instead keeps the list/detail boundary
// fixed regardless of which session is selected.
func TestMultiViewListDetailSplitStableAcrossSelection(t *testing.T) {
	var sessions []session.Session
	for i := 0; i < 20; i++ {
		sessions = append(sessions, session.Session{
			ID: fmt.Sprintf("s%d", i), Project: "alpha", Name: fmt.Sprintf("sess-%d", i),
		})
	}
	// Only the last session has a tall detail block (ticket, PR, wrapped
	// prompt) — the exact shape that used to shrink the list's viewport the
	// moment it became selected.
	sessions[len(sessions)-1].Ticket = "https://ticket.example/x"
	sessions[len(sessions)-1].PR = "https://pr.example/x"
	sessions[len(sessions)-1].Prompt = "a long enough prompt that it wraps across a few lines like a real session's would"

	be := &fakeBackend{sessions: sessions}
	m := newTestModel(be)

	splitLine := func(cursor int) int {
		frame, _, _ := m.renderMultiPanel("alpha", sessions, cursor, 40, 33, true)
		for i, line := range strings.Split(frame, "\n") {
			if strings.Contains(line, "──────") {
				return i
			}
		}
		t.Fatalf("cursor=%d: no separator line found in frame:\n%s", cursor, frame)
		return -1
	}

	short := splitLine(0)
	long := splitLine(len(sessions) - 1)
	if short != long {
		t.Fatalf("list/detail split moved when selection changed: cursor=0 split at line %d, cursor=%d split at line %d", short, len(sessions)-1, long)
	}
}

// TestMultiViewTicketIconClickCopiesOverSSH asserts that a ticket icon click
// inside the multi-panel grid still follows the mobile/SSH rule (see
// TestLinkClickOverSSHCopiesInsteadOfOpening for the single-panel case):
// copy the URL via OSC 52 rather than shelling out to `open`, since `open`
// would launch a browser on the remote machine, not the phone/laptop the
// user is actually looking at. handleListMouse applies this uniformly via
// m.isRemote() regardless of how many panels are visible, so this should
// hold without any multi-view-specific handling.
func TestMultiViewTicketIconClickCopiesOverSSH(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001")

	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1", Ticket: "https://ticket.example/a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.width, m.height = 80, 24
	m.mode = ModeMultiView
	m.View() // populate m.linkHits

	var hit resolvedLinkHit
	for _, h := range m.linkHits {
		if h.url == be.sessions[0].Ticket {
			hit = h
		}
	}
	if hit.url == "" {
		t.Fatalf("ticket link hit not found among m.linkHits: %+v", m.linkHits)
	}

	updated, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: hit.x0, Y: hit.y})
	m2 := updated.(*Model)

	if cmd != nil {
		t.Errorf("expected no async command for the copy path, got one")
	}
	if m2.flashKind != "info" || m2.flash != "copied "+be.sessions[0].Ticket {
		t.Errorf("flash = (%q, %q), want (\"info\", %q)", m2.flashKind, m2.flash, "copied "+be.sessions[0].Ticket)
	}
}

// TestMultiViewRowClickPicksProjectWithoutOpening asserts that clicking a
// session row inside a non-focused panel switches multiFocus to that row's
// own project (the multi-view equivalent of "click a project to pick it")
// and selects the row within that panel, but does not open/attach the
// session — see TestSessionRowClickSelectsWithoutOpening for the single-
// project-view counterpart.
func TestMultiViewRowClickPicksProjectWithoutOpening(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
		{ID: "b2", Project: "beta", Name: "b2"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha
	m.View()         // populate m.rowHits

	var hit resolvedRowHit
	for _, h := range m.rowHits {
		if h.sessionID == "b2" {
			hit = h
		}
	}
	if hit.sessionID == "" {
		t.Fatalf("no row hit found for b2, hits: %+v", m.rowHits)
	}

	run(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: hit.x0, Y: hit.y})

	if m.multiFocus != 1 {
		t.Errorf("multiFocus = %d, want 1 (beta, b2's project)", m.multiFocus)
	}
	if got := m.multiCursorFor("beta"); got != 1 {
		t.Errorf("beta cursor = %d, want 1 (b2)", got)
	}
	if len(be.openCalls) != 0 {
		t.Errorf("openCalls = %v, want none (row click no longer opens)", be.openCalls)
	}
}

// TestMultiViewPanelClickPicksProjectEvenOffAnyRow asserts that a click
// landing inside a panel but not on any session row — its title line, its
// detail pane, empty space below a short list — still picks that panel's
// project, via m.panelHits rather than m.rowHits. Without this, only
// clicking directly on a row could switch multiFocus, which doesn't match
// "click anywhere in the project square."
func TestMultiViewPanelClickPicksProjectEvenOffAnyRow(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha
	m.View()         // populate m.panelHits

	var hit resolvedPanelHit
	for _, h := range m.panelHits {
		if h.project == "beta" {
			hit = h
		}
	}
	if hit.project == "" {
		t.Fatalf("no panel hit found for beta, hits: %+v", m.panelHits)
	}

	// The panel's top-left corner (its border/title line) isn't any
	// session's row.
	if _, ok := m.sessionRowAt(hit.x0, hit.y0); ok {
		t.Fatalf("test setup: (%d,%d) unexpectedly matched a row hit", hit.x0, hit.y0)
	}

	run(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: hit.x0, Y: hit.y0})

	if m.multiFocus != 1 {
		t.Errorf("multiFocus = %d, want 1 (beta)", m.multiFocus)
	}
	if len(be.openCalls) != 0 {
		t.Errorf("openCalls = %v, want none", be.openCalls)
	}
}

func TestMultiViewTabWrapsFocusAmongVisiblePanels(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView

	if _, cmd := m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab}); cmd != nil {
		t.Fatalf("unexpected cmd from Tab")
	}
	if m.multiFocus != 1 {
		t.Fatalf("after one Tab: multiFocus = %d, want 1", m.multiFocus)
	}
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab})
	if m.multiFocus != 0 {
		t.Fatalf("after wrapping Tab: multiFocus = %d, want 0 (wrap)", m.multiFocus)
	}
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.multiFocus != 1 {
		t.Fatalf("after Shift-Tab wrap-back: multiFocus = %d, want 1", m.multiFocus)
	}
}

func TestMultiViewArrowKeysSlideFocusBetweenProjects(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView

	if _, cmd := m.updateMultiView(tea.KeyMsg{Type: tea.KeyRight}); cmd != nil {
		t.Fatalf("unexpected cmd from Right")
	}
	if m.multiFocus != 1 {
		t.Fatalf("after one Right: multiFocus = %d, want 1", m.multiFocus)
	}
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyRight})
	if m.multiFocus != 0 {
		t.Fatalf("after wrapping Right: multiFocus = %d, want 0 (wrap)", m.multiFocus)
	}
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyLeft})
	if m.multiFocus != 1 {
		t.Fatalf("after Left wrap-back: multiFocus = %d, want 1", m.multiFocus)
	}
}

// TestMultiViewReorderFollowsFocusToNewColumn is the regression test for the
// bug report that reordering a project in multi-view left the highlighted
// panel behind: ProjectMovedMsg used to re-anchor m.activeProj (and, while
// the picker is open, m.pickerCursor) by name, but never m.multiFocus — the
// index that actually drives which panel renders focused in multi-view. So
// shifting "alpha" right slid alpha into beta's old column while multiFocus
// stayed at index 0, making the highlight appear to stay on beta instead of
// following alpha to its new position.
func TestMultiViewReorderFollowsFocusToNewColumn(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha

	_, cmd := m.updateMultiView(tea.KeyMsg{Type: tea.KeyShiftRight})
	if cmd == nil {
		t.Fatalf("expected a command to dispatch MoveProject")
	}
	resultMsg := cmd()
	if len(be.moveProjectCalls) != 1 {
		t.Fatalf("expected 1 MoveProject call, got %d", len(be.moveProjectCalls))
	}
	if got := be.moveProjectCalls[0]; got.name != "alpha" || got.delta != 1 {
		t.Fatalf("MoveProject called with %+v, want {alpha 1}", got)
	}

	// Backend reorders "alpha" after "beta"; simulate the persisted order.
	m.cfg.Order = []string{"beta", "alpha"}
	m.Update(resultMsg)

	if m.projects[1] != "alpha" {
		t.Fatalf("expected alpha second after reorder, got %v", m.projects)
	}
	if m.multiFocus != 1 {
		t.Fatalf("expected multiFocus to follow alpha to column 1, got %d", m.multiFocus)
	}
}

// TestMultiViewCursorIsPerProject is the regression test for the bug this
// per-project map fixes: without it, moving the cursor in one panel would
// have to share a single index with every other panel (as m.cursor does for
// the single-project list), so switching focus to another project's panel
// would carry over a cursor position that has nothing to do with that
// project's own session list.
func TestMultiViewCursorIsPerProject(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "a2", Project: "alpha", Name: "a2"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha

	m.updateMultiView(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.multiCursorFor("alpha"); got != 1 {
		t.Fatalf("alpha cursor = %d, want 1", got)
	}

	// Switching focus to beta (only one session) must not be affected by
	// alpha's cursor position, and moving there must not disturb alpha's.
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.multiCursorFor("beta"); got != 0 {
		t.Fatalf("beta cursor = %d, want 0", got)
	}
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.multiCursorFor("beta"); got != 0 {
		t.Fatalf("beta cursor after Down (only 1 session) = %d, want 0", got)
	}
	if got := m.multiCursorFor("alpha"); got != 1 {
		t.Fatalf("alpha cursor changed after moving beta's: got %d, want 1", got)
	}
}

// TestMultiViewDownFollowsPanelOrderWithLiveSession is the regression test
// for arrow nav "skipping" sessions in a grid panel: multiViewSessionsFor
// (what the panel renders) must float live-tmux sessions to the top the same
// way refreshSessions does for m.sessions (what cursor movement acts on) —
// otherwise Down walks the sorted order while the panel displays the
// unsorted one, and the highlight jumps past whatever row that reordering
// skipped over.
func TestMultiViewDownFollowsPanelOrderWithLiveSession(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "a2", Project: "alpha", Name: "a2"},
		{ID: "a3", Project: "alpha", Name: "a3"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha
	m.tmuxAlive = map[string]bool{"a2": true}

	panelOrder := m.multiViewSessionsFor("alpha")
	if len(panelOrder) != 3 || panelOrder[0].ID != "a2" {
		t.Fatalf("panel order = %v, want a2 first (live session floats to top)", panelOrder)
	}

	m.updateMultiView(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.multiCursorFor("alpha"); got != 1 {
		t.Fatalf("cursor after one Down = %d, want 1 (the row right below the initial selection)", got)
	}
}

// TestMultiViewTabSlidesWindowWhenFocusLeaves is the regression test for the
// bug this fixes: previously multiViewProjects() always showed the first N
// projects and Tab only cycled among those, so focus could never reach a
// project that didn't fit in the initial window — it just silently refused
// to move once it hit the last visible panel.
func TestMultiViewTabSlidesWindowWhenFocusLeaves(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
		{ID: "g1", Project: "gamma", Name: "g1"},
	}}
	cfg := &config.Config{Projects: map[string]config.Project{
		"alpha": {Repo: "/tmp/alpha"},
		"beta":  {Repo: "/tmp/beta"},
		"gamma": {Repo: "/tmp/gamma"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 40, 24 // multiPanelMinWidth=34: only 1 panel fits
	m.mode = ModeMultiView

	if n := m.multiViewPanelCount(); n != 1 {
		t.Fatalf("panel count = %d, want 1", n)
	}
	if got := m.multiViewProjects(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("initial window = %v, want [alpha]", got)
	}

	m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab})
	if m.multiFocus != 1 {
		t.Fatalf("multiFocus after Tab = %d, want 1", m.multiFocus)
	}
	if got := m.multiViewProjects(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("window after Tab = %v, want [beta] (should have slid)", got)
	}

	m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.multiViewProjects(); len(got) != 1 || got[0] != "gamma" {
		t.Fatalf("window after 2nd Tab = %v, want [gamma]", got)
	}

	// Wrapping back to alpha must slide the window all the way back too.
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.multiViewProjects(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("window after wrap = %v, want [alpha]", got)
	}
}

// TestMultiViewHidesProjectsWithoutActiveSessions is the regression test for
// the requirement that multi-view skip projects with nothing active going
// on — an empty or all-archived project isn't worth a panel in a view whose
// whole point is surveying active work across projects at a glance.
func TestMultiViewHidesProjectsWithoutActiveSessions(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1", Archived: true},
	}}
	m := newMultiProjectTestModel(be) // alpha, beta
	m.width = 200                     // plenty of room for both, if both were eligible

	got := m.multiViewEligibleProjects()
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("eligible projects = %v, want [alpha] (beta has no non-archived sessions)", got)
	}
	if got := m.multiViewProjects(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("visible panels = %v, want [alpha]", got)
	}
}

// TestMultiViewPanelWidthsSumToTerminalWidth is the regression test for the
// bug behind the rightmost pane's right border getting clipped: each panel's
// panelBorder.Width(w) call actually renders at w+2 (the border adds 2
// columns beyond what's passed to Width()), so naively splitting m.width by
// n and using that directly made n panels' real widths sum to
// m.width+2n — silently truncated off the right edge by renderMultiView's
// final MaxWidth(m.width). multiViewPanelWidths must reserve that overhead so
// the real rendered total lands exactly on width, never over it.
func TestMultiViewPanelWidthsSumToTerminalWidth(t *testing.T) {
	for _, tc := range []struct{ width, n int }{
		{68, 2}, {80, 2}, {100, 2}, {101, 2}, {103, 3}, {102, 3},
	} {
		widths := multiViewPanelWidths(tc.width, tc.n)
		if len(widths) != tc.n {
			t.Fatalf("width=%d n=%d: got %d widths, want %d", tc.width, tc.n, len(widths), tc.n)
		}
		sum := 0
		for _, w := range widths {
			sum += w + 2 // +2 per panel: the actual rendered width via panelBorder.Width(w)
		}
		if sum > tc.width {
			t.Fatalf("width=%d n=%d: real rendered total = %d, overflows terminal width", tc.width, tc.n, sum)
		}
	}
}

// TestMultiViewSessionKeysActOnFocusedPanel is the regression test for the
// core bug report: before delegateToList existed, updateMultiView only
// handled a handful of navigation keys (Tab/Shift-Tab/Up/Down/Open/Cancel) —
// every ordinary session key binding (d, a, t, e, x, r, …) fell through
// unmatched and did nothing at all. It also guards the trickier part of the
// fix: the focused session must be identified by ID, not position, since
// multiViewSessionsFor (what the panel renders) and refreshSessions'
// m.sessions (what updateList's handlers act on) can order a project's
// sessions differently.
func TestMultiViewSessionKeysActOnFocusedPanel(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
		{ID: "b2", Project: "beta", Name: "b2"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 1           // beta
	m.multiCursors["beta"] = 1 // b2

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	drainAll(m, cmd)
	if m.mode != ModeConfirmDelete {
		t.Fatalf("mode after 'd' = %v, want ModeConfirmDelete", m.mode)
	}
	run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(be.deleteCalls) != 1 || be.deleteCalls[0] != "b2" {
		t.Fatalf("deleteCalls = %v, want [b2] (the focused panel's selected session)", be.deleteCalls)
	}
	if m.mode != ModeMultiView {
		t.Fatalf("mode after delete = %v, want back to ModeMultiView", m.mode)
	}
}

// TestMultiViewArchiveStaysInMultiView is the regression test confirming an
// immediate (non-dialog) session action — archive — both reaches the right
// session and doesn't kick the user out of ModeMultiView, since it never
// opens an overlay to begin with.
func TestMultiViewArchiveStaysInMultiView(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 1 // beta

	_, cmd := m.updateMultiView(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("expected an archive command")
	}
	drainCmd(m, cmd)
	if len(be.archiveCalls) != 1 || be.archiveCalls[0].id != "b1" {
		t.Fatalf("archiveCalls = %v, want [{b1 true}]", be.archiveCalls)
	}
	if m.mode != ModeMultiView {
		t.Fatalf("mode = %v, want to stay in ModeMultiView", m.mode)
	}
}

// TestMultiViewSettingsEscReturnsToMultiView is the regression test for
// openSettings/updateSettings not routing through sessionDialogReturn the
// way every other dialog does: opening Settings from ModeMultiView and
// backing out with Esc used to hardcode ModeList, silently dropping the
// user out of multi-view.
func TestMultiViewSettingsEscReturnsToMultiView(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView

	run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.mode != ModeSettings {
		t.Fatalf("mode after 's' = %v, want ModeSettings", m.mode)
	}
	run(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModeMultiView {
		t.Fatalf("mode after esc = %v, want back to ModeMultiView", m.mode)
	}
}

// TestMultiViewSettingsThemePickerEscReturnsToMultiView guards the same
// sessionDialogReturn field against a second clobber: drilling from Settings
// into the theme picker and backing out of both must still land back in
// ModeMultiView, not ModeList or a stuck ModeSettings.
func TestMultiViewSettingsThemePickerEscReturnsToMultiView(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView

	run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	run(m, tea.KeyMsg{Type: tea.KeyDown}) // sort mode -> theme & appearance
	run(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != ModeThemePicker {
		t.Fatalf("mode after enter on theme row = %v, want ModeThemePicker", m.mode)
	}

	run(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModeSettings {
		t.Fatalf("mode after esc from theme picker = %v, want ModeSettings", m.mode)
	}

	run(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModeMultiView {
		t.Fatalf("mode after esc from settings = %v, want back to ModeMultiView", m.mode)
	}
}

// TestMultiViewTagFormRoundTripsToMultiView checks the tag dialog (opened
// with 't') both prefills from the focused panel's session and returns to
// ModeMultiView, not ModeList, once submitted.
func TestMultiViewTagFormRoundTripsToMultiView(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1", Ticket: "https://tracker/OLD"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 1 // beta

	m.updateMultiView(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if m.mode != ModeTagForm {
		t.Fatalf("mode after 't' = %v, want ModeTagForm", m.mode)
	}
	if got := m.tagForm.inputs[0].Value(); got != "https://tracker/OLD" {
		t.Fatalf("tag form ticket = %q, want prefilled from b1", got)
	}
	run(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(be.tagCalls) != 1 || be.tagCalls[0].id != "b1" {
		t.Fatalf("tagCalls = %v, want [{b1 ...}]", be.tagCalls)
	}
	if m.mode != ModeMultiView {
		t.Fatalf("mode after tag submit = %v, want back to ModeMultiView", m.mode)
	}
}

// TestMultiViewSearchJumpRoundTripsToMultiView is the regression test for
// jumping to a session via the 'f' search dialog while in ModeMultiView: it
// must return to ModeMultiView (not fall back to ModeList) and must focus
// the jumped-to session's own panel/row, not leave the previously-focused
// panel's stale selection in place.
func TestMultiViewSearchJumpRoundTripsToMultiView(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha

	run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.mode != ModeSearch {
		t.Fatalf("mode after 'f' = %v, want ModeSearch", m.mode)
	}
	typeText(m, "b1")
	run(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.mode != ModeMultiView {
		t.Fatalf("mode after search jump = %v, want back to ModeMultiView", m.mode)
	}
	if m.multiFocus != 1 {
		t.Fatalf("multiFocus after jumping to b1 = %d, want 1 (beta's panel)", m.multiFocus)
	}
	if got := m.multiCursors["beta"]; got != 0 {
		t.Fatalf("multiCursors[beta] = %d, want 0 (b1 selected)", got)
	}
}

// TestMultiViewEligibilityFollowsShowArchived is the regression test for
// switching the archived toggle: multiViewEligibleProjects must flip which
// projects qualify along with it — a project with only archived sessions is
// eligible while viewing the archived list, and a project with only active
// ones drops out, the mirror image of the default (active) view.
func TestMultiViewEligibilityFollowsShowArchived(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},                // active only
		{ID: "b1", Project: "beta", Name: "b1", Archived: true}, // archived only
	}}
	m := newMultiProjectTestModel(be) // alpha, beta
	m.width = 200

	if got := m.multiViewEligibleProjects(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("active view: eligible = %v, want [alpha]", got)
	}

	m.showArchived = true
	if got := m.multiViewEligibleProjects(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("archived view: eligible = %v, want [beta]", got)
	}
}

// TestProjectPickerSelectionPinsProjectInMultiView is the regression test
// for picking a project with nothing to show yet (no sessions matching the
// current archived view): without pinning it, multi-view's eligible-project
// filter would just leave it invisible, silently discarding the picker's
// selection instead of landing on it.
func TestProjectPickerSelectionPinsProjectInMultiView(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
	}}
	m := newMultiProjectTestModel(be) // alpha (has a session), beta (empty)
	m.mode = ModeMultiView
	m.multiFocus = 0 // alpha

	if got := m.multiViewEligibleProjects(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("eligible before picking = %v, want [alpha] (beta has no sessions)", got)
	}

	m.Update(slashKey())
	if m.mode != ModeProjectPicker {
		t.Fatalf("mode after / = %v, want ModeProjectPicker", m.mode)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // beta
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.mode != ModeMultiView {
		t.Fatalf("mode after picking beta = %v, want back to ModeMultiView", m.mode)
	}
	projs := m.multiViewEligibleProjects()
	if len(projs) != 2 || projs[1] != "beta" {
		t.Fatalf("eligible after picking beta = %v, want [alpha beta] (beta pinned)", projs)
	}
	if got, ok := m.focusedMultiProject(); !ok || got != "beta" {
		t.Fatalf("focused project = %q (ok=%v), want beta", got, ok)
	}

	// Tab away from the pinned, still-empty beta: it must drop back out of
	// the eligible list, and focus must land on a real project (alpha) —
	// not wherever beta's now-removed index used to point.
	m.updateMultiView(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.multiViewEligibleProjects(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("eligible after tabbing away = %v, want [alpha] (beta unpinned)", got)
	}
	if got, ok := m.focusedMultiProject(); !ok || got != "alpha" {
		t.Fatalf("focused project after tab = %q (ok=%v), want alpha", got, ok)
	}
}

// TestSessionCreatedSwitchesToActiveViewAndSelectsItsProject is the
// regression test for a new session getting lost after creation: if the
// user was viewing archived sessions, or focused on some other project, the
// brand-new (necessarily non-archived) session needs to actually be visible
// — both by switching back to the active view and by selecting/pinning its
// project in multi-view, the same as picking it from the project picker.
func TestSessionCreatedSwitchesToActiveViewAndSelectsItsProject(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // alpha, beta — both empty
	m.mode = ModeMultiView
	m.showArchived = true
	m.multiFocus = 1 // beta

	m.Update(SessionCreatedMsg{Session: session.Session{ID: "alpha:new", Project: "alpha", Name: "new"}})

	if m.showArchived {
		t.Fatal("showArchived is still true after creating a session")
	}
	if got, ok := m.focusedMultiProject(); !ok || got != "alpha" {
		t.Fatalf("focused project = %q (ok=%v), want alpha (the new session's project)", got, ok)
	}
}

func TestMultiViewOpenUsesFocusedPanelSession(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "a1", Project: "alpha", Name: "a1"},
		{ID: "b1", Project: "beta", Name: "b1"},
	}}
	m := newMultiProjectTestModel(be)
	m.mode = ModeMultiView
	m.multiFocus = 1 // beta

	_, cmd := m.updateMultiView(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected an open command")
	}
	drainCmd(m, cmd)
	if len(be.openCalls) != 1 || be.openCalls[0] != "b1" {
		t.Fatalf("openCalls = %v, want [b1]", be.openCalls)
	}
}
