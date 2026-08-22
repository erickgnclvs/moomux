package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
)

// multiPanelMinWidth is the narrowest a project's panel is allowed to get in
// ModeMultiView before another one is dropped rather than squeezed —
// multiViewPanelCount() divides m.width by this to decide how many projects
// fit side by side at once.
const multiPanelMinWidth = 34

// multiViewEligibleProjects returns m.projects filtered down to those with
// at least one session matching the current showArchived view — a project
// with nothing to show in that view isn't worth dedicating a whole panel to.
// Normally that means at least one non-archived (active) session; toggling
// A to view archived sessions flips this to at least one archived one, so
// switching to the archived view doesn't leave every panel empty.
//
// m.multiPinned is the one exception: a project jumped to via the picker
// (see updateProjectPicker's Open case) is included regardless of whether it
// has anything to show, so picking an empty project actually lands you on
// it instead of silently doing nothing. The pin doesn't survive navigating
// away — see updateMultiView's Tab/Shift-Tab handling — so it's a one-shot
// "show me this one" rather than a permanent exception.
func (m *Model) multiViewEligibleProjects() []string {
	if len(m.projects) == 0 {
		return nil
	}
	has := make(map[string]bool, len(m.projects))
	for _, s := range m.backend.Sessions() {
		if s.Archived == m.showArchived {
			has[s.Project] = true
		}
	}
	var out []string
	for _, p := range m.projects {
		if has[p] || (m.multiPinned != "" && p == m.multiPinned) {
			out = append(out, p)
		}
	}
	return out
}

// multiViewPanelCount is how many project panels fit side by side at the
// current width — always at least 1 (a panel is squeezed rather than
// dropped entirely) and never more than there are eligible projects.
func (m *Model) multiViewPanelCount() int {
	projs := m.multiViewEligibleProjects()
	if len(projs) == 0 {
		return 0
	}
	n := m.width / multiPanelMinWidth
	if n < 1 {
		n = 1
	}
	if n > len(projs) {
		n = len(projs)
	}
	return n
}

// multiViewProjects returns the window of eligible projects currently
// visible in ModeMultiView: multiViewPanelCount() of them starting at
// m.multiOffset, clamped so the window never runs past the end of the
// eligible list (e.g. after a project's last session is archived, or the
// terminal widens and more panels now fit). m.multiFocus is a global index
// into that eligible list, not this slice — it can point outside the
// window, which is exactly what tells ensureMultiFocusVisible to slide.
func (m *Model) multiViewProjects() []string {
	projs := m.multiViewEligibleProjects()
	n := m.multiViewPanelCount()
	if n == 0 {
		return nil
	}
	offset := m.multiOffset
	if max := len(projs) - n; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	m.multiOffset = offset
	return projs[offset : offset+n]
}

// ensureMultiFocusVisible slides m.multiOffset just far enough to bring
// m.multiFocus back inside the visible window — called after every change to
// multiFocus (Tab/Shift-Tab) and once before rendering, since a terminal
// resize (or a project losing its last active session) can change the
// eligible/visible set without touching multiFocus itself.
func (m *Model) ensureMultiFocusVisible() {
	projs := m.multiViewEligibleProjects()
	n := m.multiViewPanelCount()
	if n == 0 || len(projs) == 0 {
		return
	}
	if m.multiFocus < 0 || m.multiFocus >= len(projs) {
		m.multiFocus = 0
	}
	if m.multiFocus < m.multiOffset {
		m.multiOffset = m.multiFocus
	} else if m.multiFocus >= m.multiOffset+n {
		m.multiOffset = m.multiFocus - n + 1
	}
}

// multiViewSessionsFor returns proj's sessions matching the current
// showArchived filter, live-tmux sessions floated to the top — mirrors
// refreshSessions' filter and sort, but per-project rather than folded into
// the single m.sessions list. Matching that sort matters: delegateToList
// navigates the focused panel's selection through m.sessions (see
// enterSingleProjectContext), so if this returned a different order the
// panel would render, Up/Down would visibly skip over rows instead of
// moving to the adjacent one.
func (m *Model) multiViewSessionsFor(proj string) []session.Session {
	var out []session.Session
	for _, s := range m.backend.Sessions() {
		if s.Project == proj && s.Archived == m.showArchived {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return m.tmuxAlive[out[i].ID] && !m.tmuxAlive[out[j].ID]
	})
	return out
}

// multiCursorFor returns proj's clamped selection within its own panel.
func (m *Model) multiCursorFor(proj string) int {
	sessions := m.multiViewSessionsFor(proj)
	if len(sessions) == 0 {
		return 0
	}
	c := m.multiCursors[proj]
	if c < 0 {
		c = 0
	}
	if c >= len(sessions) {
		c = len(sessions) - 1
	}
	return c
}

// focusedMultiProject returns the project name m.multiFocus currently points
// at (a global index into multiViewEligibleProjects() — see
// multiViewProjects), clamping it back in range first (a project can
// disappear out from under it, e.g. its last active session gets archived).
func (m *Model) focusedMultiProject() (string, bool) {
	projs := m.multiViewEligibleProjects()
	if len(projs) == 0 {
		return "", false
	}
	if m.multiFocus < 0 || m.multiFocus >= len(projs) {
		m.multiFocus = 0
	}
	return projs[m.multiFocus], true
}

// updateMultiView handles ModeMultiView's own panel/focus navigation (Tab/
// Shift-Tab or Left/Right arrows sliding between projects) and hands
// everything else — every
// ordinary session key binding (new/delete/archive/tag/edit/park/refresh/
// reorder/…) — to delegateToList, so those don't need a second, parallel
// implementation. ModeMultiView is the app's primary/root mode (see New()),
// so unlike a dialog there's no "cancel" action at this level — Esc falls
// through to delegateToList same as any other key, where it's a no-op.
func (m *Model) updateMultiView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.cancelPoll()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.NextProject), key.Matches(msg, m.keys.Right):
		m.advanceMultiFocus(1)
		return m, nil
	case key.Matches(msg, m.keys.ShiftTab), key.Matches(msg, m.keys.PrevProject), key.Matches(msg, m.keys.Left):
		m.advanceMultiFocus(-1)
		return m, nil
	}
	return m.delegateToList(msg)
}

// advanceMultiFocus moves multi-view's focus by delta among eligible
// projects (wrapping) — see focusMultiProject for how the target is applied.
func (m *Model) advanceMultiFocus(delta int) {
	projs := m.multiViewEligibleProjects()
	n := len(projs)
	if n == 0 {
		return
	}
	m.focusMultiProject(projs[(m.multiFocus+delta+n)%n])
}

// focusMultiProject points m.multiFocus at the named project (a click on its
// panel, or a Tab/Shift-Tab/arrow step landing on it). If that's a different
// project than m.multiPinned, the pin is cleared — moving focus means "to a
// different real project," and a pinned empty one only exists to be looked
// at once (see updateProjectPicker's Open case), not to stay in rotation.
//
// The target is tracked by name, not index: clearing the pin can shrink the
// eligible list out from under whatever index the caller computed (every
// project after the removed pin shifts left by one), so the post-clear index
// is looked up by name instead of reused.
func (m *Model) focusMultiProject(name string) {
	if name != m.multiPinned {
		m.multiPinned = ""
	}
	if idx := indexOfProject(m.multiViewEligibleProjects(), name); idx >= 0 {
		m.multiFocus = idx
	}
	m.ensureMultiFocusVisible()
}

// projectForSession returns the project owning session id, scanning the
// backend's full session list rather than m.sessions — a mouse hit's session
// ID isn't scoped to whichever project happens to be "entered" via
// enterSingleProjectContext at click time.
func (m *Model) projectForSession(id string) (string, bool) {
	for _, s := range m.backend.Sessions() {
		if s.ID == id {
			return s.Project, true
		}
	}
	return "", false
}

// delegateToList runs msg through updateList as if the focused panel's
// project were the sole active one — the normal single-project key
// handling (new/delete/archive/tag/edit-session/park/refresh/reorder/help/
// …) applied to that panel's own selection — then folds the result back
// into ModeMultiView's per-panel state.
//
// The focused session is carried across by ID rather than index in both
// directions — see enterSingleProjectContext for why.
//
// focusedMultiProject can fail with no eligible project to sync to at all —
// e.g. every configured project is empty, which is exactly the state a
// brand-new project is in right up until its first session gets created.
// That isn't a reason to swallow the keypress: updateList still needs to run
// against whatever m.activeProj/m.sessions already are (same as ModeList),
// so 'n' (among others) keeps working with nothing "eligible" yet.
func (m *Model) delegateToList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	proj, hasFocus := m.focusedMultiProject()
	if hasFocus {
		m.enterSingleProjectContext(proj)
	}

	newModel, cmd := m.updateList(msg)

	if hasFocus {
		m.leaveSingleProjectContext(proj)
	}
	// Every dialog updateList can open defaults its own "return to" field
	// (sessionDialogReturn or the separate projectDialogReturn used by the
	// nested project-form flows) to ModeList, since that's genuinely where
	// they should land when opened from a plain ModeList. Bump that default
	// to ModeMultiView here — but only when it's still the default: the
	// project picker sets projectDialogReturn to ModeProjectPicker for its
	// own nested add/edit/delete flows, and that must NOT be clobbered into
	// bouncing out to multi-view instead of back to the picker.
	switch m.mode {
	case ModeNewForm, ModeConfirmDelete, ModeTagForm, ModeEditSession, ModeHelp, ModeProjectPicker, ModeThemePicker, ModeSearch, ModeSettings:
		if m.sessionDialogReturn == ModeList {
			m.sessionDialogReturn = ModeMultiView
		}
	case ModeConfirmDeleteProject, ModeNewProject, ModeEditProject, ModeProjectInitChoice:
		if m.projectDialogReturn == ModeList {
			m.projectDialogReturn = ModeMultiView
		}
	}
	return newModel, cmd
}

// enterSingleProjectContext points the model's single-active-project state
// (m.activeProj/m.sessions/m.cursor — what updateList's handlers and
// renderListView act on) at proj, carrying over whichever session proj's own
// multi-view panel had focused. Returns false if proj isn't a real project
// (nothing to enter).
//
// The focused session is carried across by ID, not index:
// multiViewSessionsFor (what panels render from) and refreshSessions'
// m.sessions (what updateList/renderListView act on) can order the same
// project's sessions differently — the latter sorts live-tmux sessions to
// the top, the former doesn't — so translating m.multiCursors[proj] into
// m.cursor by position instead of ID could select the wrong session
// entirely.
func (m *Model) enterSingleProjectContext(proj string) bool {
	projIdx := indexOfProject(m.projects, proj)
	if projIdx < 0 {
		return false
	}

	panelSessions := m.multiViewSessionsFor(proj)
	var focusedID string
	if cursor := m.multiCursorFor(proj); cursor < len(panelSessions) {
		focusedID = panelSessions[cursor].ID
	}

	m.activeProj = projIdx
	m.refreshSessions()
	if focusedID != "" {
		m.focusSession(focusedID)
	}
	return true
}

// leaveSingleProjectContext folds m.cursor's current selection back into
// proj's own multi-view panel state (m.multiCursors), the reverse of
// enterSingleProjectContext — called after an action might have moved it
// (reordering, session creation/deletion, …).
func (m *Model) leaveSingleProjectContext(proj string) {
	if m.cursor >= len(m.sessions) {
		return
	}
	selID := m.sessions[m.cursor].ID
	for i, s := range m.multiViewSessionsFor(proj) {
		if s.ID == selID {
			m.multiCursors[proj] = i
			break
		}
	}
}

// refreshSessionsAndSync calls refreshSessions, then — if we're back in
// ModeMultiView — folds the result into that project's panel state via
// leaveSingleProjectContext. Delete/archive/tag act on m.sessions/m.cursor
// and finish asynchronously (the backend call returns a message later), by
// which point the dialog has already flipped m.mode back to ModeMultiView;
// unlike a plain keypress (wrapped end-to-end by delegateToList), nothing
// else folds that final state back into m.multiCursors, so the multi-view
// panel would otherwise keep showing whatever was selected before the
// action completed.
func (m *Model) refreshSessionsAndSync() {
	m.refreshSessions()
	if m.mode == ModeMultiView && m.activeProj < len(m.projects) {
		m.leaveSingleProjectContext(m.projects[m.activeProj])
	}
}

// renderMultiView is ModeMultiView's whole-screen render, replacing the
// normal single-project body with one panel per visible project — the base
// View() dispatches straight here instead of computing the usual list+detail
// layout. It records its own link/row/panel hits directly into m.linkHits/
// m.rowHits/m.panelHits (in absolute terminal coordinates, one panel-local
// origin per panel) rather than going through updateLinkHits, which only
// knows how to place a single list+detail pair.
func (m *Model) renderMultiView() string {
	m.ensureMultiFocusVisible()

	// Only one project's panel would show — either it's the only eligible
	// one, or the terminal isn't wide enough to fit a second side by side.
	// A single cramped panel with its own tiny stacked list+detail isn't
	// worth it when the classic side-by-side (or its own narrow stacked
	// fallback) already does this exact job, so just reuse it.
	if m.multiViewPanelCount() <= 1 {
		if proj, ok := m.focusedMultiProject(); ok {
			m.enterSingleProjectContext(proj)
		}
		return m.renderListView()
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 2
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	m.linkHits = nil
	m.rowHits = nil
	m.panelHits = nil

	projs := m.multiViewProjects()
	offset := m.multiOffset
	var body string
	if len(projs) == 0 {
		w := m.width - 2
		if w < 1 {
			w = 1
		}
		empty := "  no projects yet — press / then n to add one"
		if len(m.projects) > 0 {
			empty = "  no projects with active sessions"
			if m.showArchived {
				empty = "  no projects with archived sessions"
			}
		}
		content := muteStyle.Render(empty)
		body = panelBorder.Width(w).Height(bodyHeight).Render(content)
	} else {
		n := len(projs)
		widths := multiViewPanelWidths(m.width, n)
		panels := make([]string, n)
		panelY := lipgloss.Height(header) + panelBorder.GetBorderTopSize()
		panelX := 0
		for i, proj := range projs {
			w := widths[i]
			focused := offset+i == m.multiFocus
			sessions := m.multiViewSessionsFor(proj)
			cursor := m.multiCursorFor(proj)
			content, hits, rows := m.renderMultiPanel(proj, sessions, cursor, w-2, bodyHeight, focused)
			border := panelBorder
			if focused {
				border = border.BorderForeground(colAccent)
			}
			panels[i] = border.Width(w).Height(bodyHeight).Render(content)
			originX := panelX + panelBorder.GetBorderLeftSize() + panelBorder.GetPaddingLeft()
			for _, h := range hits {
				m.linkHits = append(m.linkHits, resolvedLinkHit{
					sessionID: h.sessionID,
					url:       h.url,
					copyOnly:  h.copyOnly,
					y:         panelY + h.line,
					x0:        originX + h.col0,
					x1:        originX + h.col1,
				})
			}
			for _, r := range rows {
				m.rowHits = append(m.rowHits, resolvedRowHit{
					sessionID: r.sessionID,
					y:         panelY + r.line,
					x0:        originX,
					x1:        originX + w - 2,
				})
			}
			// The panel's own full rectangle, border included (+2 in each
			// dimension, matching panelBorder's NormalBorder), so a click
			// anywhere in it — the detail pane, an empty list, the title
			// line, even the border itself — resolves back to this project.
			m.panelHits = append(m.panelHits, resolvedPanelHit{
				project: proj,
				x0:      panelX,
				x1:      panelX + w + 2,
				y0:      lipgloss.Height(header),
				y1:      lipgloss.Height(header) + bodyHeight + 2,
			})
			// +2 for this panel's own border, matching the +2*n reserved by
			// multiViewPanelWidths so successive panels' origins line up
			// with where lipgloss.JoinHorizontal actually places them.
			panelX += w + 2
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, panels...)
	}

	base := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	return lipgloss.NewStyle().MaxHeight(m.height).MaxWidth(m.width).Render(base)
}

// multiViewPanelWidths splits width across n side-by-side panels, returning
// the value each should pass to panelBorder.Width(). Each panel's border
// adds 2 columns beyond the width given to Width() (see detailW's own "-4"
// in the two-panel list+detail layout for the same reason), so naively
// dividing width by n and handing that straight to Width() would make the n
// panels' actual rendered widths sum to width+2n — overflowing the terminal
// by 2 columns per panel, silently clipped off the right edge by the
// final MaxWidth(m.width) render. Reserving 2*n up front keeps the sum
// exact.
func multiViewPanelWidths(width, n int) []int {
	if n <= 0 {
		return nil
	}
	avail := width - 2*n
	panelW := avail / n
	if panelW < 10 {
		panelW = 10
	}
	widths := make([]int, n)
	for i := range widths {
		widths[i] = panelW
	}
	// Give the remainder to the last panel instead of leaving a dead-space
	// gap when avail doesn't divide evenly.
	if last := avail - panelW*(n-1); last >= 10 {
		widths[n-1] = last
	}
	return widths
}

// minMultiDetailHeight is the smallest a panel's detail section is worth
// showing at — below it, renderMultiPanel gives the whole panel to the list
// instead, mirroring the narrow single-project layout's own
// minStackedPaneHeight guard. One row lower than it used to be: the
// "archived" detail row is gone for good now, not just relocated.
const minMultiDetailHeight = 5

// renderMultiPanel stacks a project's session list over the detail of its
// currently selected session — the same list+detail split the narrow
// mobile layout uses for a single project, repeated per panel so each
// project's own selection is visible without needing to tab to it first.
// The two are divided by a thin separator line rather than a "DETAIL"
// title, mirroring the narrow single-project layout's own treatment.
func (m *Model) renderMultiPanel(proj string, sessions []session.Session, cursor int, width, height int, focused bool) (string, []linkHit, []rowHit) {
	avail := height - 1 // reserve one row for the separator
	if avail < minStackedPaneHeight+minMultiDetailHeight {
		return m.renderSessionPanel(proj, sessions, cursor, width, height, focused)
	}

	var sel session.Session
	hasSel := cursor < len(sessions)
	if hasSel {
		sel = sessions[cursor]
	}

	// detailH is sized around what the selected session's detail content
	// actually needs (fields, wrapped prompt, closing cowsay art) rather
	// than a flat fraction of the panel — a project with a long, actively-
	// scrolled session list (the whole point of this app) used to starve its
	// own detail section down to minMultiDetailHeight regardless of how much
	// room the terminal had, so the cow tacked onto the bottom of detail's
	// content never got to render. It's still capped so a chatty session
	// (long ticket/PR/prompt fields) can't push the list below a usable
	// minimum in a short terminal.
	detailH := m.maxDetailContentHeight(width)
	if maxDetailH := avail - minStackedListRows; detailH > maxDetailH {
		detailH = maxDetailH
	}
	if detailH < minMultiDetailHeight {
		detailH = minMultiDetailHeight
	}
	listH := avail - detailH
	list, hits, rows := m.renderSessionPanel(proj, sessions, cursor, width, listH, focused)

	detail, detailHits := m.renderDetailFor(sel, hasSel, width, detailH, false)
	// detail sits below the list and its one-row separator, so its hits
	// (relative to the detail panel's own top) need that offset folded in
	// to land in the combined panel's coordinates.
	detailOffset := listH + 1
	for _, h := range detailHits {
		h.line += detailOffset
		hits = append(hits, h)
	}
	separator := lipgloss.NewStyle().Foreground(colBorder).Render(strings.Repeat("─", width))
	return lipgloss.JoinVertical(lipgloss.Left, list, separator, detail), hits, rows
}

// renderSessionPanel renders one project's session list for ModeMultiView.
// It mirrors renderList's row styling (and, like renderList, returns the
// ticket/PR icon and row hitboxes in panel-local coordinates) but is driven
// by explicit sessions/cursor arguments instead of the model's single
// active-project state, since multi-view shows several projects' lists at
// once.
func (m *Model) renderSessionPanel(proj string, sessions []session.Session, cursor int, width, height int, focused bool) (string, []linkHit, []rowHit) {
	var b strings.Builder
	title := m.projectEmoji(proj) + " " + proj
	compact := m.compactScreen()
	titleRows := 0
	if !compact {
		style := titleStyle
		if !focused {
			style = style.Foreground(colMute)
		}
		b.WriteString(style.Render(title))
		b.WriteString("\n\n")
		titleRows = 2
	}
	if len(sessions) == 0 {
		b.WriteString(muteStyle.Render("  no sessions"))
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String()), nil, nil
	}

	visible := height - titleRows
	if visible < 1 {
		visible = 1
	}
	start, end := scrollWindow(cursor, len(sessions), visible)
	hasAbove, hasBelow := start > 0, end < len(sessions)
	rowOffset := 0
	if hasAbove {
		b.WriteString(scrollHintLine("⌃", width))
		b.WriteString("\n")
		rowOffset = 1
	}
	var hits []linkHit
	var rows []rowHit
	for i := start; i < end; i++ {
		s := sessions[i]
		selected := focused && i == cursor
		line := titleRows + rowOffset + (i - start)
		rows = append(rows, rowHit{sessionID: s.ID, line: line})
		row, iconHits := renderRow(s, m.effectiveState(s), width-2, selected, "", m.gitStatus[s.ID])
		for _, h := range iconHits {
			h.sessionID = s.ID
			h.line = line
			// +1 column for the row style's own left padding.
			h.col0++
			h.col1++
			hits = append(hits, h)
		}
		if selected {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	if hasBelow {
		b.WriteString(scrollHintLine("⌄", width))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String()), hits, rows
}
