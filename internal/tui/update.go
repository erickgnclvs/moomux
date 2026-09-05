package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/browser"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// updateTitlesCmd pushes each changed session's new status to its tmux
// window name in the background (rename-window shells out, so this stays
// off the Update goroutine).
func updateTitlesCmd(backend Backend, changed map[string]watcher.State) tea.Cmd {
	return func() tea.Msg {
		for id, st := range changed {
			_ = backend.SetSessionStatusTitle(id, st)
		}
		return nil
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// One backend read per pass — see allSessions.
	m.invalidateSessions()
	switch msg := msg.(type) {
	case UpdateAvailableMsg:
		m.UpdateVersion = msg.Version
		return m, nil

	case UpdateCheckTickMsg:
		return m, tea.Batch(checkUpdateCmd(m.Version), tickUpdateCheck())

	case UpdateAppliedMsg:
		m.updating = false
		m.busy = false
		if msg.Err != nil {
			return m.flashError(msg.Err)
		}
		// main() checks Relaunch once p.Run() returns and execs the
		// freshly-installed binary; can't exec here directly since bubbletea
		// still owns the terminal (raw mode, alt screen) at this point.
		m.Relaunch = true
		return m, tea.Quit

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeFormInputs()
		// Re-reveal the focused control after the usable viewport changes,
		// including when a mobile keyboard opens or closes.
		m.overlayFocus = -1
		return m, nil

	case StatusTickMsg:
		for path, st := range msg.Snap.States {
			m.states[path] = st
		}
		// MultiWatcher fans out one Snapshot per sub-watcher, each covering
		// only its own agent's paths, so we can't replace m.states wholesale
		// here without wiping every other watcher's entries. Instead prune
		// against the full live session set so paths from deleted sessions
		// don't linger forever.
		m.pruneDeadSessions()
		m.refreshSessions()
		if msg.Snap.Err != nil {
			// Surface once rather than re-flashing on every subsequent tick
			// while the same failure persists.
			warning := "status scan warning: " + msg.Snap.Err.Error()
			if m.flash != warning {
				m.setFlash("error", warning)
			}
		}
		changedTitles := map[string]watcher.State{}
		for _, s := range m.allSessions() {
			st := m.effectiveState(s)
			if prev, ok := m.titleState[s.ID]; !ok || prev != st {
				m.titleState[s.ID] = st
				changedTitles[s.ID] = st
			}
		}
		cmds := []tea.Cmd{listenStatus(m.statusCh)}
		if len(changedTitles) > 0 {
			cmds = append(cmds, updateTitlesCmd(m.backend, changedTitles))
		}
		if cmd := m.fetchStaleGitStatusCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.fetchStalePRStatusCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case TmuxTickMsg:
		return m, tea.Batch(refreshStatusCmd(m), tickTmux())

	case StatusRefreshedMsg:
		m.tmuxAlive = msg.TmuxAlive
		// tmuxAlive drives the live-session float in refreshSessions' sort,
		// so a change here has to re-sort m.sessions immediately — otherwise
		// the list only catches up next time something unrelated happens to
		// call refreshSessions(), which reads as a session jumping into the
		// middle of the list out of nowhere.
		m.refreshSessions()
		for id, p := range msg.Prompts {
			if m.prompts[id] == "" {
				m.prompts[id] = p
			}
		}
		if !m.tmuxCheckedOnce {
			// The startup tmux-alive check (fired immediately by Init(), not
			// the routine 2s one) just resolved. Every session's git status
			// is still unfetched at this point (checkedAt is zero), so this
			// covers all of them without waiting for the first watcher tick,
			// which may be a couple seconds off yet.
			m.tmuxCheckedOnce = true
			return m, tea.Batch(m.fetchStaleGitStatusCmd(), m.fetchStalePRStatusCmd())
		}
		return m, nil

	case GitStatusMsg:
		for id, st := range msg.Status {
			delete(m.gitStatusPending, id)
			// Two overlapping fetches for the same session (a routine
			// refresh racing the delete dialog's on-demand check, say) can
			// resolve out of order — never let an older result clobber a
			// fresher one already recorded.
			if cur, ok := m.gitStatus[id]; ok && !st.checkedAt.After(cur.checkedAt) {
				continue
			}
			m.gitStatus[id] = st
		}
		if m.confirmChecking && len(m.sessions) > 0 {
			if st, ok := msg.Status[m.sessions[m.cursor].ID]; ok {
				m.confirmGit = st
				m.confirmChecking = false
			}
		}
		return m, nil

	case ChangeSummaryMsg:
		if m.mode == ModeConfirmDelete && len(m.sessions) > 0 && m.sessions[m.cursor].ID == msg.ID {
			m.confirmSummary = msg.Summary
		}
		return m, nil

	case PRStatusMsg:
		for id, st := range msg.Status {
			delete(m.prStatusPending, id)
			if cur, ok := m.prStatus[id]; ok && !st.checkedAt.After(cur.checkedAt) {
				continue
			}
			m.prStatus[id] = st
		}
		return m, nil

	case StatusChannelClosedMsg:
		m.setFlash("error", "status watcher stopped")
		return m, nil

	case TmuxKilledMsg:
		m.setFlash("info", "parked")
		return m, refreshStatusCmd(m)

	case InfoMsg:
		if m.busy {
			return m, tickFlash()
		}
		dur := infoFlashDuration
		if m.flashKind == "error" {
			dur = errorFlashDuration
		}
		if !m.flashTime.IsZero() && time.Since(m.flashTime) > dur {
			m.flash = ""
			m.flashKind = ""
		}
		return m, tickFlash()

	case ErrorMsg:
		m.busy = false
		m.setError(msg.Err)
		return m, nil

	case CreateFailedMsg:
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		m.busy = false
		m.flash, m.flashKind = "", ""
		m.mode = ModeNewForm
		m.newFormErr = msg.Err.Error()
		m.newFormFocusInput()
		return m, nil

	case SessionCreatedMsg:
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		m.busy = false
		text := "created " + msg.Session.Name
		if msg.Hint != "" {
			text += " — " + msg.Hint
		}
		m.setFlash("info", text)
		// Remove from prompt cache so the next tick scans the new session.
		delete(m.prompts, msg.Session.ID)
		delete(m.promptCheckedAt, msg.Session.ID)
		// A brand-new session is never archived — land on the active view so
		// it's actually visible, regardless of which view was showing before.
		m.showArchived = false
		for i, name := range m.projects {
			if name == msg.Session.Project {
				m.activeProj = i
				break
			}
		}
		m.refreshSessions()
		// Multi-view only shows projects with something to display (see
		// multiViewEligibleProjects); pin the new session's project so it's
		// selected there too, the same as picking it from the project picker.
		m.multiPinned = msg.Session.Project
		if idx := indexOfProject(m.multiViewEligibleProjects(), msg.Session.Project); idx >= 0 {
			m.multiFocus = idx
			m.ensureMultiFocusVisible()
		}
		return m, refreshStatusCmd(m)

	case SessionDeletedMsg:
		text := "deleted"
		if msg.Hint != "" {
			text += " — " + msg.Hint
		}
		m.setFlash("info", text)
		m.refreshSessionsFocusing(msg.NextID)
		return m, refreshStatusCmd(m)

	case SessionArchivedMsg:
		if msg.Err != nil {
			m.setError(msg.Err)
			return m, nil
		}
		if msg.Archived {
			m.setFlash("info", "archived")
		} else {
			m.setFlash("info", "restored")
		}
		m.refreshSessionsFocusing(msg.NextID)
		return m, nil

	case SessionTaggedMsg:
		m.setFlash("info", "tagged "+msg.Session.Name)
		m.refreshSessionsAndSync()
		return m, nil

	case SessionAgentUpdatedMsg:
		m.busy = false
		if msg.Err != nil {
			if m.mode != ModeEditSession {
				// The user closed the form while the update was in flight;
				// don't snap it back open.
				m.setError(msg.Err)
				return m, nil
			}
			m.sessionForm.err = msg.Err.Error()
			return m, nil
		}
		m.refreshSessions()
		m.focusSession(msg.Session.ID)
		delete(m.prompts, msg.Session.ID)
		delete(m.promptCheckedAt, msg.Session.ID)
		m.mode = m.sessionDialogReturn
		m.setFlash("info", "updated session "+msg.Session.Name)
		return m, refreshStatusCmd(m)

	case SessionMovedMsg:
		if msg.Err != nil {
			m.setError(msg.Err)
			return m, nil
		}
		m.refreshSessions()
		m.focusSession(msg.ID)
		return m, nil

	case SessionOpenedMsg:
		// Opening a session bumps its LastOpened, which can sort it to a new
		// position (recently-opened sort, or the tmux-alive float once its
		// window comes up) — follow it there instead of leaving the cursor
		// on the old index.
		m.refreshSessions()
		m.focusSession(msg.ID)
		text := "opened " + msg.ID
		if msg.Hint != "" {
			text += " — " + msg.Hint
		}
		m.setFlash("info", text)
		return m, nil

	case ProjectAddedMsg:
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		switch msg.Kind {
		case "add":
			if msg.Err == nil {
				m.activateProject(msg.Name)
				m.setFlash("info", "added project "+msg.Name)
				m.finishProjectAdded(msg.Name)
				return m, nil
			}
			if errors.Is(msg.Err, gitwt.ErrNotGitRepo) {
				m.pending = pendingProject{name: msg.Name, p: msg.Project}
				m.mode = ModeProjectInitChoice
				m.resetOverlayViewport()
				return m, nil
			}
			m.projForm.err = msg.Err.Error()
			return m, nil
		case "init", "plain":
			// Both are answers to the init-choice dialog, so a failure sends
			// the user back to the form rather than the "add" case's retry
			// into that dialog.
			if msg.Err != nil {
				m.mode = ModeNewProject
				m.projForm.err = msg.Err.Error()
				return m, nil
			}
			m.activateProject(msg.Name)
			if msg.Kind == "init" {
				m.setFlash("info", "initialized git repo + added "+msg.Name)
			} else {
				m.setFlash("info", "added plain (non-git) project "+msg.Name)
			}
			m.finishProjectAdded(msg.Name)
			return m, nil
		}
		return m, nil

	case ProjectMovedMsg:
		if msg.Err != nil {
			m.setError(msg.Err)
			return m, nil
		}
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		// Re-anchor by name rather than index on both cursors: the active
		// project (which may not be the one that just moved, when the
		// reorder came from the picker) and, while the picker is open, its
		// own highlight following the project it just reordered.
		var activeName string
		if m.activeProj < len(m.projects) {
			activeName = m.projects[m.activeProj]
		}
		m.refreshProjects()
		if i := indexOfProject(m.projects, activeName); i >= 0 {
			m.activeProj = i
		}
		if m.mode == ModeProjectPicker {
			if i := indexOfProject(m.projects, msg.Name); i >= 0 {
				m.pickerCursor = i
			}
		}
		if m.mode == ModeMultiView {
			if i := indexOfProject(m.multiViewEligibleProjects(), msg.Name); i >= 0 {
				m.multiFocus = i
			}
		}
		return m, nil

	case ProjectUpdatedMsg:
		m.busy = false
		if msg.Err != nil {
			if m.mode != ModeEditProject {
				m.setError(msg.Err)
				return m, nil
			}
			m.projForm.err = msg.Err.Error()
			return m, nil
		}
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		m.activateProject(msg.Name)
		m.mode = m.projectDialogReturn
		m.setFlash("info", "updated project "+msg.Name)
		return m, nil

	case ProjectRemovedMsg:
		if msg.Err != nil {
			m.mode = m.projectDialogReturn
			m.setFlash("error", msg.Err.Error())
			return m, nil
		}
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		m.refreshProjects()
		m.cursor = 0
		m.refreshSessions()
		m.mode = m.projectDialogReturn
		m.setFlash("info", "removed project "+msg.Name)
		return m, nil

	case ThemeSetMsg:
		if msg.Cfg != nil {
			*m.cfg = *msg.Cfg
		}
		m.setFlash("info", "theme saved: "+msg.Theme+" / "+appearanceLabel(msg.Appearance))
		return m, nil

	case LinkOpenedMsg:
		m.setFlash("info", "opened "+msg.URL)
		return m, nil

	case tea.MouseMsg:
		// ModeMultiView's multi-panel layout records its own link/row hits
		// (see renderMultiView) in the same m.linkHits/m.rowHits consulted
		// below, so it's a clickable surface exactly like ModeList — this
		// only excludes modes with an overlay on top (forms, dialogs),
		// where a click should go to the overlay's viewport instead.
		if m.mode != ModeList && m.mode != ModeMultiView {
			var cmd tea.Cmd
			m.overlayViewport, cmd = m.overlayViewport.Update(msg)
			return m, cmd
		}
		return m.handleListMouse(msg)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			// Quit from anywhere — the per-mode handlers ignore unmatched
			// keys, so without this ctrl+c is swallowed inside every form
			// and dialog. ("q" stays mode-specific: it's typeable text in
			// the form inputs.)
			m.cancelPoll()
			return m, tea.Quit
		}
		if m.mode != ModeList && m.mode != ModeHelp && m.mode != ModeMultiView && isOverlayScrollKey(msg) {
			var cmd tea.Cmd
			m.overlayViewport, cmd = m.overlayViewport.Update(msg)
			return m, cmd
		}
		switch m.mode {
		case ModeNewForm:
			return m.updateNewForm(msg)
		case ModeConfirmDelete:
			return m.updateConfirm(msg)
		case ModeNewProject:
			return m.updateNewProject(msg)
		case ModeConfirmDeleteProject:
			return m.updateConfirmDeleteProject(msg)
		case ModeProjectInitChoice:
			return m.updateProjectInitChoice(msg)
		case ModeTagForm:
			return m.updateTagForm(msg)
		case ModeHelp:
			return m.updateHelp(msg)
		case ModeEditSession:
			return m.updateEditSession(msg)
		case ModeEditProject:
			return m.updateEditProject(msg)
		case ModeProjectPicker:
			return m.updateProjectPicker(msg)
		case ModeThemePicker:
			return m.updateThemePicker(msg)
		case ModeMultiView:
			return m.updateMultiView(msg)
		case ModeSearch:
			return m.updateSearch(msg)
		case ModeSettings:
			return m.updateSettings(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func isOverlayScrollKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		return true
	default:
		return false
	}
}

func (m *Model) resetOverlayViewport() {
	m.overlayViewport.GotoTop()
	m.overlayMode = ModeList
	m.overlayFocus = -1
}

// openNewSessionForm opens the new-session dialog, pre-selecting the
// project it was opened from (and its default agent, unless that project
// requires an explicit choice on every session). With more than one
// project, focus still starts on the project selector so it's easy to see
// and change, it just isn't forced to an empty choice.
func (m *Model) openNewSessionForm() {
	m.mode = ModeNewForm
	m.sessionDialogReturn = ModeList
	m.newFormErr = ""
	m.newFormProjIdx = m.activeProj
	if len(m.projects) > 1 {
		m.newFormFocus = newFormProjFocus
	} else {
		m.newFormFocus = 1 // start below the project picker, at the name field
	}
	m.newFormOpenInBackground = false
	m.newFormDangerous = false
	m.newFormAutoSubmit = m.cfg.AutoSubmitDefault
	m.newFormModelIdx = 0
	m.newFormThinkingIdx = 0
	m.nameInput.SetValue("")
	m.branchInput.SetValue("")
	m.baseBranchInput.SetValue("")
	m.ticketInput.SetValue("")
	m.prInput.SetValue("")
	m.promptInput.SetValue("")
	m.newFormModelInput.SetValue("")
	m.newFormBlurAll()
	m.newFormFocusInput()
	m.resetOverlayViewport()
	m.resizeFormInputs()
	m.newFormApplyProjectDefaults()
}

// newFormApplyProjectDefaults sets the agent selector to the currently
// selected project's default agent, unless that project requires an
// explicit choice on every session. If no project is chosen yet, the agent
// selector is left unset too, since it depends on the project.
func (m *Model) newFormApplyProjectDefaults() {
	if m.newFormProjIdx < 0 {
		m.newFormAgentIdx = -1
		return
	}
	proj := m.projects[m.newFormProjIdx]
	p := m.cfg.Projects[proj]
	if p.PromptAgent {
		m.newFormAgentIdx = -1
	} else {
		m.newFormAgentIdx = m.agentNameIndex(p.AgentName())
		m.newFormDangerous = p.Dangerous
	}
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.cancelPoll()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.mode = ModeHelp
		m.sessionDialogReturn = ModeList
		m.resetOverlayViewport()
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if len(m.sessions) > 0 {
			m.cursor = (m.cursor - 1 + len(m.sessions)) % len(m.sessions)
		}
	case key.Matches(msg, m.keys.Down):
		if len(m.sessions) > 0 {
			m.cursor = (m.cursor + 1) % len(m.sessions)
		}
	case key.Matches(msg, m.keys.MoveUp):
		if m.cfg.SortRecentFirst {
			m.setFlash("info", "manual reorder is off while sorting by most-recently-opened (change in settings, s)")
			return m, nil
		}
		if len(m.sessions) > 0 && m.cursor > 0 {
			return m, m.moveSessionCmd(m.sessions[m.cursor].ID, -1)
		}
	case key.Matches(msg, m.keys.MoveDown):
		if m.cfg.SortRecentFirst {
			m.setFlash("info", "manual reorder is off while sorting by most-recently-opened (change in settings, s)")
			return m, nil
		}
		if len(m.sessions) > 0 && m.cursor < len(m.sessions)-1 {
			return m, m.moveSessionCmd(m.sessions[m.cursor].ID, 1)
		}
	case key.Matches(msg, m.keys.MoveProjLeft):
		if len(m.projects) > 0 && m.activeProj > 0 {
			return m, m.moveProjectCmd(m.projects[m.activeProj], -1)
		}
	case key.Matches(msg, m.keys.MoveProjRight):
		if len(m.projects) > 0 && m.activeProj < len(m.projects)-1 {
			return m, m.moveProjectCmd(m.projects[m.activeProj], 1)
		}
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.NextProject):
		m.switchProject(1)
	case key.Matches(msg, m.keys.ShiftTab), key.Matches(msg, m.keys.PrevProject):
		m.switchProject(-1)
	case key.Matches(msg, m.keys.NextProjectAll):
		if len(m.projects) > 0 {
			m.activeProj = (m.activeProj + 1) % len(m.projects)
			m.cursor = 0
			m.refreshSessions()
		}
	case key.Matches(msg, m.keys.PrevProjectAll):
		if len(m.projects) > 0 {
			m.activeProj = (m.activeProj - 1 + len(m.projects)) % len(m.projects)
			m.cursor = 0
			m.refreshSessions()
		}
	case key.Matches(msg, m.keys.Refresh):
		m.refreshSessions()
		return m, refreshStatusCmd(m)
	case key.Matches(msg, m.keys.RemoteLinks):
		m.forceCopyLinks = !m.forceCopyLinks
		state := "auto"
		if m.forceCopyLinks {
			state = "forced on"
		}
		m.setFlash("info", "ticket/PR links copy instead of open: "+state)
		return m, nil
	case key.Matches(msg, m.keys.Kill):
		if m.busy {
			return m.flashError(fmt.Errorf("still creating the previous session — wait for it to finish"))
		}
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			return m, func() tea.Msg {
				if err := m.backend.KillTmux(id); err != nil {
					return ErrorMsg{Err: err}
				}
				return TmuxKilledMsg{ID: id}
			}
		}
	case key.Matches(msg, m.keys.New):
		if m.busy {
			// A session create is already in flight; opening another form here
			// would let its submit fire a second, concurrent osascript call
			// into iTerm2 before the first tab/window has settled — iTerm2
			// resolves "current window"/"current tab" racily while it's still
			// launching, so the second AppleScript can land its "tmux attach"
			// in the tab the first one just created instead of a new one (see
			// terminal/iterm.go's createTab).
			return m.flashError(fmt.Errorf("still creating the previous session — wait for it to finish"))
		}
		if len(m.projects) == 0 {
			return m.flashError(fmt.Errorf("no projects configured — press / then n to add one"))
		}
		m.openNewSessionForm()
	case key.Matches(msg, m.keys.Delete):
		if m.busy {
			return m.flashError(fmt.Errorf("still creating the previous session — wait for it to finish"))
		}
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			// Opens instantly on whatever's cached from the routine
			// background refresh (or nothing yet) so the dialog never
			// visibly pauses, then kicks off a fresh check of its own in the
			// background — confirmChecking drives a small loading note in
			// the dialog until GitStatusMsg lands and updates confirmGit for
			// real. Deliberately not routed through fetchStaleGitStatusCmd's
			// gitStatusPending bookkeeping: this is an explicit user action
			// that wants the freshest answer right now, not deduped against
			// whatever the periodic refresh happens to be doing.
			m.confirmGit = m.gitStatus[id]
			m.confirmSummary = changeSummary{}
			m.confirmAck = false
			m.confirmChecking = true
			m.mode = ModeConfirmDelete
			m.sessionDialogReturn = ModeList
			// Without this the dialog inherits the previous overlay's scroll
			// offset and can open with the "what you're deleting" text
			// scrolled off-screen, leaving only "y to confirm" visible.
			m.resetOverlayViewport()
			return m, tea.Batch(fetchGitStatusCmd(m.backend, []string{id}), fetchChangeSummaryCmd(m.backend, id))
		}
	case key.Matches(msg, m.keys.Archive):
		if m.busy {
			return m.flashError(fmt.Errorf("still creating the previous session — wait for it to finish"))
		}
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			nextID := m.neighborSessionID()
			archive := !m.showArchived
			return m, func() tea.Msg {
				_, err := m.backend.SetSessionArchived(id, archive)
				return SessionArchivedMsg{ID: id, Archived: archive, NextID: nextID, Err: err}
			}
		}
	case key.Matches(msg, m.keys.ShowArchived):
		m.showArchived = !m.showArchived
		m.cursor = 0
		m.refreshSessions()
	case key.Matches(msg, m.keys.Tag):
		if len(m.sessions) > 0 {
			s := m.sessions[m.cursor]
			m.mode = ModeTagForm
			m.sessionDialogReturn = ModeList
			m.tagForm = newTagForm(s.Ticket, s.PR)
			m.resetOverlayViewport()
			m.resizeFormInputs()
		}
	case key.Matches(msg, m.keys.EditSession):
		if len(m.sessions) > 0 {
			s := m.sessions[m.cursor]
			m.mode = ModeEditSession
			m.sessionDialogReturn = ModeList
			m.resetOverlayViewport()
			m.sessionForm = newSessionForm(s.ID, s.Project, s.Name, m.agentNameIndex(s.AgentName()), s.Dangerous)
			m.resizeFormInputs()
		}
	case key.Matches(msg, m.keys.ProjectPicker):
		// No zero-projects guard: the picker is now the only way to add a
		// project (main list's P/E were removed), so it has to stay reachable
		// even with none yet — its own empty-state hint covers that ("press
		// n to add one").
		m.pickerCursor = m.activeProj
		m.mode = ModeProjectPicker
		m.sessionDialogReturn = ModeList
		m.resetOverlayViewport()
		return m, nil
	case key.Matches(msg, m.keys.Settings):
		m.openSettings()
		return m, nil
	case key.Matches(msg, m.keys.Search):
		m.searchInput.SetValue("")
		m.searchInput.Focus()
		m.searchCursor = 0
		m.refreshSearchResults()
		m.mode = ModeSearch
		m.sessionDialogReturn = ModeList
		m.resetOverlayViewport()
		return m, nil
	case key.Matches(msg, m.keys.DelProject):
		if len(m.projects) == 0 {
			return m.flashError(fmt.Errorf("no projects to remove"))
		}
		if n := m.projectSessionCount(); n > 0 {
			return m.flashError(fmt.Errorf("%s has %d session(s) (incl. archived) — delete them first", m.projects[m.activeProj], n))
		}
		m.projectDialogReturn = ModeList
		m.mode = ModeConfirmDeleteProject
		m.resetOverlayViewport()
		return m, nil
	case key.Matches(msg, m.keys.Open):
		if len(m.sessions) > 0 {
			return m, m.openSessionCmd(m.sessions[m.cursor].ID)
		}
	case key.Matches(msg, m.keys.Update):
		if m.UpdateVersion == "" {
			m.setFlash("info", "already up to date")
			return m, nil
		}
		if m.updating {
			return m, nil
		}
		m.updating = true
		m.busy = true
		m.setFlash("info", "updating to v"+m.UpdateVersion+"…")
		return m, runUpdateCmd()
	}
	return m, nil
}

// switchProject moves the active project by delta, skipping empty ones —
// shared by Tab/Shift-Tab and a horizontal mouse-wheel swipe.
func (m *Model) switchProject(delta int) {
	if len(m.projects) == 0 {
		return
	}
	m.activeProj = m.nextNonEmptyProject(delta)
	m.cursor = 0
	m.refreshSessions()
}

// handleListMouse handles a mouse event against the current list/detail
// view — ModeList, ModeMultiView's single-project fallback, or ModeMultiView's
// real multi-panel grid (see renderListView/renderMultiView). In the
// single-project-fallback case any cursor movement is folded back into the
// focused project's own multi-view panel state before returning:
// renderMultiView's enterSingleProjectContext resyncs m.cursor from that
// state on every render, so without this a click/wheel-scroll would just get
// silently overwritten on the next frame. Ticket/PR icon clicks (via
// m.linkAt) work identically in all three cases — copy-vs-open over SSH
// (m.isRemote) isn't mode-specific, so mobile clients see the same behavior
// whether one panel or several are visible.
//
// A tap on a session row itself no longer opens/attaches it — a stray click
// used to launch a tmux attach unexpectedly, which is disruptive enough that
// it's not worth the tap-to-open convenience. In ModeList it just moves the
// cursor there; in ModeMultiView it does the multi-panel equivalent: pick
// that row's project (m.multiFocus) the same way Tab would. A click that
// lands elsewhere in a panel — the detail pane, an empty list, the title
// line — still picks that project via m.panelHits, just without moving its
// cursor, so anywhere in a project's square is how you pick which one to
// act on.
func (m *Model) handleListMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var proj string
	var hasFocus bool
	if m.mode == ModeMultiView {
		proj, hasFocus = m.focusedMultiProject()
	}
	sync := func() {
		if hasFocus {
			m.leaveSingleProjectContext(proj)
		}
	}

	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if url, copyOnly := m.linkAt(msg.X, msg.Y); url != "" {
			if copyOnly || m.isRemote() {
				// Over SSH, `open` launches a browser on the remote
				// machine, not the phone/laptop the user is actually
				// looking at — and moomux's mouse tracking means the
				// terminal never gets a chance to handle the tap as a
				// link itself. Copy the URL instead: OSC 52 isn't tied
				// to mouse mode, so it reaches the client's clipboard
				// regardless.
				//
				// This runs synchronously (not as a tea.Cmd) because a
				// Cmd executes in its own goroutine, concurrently with
				// bubbletea's render loop — both writing to os.Stdout at
				// once can interleave and corrupt the escape sequence
				// before the terminal ever sees a well-formed one.
				if err := browser.Copy(url); err != nil {
					m.setError(err)
					sync()
					return m, nil
				}
				m.setFlash("info", "copied "+url)
				sync()
				return m, nil
			}
			sync()
			return m, func() tea.Msg {
				if err := browser.Open(url); err != nil {
					return ErrorMsg{Err: err}
				}
				return LinkOpenedMsg{URL: url}
			}
		}
		// Not a ticket/PR icon — a tap on the row selects it (and, in
		// ModeMultiView, picks that row's project as the focused panel).
		if id, ok := m.sessionRowAt(msg.X, msg.Y); ok {
			if m.mode == ModeMultiView {
				if p, ok := m.projectForSession(id); ok {
					m.focusMultiProject(p)
					for i, s := range m.multiViewSessionsFor(p) {
						if s.ID == id {
							m.multiCursors[p] = i
							break
						}
					}
				}
			} else {
				m.focusSession(id)
			}
		} else if m.mode == ModeMultiView {
			// Missed every row (e.g. the detail pane, an empty list, the
			// title line) but still landed inside a panel — pick that
			// project without touching its cursor.
			if p, ok := m.panelAt(msg.X, msg.Y); ok {
				m.focusMultiProject(p)
			}
		}
	}
	sync()
	return m, nil
}

// moveSessionCmd and moveProjectCmd persist a reorder off the event loop.
// A nil Err on the resulting msg is the success case, so the backend call's
// error goes straight into the field rather than being branched on here.
func (m *Model) moveSessionCmd(id string, delta int) tea.Cmd {
	return func() tea.Msg { return SessionMovedMsg{ID: id, Err: m.backend.MoveSession(id, delta)} }
}

// cfgSnapshotOnSuccess returns a fresh ConfigSnapshot for a mutation Msg's
// Cfg field when err is nil, or nil when the mutation failed (nothing
// changed, so Update() has nothing to apply). Only ever call this from
// inside a tea.Cmd closure, right after the mutation call whose err it's
// given — never from Update()/View() directly.
func (m *Model) cfgSnapshotOnSuccess(err error) *config.Config {
	if err != nil {
		return nil
	}
	snap := m.backend.ConfigSnapshot()
	return &snap
}

func (m *Model) moveProjectCmd(name string, delta int) tea.Cmd {
	return func() tea.Msg {
		err := m.backend.MoveProject(name, delta)
		return ProjectMovedMsg{Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
	}
}

// openSessionCmd returns a Cmd that opens/attaches the given session, used
// by the Enter/o key binding.
func (m *Model) openSessionCmd(id string) tea.Cmd {
	return func() tea.Msg {
		hint, err := m.backend.OpenSession(id)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		return SessionOpenedMsg{ID: id, Hint: hint}
	}
}

// updateHelp handles keys while the help overlay is open. Any of ?, esc, or q
// dismisses it; ctrl+c still quits the app outright.
func (m *Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.cancelPoll()
		return m, tea.Quit
	}
	switch {
	case key.Matches(msg, m.keys.Help), key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Quit):
		m.mode = m.sessionDialogReturn
		return m, nil
	}
	var cmd tea.Cmd
	m.overlayViewport, cmd = m.overlayViewport.Update(msg)
	return m, cmd
}

func (m *Model) updateNewForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab):
		if key.Matches(msg, m.keys.ShiftTab) {
			m.newFormMoveFocus(-1)
		} else {
			m.newFormMoveFocus(1)
		}
		return m, nil
	case key.Matches(msg, m.keys.FormDown), key.Matches(msg, m.keys.FormUp):
		// The prompt field is a multi-line textarea — leave ↑/↓ to it for
		// moving the cursor between lines. But if you're on the first
		// or last line, let it switch fields!
		if m.newFormFocus == 4 {
			atTop := m.promptInput.Line() == 0
			atBottom := m.promptInput.Line() == m.promptInput.LineCount()-1
			if (key.Matches(msg, m.keys.FormUp) && !atTop) || (key.Matches(msg, m.keys.FormDown) && !atBottom) {
				break
			}
		}
		if key.Matches(msg, m.keys.FormUp) {
			m.newFormMoveFocus(-1)
		} else {
			m.newFormMoveFocus(1)
		}
		return m, nil
	case key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Right):
		// Only steer the project/agent selectors when one of them is the
		// focused field; otherwise the arrows belong to the text input's
		// cursor.
		if m.newFormFocus == newFormProjFocus {
			if len(m.projects) > 0 {
				if m.newFormProjIdx < 0 {
					m.newFormProjIdx = 0
				} else if key.Matches(msg, m.keys.Left) {
					m.newFormProjIdx = (m.newFormProjIdx - 1 + len(m.projects)) % len(m.projects)
				} else {
					m.newFormProjIdx = (m.newFormProjIdx + 1) % len(m.projects)
				}
				m.newFormApplyProjectDefaults()
			}
			return m, nil
		}
		if m.newFormFocus == newFormAgentFocus {
			if m.newFormAgentIdx < 0 {
				m.newFormAgentIdx = 0
			} else if key.Matches(msg, m.keys.Left) {
				m.newFormAgentIdx = (m.newFormAgentIdx - 1 + len(m.agentNames())) % len(m.agentNames())
			} else {
				m.newFormAgentIdx = (m.newFormAgentIdx + 1) % len(m.agentNames())
			}
			// Each agent has its own model list (or, for opencode, a
			// free-text field) and thinking-level list; a stale choice from a
			// previous agent could point at the wrong value, so reset both
			// whenever the agent changes.
			m.newFormModelIdx = 0
			m.newFormModelInput.SetValue("")
			m.newFormThinkingIdx = 0
			return m, nil
		}
		if m.newFormFocus == newFormModelFocus {
			agent := ""
			if m.newFormAgentIdx >= 0 {
				agent = m.agentNames()[m.newFormAgentIdx]
			}
			if agent == "opencode" {
				// Free-text field: leave ←→ to the text input's own cursor
				// movement, handled by the default routing below.
				break
			}
			names := m.modelNamesFor(agent)
			if key.Matches(msg, m.keys.Left) {
				m.newFormModelIdx = (m.newFormModelIdx - 1 + len(names)) % len(names)
			} else {
				m.newFormModelIdx = (m.newFormModelIdx + 1) % len(names)
			}
			return m, nil
		}
		if m.newFormFocus == newFormThinkingFocus {
			agent := ""
			if m.newFormAgentIdx >= 0 {
				agent = m.agentNames()[m.newFormAgentIdx]
			}
			names := m.thinkingNamesFor(agent)
			if key.Matches(msg, m.keys.Left) {
				m.newFormThinkingIdx = (m.newFormThinkingIdx - 1 + len(names)) % len(names)
			} else {
				m.newFormThinkingIdx = (m.newFormThinkingIdx + 1) % len(names)
			}
			return m, nil
		}
		if m.newFormFocus == newFormDangerousFocus {
			m.newFormDangerous = !m.newFormDangerous
			return m, nil
		}
		if m.newFormFocus == newFormOpenTerminalFocus {
			m.newFormOpenInBackground = !m.newFormOpenInBackground
			return m, nil
		}
		if m.newFormFocus == newFormAutoSubmitFocus {
			m.newFormAutoSubmit = !m.newFormAutoSubmit
			return m, nil
		}
	case key.Matches(msg, m.keys.Enter):
		if m.newFormFocus == 4 {
			// Enter inserts a newline in the multi-line prompt field instead
			// of submitting — tab to another field to submit with Enter.
			break
		}
		if m.newFormProjIdx < 0 {
			m.newFormErr = "choose a project — tab to the project row, then ←→"
			return m, nil
		}
		name := m.nameInput.Value()
		branch := m.branchInput.Value()
		baseBranch := m.baseBranchInput.Value()
		ticket := m.ticketInput.Value()
		pr := m.prInput.Value()
		firstPrompt := m.promptInput.Value()
		if name == "" && branch == "" {
			m.newFormErr = "enter a session name or an existing branch"
			return m, nil
		}
		if m.newFormAgentIdx < 0 {
			m.newFormErr = "this project requires choosing an agent — tab to the agent row, then ←→"
			return m, nil
		}
		m.newFormErr = ""
		proj := m.projects[m.newFormProjIdx]
		agent := m.agentNames()[m.newFormAgentIdx]
		model := m.newFormModelInput.Value()
		if agent != "opencode" {
			model = m.modelNamesFor(agent)[m.newFormModelIdx]
		}
		thinking := m.thinkingNamesFor(agent)[m.newFormThinkingIdx]
		dangerous := m.newFormDangerous
		openTerminal := !m.newFormOpenInBackground
		m.mode = m.sessionDialogReturn
		label := name
		if label == "" {
			label = branch
		}
		m.setFlash("info", "creating "+label+"…")
		m.busy = true
		autoSubmit := m.newFormAutoSubmit
		// Captured here, on the Update goroutine, rather than read from
		// m.cfg inside the closure below — that closure runs concurrently
		// with Update()/View(), which is the exact race this whole Cfg
		// plumbing exists to avoid (see New()'s doc comment on m.cfg).
		autoSubmitDefault := m.cfg.AutoSubmitDefault
		return m, func() tea.Msg {
			var cfgSnap *config.Config
			if autoSubmit != autoSubmitDefault {
				// Best-effort: remembering the new default shouldn't block
				// session creation if the config write fails.
				cfgSnap = m.cfgSnapshotOnSuccess(m.backend.SetAutoSubmitDefault(autoSubmit))
			}
			s, hint, err := m.backend.CreateSession(proj, name, agent, branch, ticket, openTerminal, &dangerous, baseBranch, model, thinking)
			if err != nil {
				return CreateFailedMsg{Err: err, Cfg: cfgSnap}
			}
			// The session (worktree + tmux pane) already exists at this
			// point — a PR-tag or first-prompt failure below must not
			// discard that success and report it as if creation itself
			// failed; it's surfaced as a hint on the same SessionCreatedMsg
			// instead, same as CreateSession's own degraded-but-succeeded
			// terminal-open failures.
			if pr != "" {
				if updated, tagErr := m.backend.SetSessionTags(s.ID, ticket, pr); tagErr != nil {
					hint = joinHint(hint, fmt.Sprintf("couldn't set PR tag: %v", tagErr))
				} else {
					s = updated
				}
			}
			if firstPrompt != "" {
				if agent != "codex" {
					// codex's thinking level is a real -c model_reasoning_effort
					// flag (already applied to the launch command above), not a
					// prompt phrase.
					firstPrompt = thinkingPromptPrefix(thinking) + firstPrompt
				}
				if extra := newFormPromptExtras(ticket, pr); extra != "" {
					firstPrompt += "\n\n" + extra
				}
				if updated, promptErr := m.backend.SetSessionPrompt(s.ID, firstPrompt); promptErr == nil {
					s = updated
				}
				if err := m.backend.StartFirstPrompt(s.TmuxSession, firstPrompt, autoSubmit); err != nil {
					hint = joinHint(hint, fmt.Sprintf("couldn't send first prompt: %v", err))
				}
			}
			return SessionCreatedMsg{Session: s, Hint: hint, Cfg: cfgSnap}
		}
	}
	// Any other focus is a selector or toggle row, with no text input to type
	// into.
	var cmd tea.Cmd
	switch m.newFormFocus {
	case 1:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case 2:
		m.branchInput, cmd = m.branchInput.Update(msg)
	case 3:
		m.baseBranchInput, cmd = m.baseBranchInput.Update(msg)
	case 4:
		m.promptInput, cmd = m.promptInput.Update(msg)
	case 5:
		m.ticketInput, cmd = m.ticketInput.Update(msg)
	case 6:
		m.prInput, cmd = m.prInput.Update(msg)
	case newFormModelFocus:
		if m.newFormAgentIdx >= 0 && m.agentNames()[m.newFormAgentIdx] == "opencode" {
			m.newFormModelInput, cmd = m.newFormModelInput.Update(msg)
		}
	}
	return m, cmd
}

// joinHint combines two non-empty hint strings for display; either may be
// empty.
func joinHint(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " — " + b
	}
}

// newFormPromptExtras builds a "Ticket: ...\nPR: ..." block from whichever
// of ticket/pr are non-empty, so the agent's first task carries the same
// context the session list shows as clickable icons. Empty if neither is
// set.
func newFormPromptExtras(ticket, pr string) string {
	var lines []string
	if ticket != "" {
		lines = append(lines, "Ticket: "+ticket)
	}
	if pr != "" {
		lines = append(lines, "PR: "+pr)
	}
	return strings.Join(lines, "\n")
}

// thinkingPromptPrefix returns level+": " to prepend to the first prompt, or
// "" for "default". There's no CLI flag for extended-thinking effort, so this
// leans on the same magic words a user would type by hand.
// ponytail: a no-op when there's no first prompt to prepend to — the form
// doesn't warn about that combination, it just has no effect.
func thinkingPromptPrefix(level string) string {
	if level == "" || level == "default" {
		return ""
	}
	return level + ": "
}

// newFormMoveFocus blurs the currently focused field and shifts focus by
// delta (wrapping), then focuses whatever field lands there.
func (m *Model) newFormMoveFocus(delta int) {
	m.newFormBlurAll()
	m.newFormFocus = (m.newFormFocus + delta + newFormFieldCount) % newFormFieldCount
	m.newFormFocusInput()
}

func (m *Model) newFormBlurAll() {
	m.nameInput.Blur()
	m.branchInput.Blur()
	m.baseBranchInput.Blur()
	m.ticketInput.Blur()
	m.prInput.Blur()
	m.promptInput.Blur()
	m.newFormModelInput.Blur()
}

// newFormFocusInput focuses the text input at the current focus, if any — the
// selector and toggle rows have nothing to focus. The model row is a text
// input only for opencode; for claude/codex it's a selector, so it's left
// unfocused there.
func (m *Model) newFormFocusInput() {
	switch m.newFormFocus {
	case 1:
		m.nameInput.Focus()
	case 2:
		m.branchInput.Focus()
	case 3:
		m.baseBranchInput.Focus()
	case 4:
		m.promptInput.Focus()
	case 5:
		m.ticketInput.Focus()
	case 6:
		m.prInput.Focus()
	case newFormModelFocus:
		if m.newFormAgentIdx >= 0 && m.agentNames()[m.newFormAgentIdx] == "opencode" {
			m.newFormModelInput.Focus()
		}
	}
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		if len(m.sessions) == 0 {
			m.mode = m.sessionDialogReturn
			return m, nil
		}
		if m.confirmChecking {
			// The fresh check hasn't resolved yet — acting on whatever's
			// cached (possibly nothing, possibly stale) would defeat the
			// point of checking at all. Ignore y until GitStatusMsg lands.
			return m, nil
		}
		if m.confirmGit.ok && (m.confirmGit.dirty || m.confirmGit.unpushed) && !m.confirmAck {
			// First y on a dirty/unpushed worktree only acknowledges the
			// warning; a second y is required to actually delete.
			m.confirmAck = true
			return m, nil
		}
		id := m.sessions[m.cursor].ID
		nextID := m.neighborSessionID()
		m.mode = m.sessionDialogReturn
		return m, func() tea.Msg {
			hint, err := m.backend.DeleteSession(id)
			if err != nil {
				return ErrorMsg{Err: err}
			}
			return SessionDeletedMsg{ID: id, NextID: nextID, Hint: hint}
		}
	case key.Matches(msg, m.keys.No), key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
	}
	return m, nil
}

// cycleFormFocus blurs the currently focused textinput (if any), advances
// *focus by one step (forward or backward) with wraparound over total
// fields, then focuses the newly selected textinput (if any). *focus may
// land on an index >= len(inputs) to represent a non-textinput field (e.g.
// an agent selector); such indices are simply skipped for Blur/Focus.
func cycleFormFocus(inputs []textinput.Model, focus *int, total int, forward bool) {
	if *focus < len(inputs) {
		inputs[*focus].Blur()
	}
	if forward {
		*focus = (*focus + 1) % total
	} else {
		*focus = (*focus - 1 + total) % total
	}
	if *focus < len(inputs) {
		inputs[*focus].Focus()
	}
}

// cycleProjectAgentIdx moves idx (a projectForm.agentIdx, possibly
// askAgentIdx) by delta across agentNames plus the trailing "ask each
// time" entry, wrapping in both directions.
func (m *Model) cycleProjectAgentIdx(idx, delta int) int {
	names := m.agentNames()
	pos := idx
	if pos == askAgentIdx {
		pos = len(names)
	}
	n := len(names) + 1
	pos = (pos + delta + n) % n
	if pos == len(names) {
		return askAgentIdx
	}
	return pos
}

// adjustProjFormField steers the project form's non-text control at the
// current focus by delta (emoji/agent selectors cycle; the dangerous and
// worktree toggles just flip either way), and reports whether focus was on
// one of them at all — false means ←/→ belongs to a text input's cursor.
// Shared by the add- and edit-project forms, which drive identical controls.
func (m *Model) adjustProjFormField(delta int) bool {
	switch m.projForm.focus {
	case projFormInputCount:
		m.projForm.emojiIdx = cycleProjectEmojiIdx(m.projForm.emojiChoices, m.projForm.emojiIdx, delta)
	case projFormInputCount + 1:
		m.projForm.agentIdx = m.cycleProjectAgentIdx(m.projForm.agentIdx, delta)
	case projFormInputCount + 2:
		m.projForm.dangerous = !m.projForm.dangerous
	case projFormInputCount + 3:
		m.projForm.noWorktree = !m.projForm.noWorktree
	default:
		return false
	}
	return true
}

// projectAgentFields returns the Agent, Dangerous and PromptAgent values a
// submitted project form should store, given its agentIdx (possibly
// askAgentIdx) and its independent dangerous toggle.
func (m *Model) projectAgentFields(agentIdx int, dangerous bool) (agent string, dangerousOut, promptAgent bool) {
	if agentIdx == askAgentIdx {
		return "", false, true
	}
	return m.agentNames()[agentIdx], dangerous, false
}

func (m *Model) updateNewProject(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	const totalFields = projFormInputCount + 4 // +1 emoji selector, +1 agent selector, +1 dangerous toggle, +1 worktree toggle
	switch {
	case key.Matches(msg, m.keys.Cancel):
		// projectDialogReturn defaults to ModeList (its zero value), which is
		// also correct for New()'s zero-projects startup case that opens
		// this form directly without going through the picker or main list.
		m.mode = m.projectDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.FormDown):
		cycleFormFocus(m.projForm.inputs, &m.projForm.focus, totalFields, true)
		return m, nil
	case key.Matches(msg, m.keys.ShiftTab), key.Matches(msg, m.keys.FormUp):
		cycleFormFocus(m.projForm.inputs, &m.projForm.focus, totalFields, false)
		return m, nil
	case key.Matches(msg, m.keys.Left):
		if m.adjustProjFormField(-1) {
			return m, nil
		}
	case key.Matches(msg, m.keys.Right):
		if m.adjustProjFormField(1) {
			return m, nil
		}
	case key.Matches(msg, m.keys.Enter):
		name := m.projForm.inputs[0].Value()
		repo := m.projForm.inputs[1].Value()
		base := m.projForm.inputs[2].Value()
		prefix := m.projForm.inputs[3].Value()
		emoji := projectEmojiFieldValue(m.projForm.emojiChoices, m.projForm.emojiIdx)
		if base == "" {
			base = "main"
		}
		agent, dangerous, promptAgent := m.projectAgentFields(m.projForm.agentIdx, m.projForm.dangerous)
		p := config.Project{Repo: repo, BaseBranch: base, BranchPrefix: prefix, Emoji: emoji, Agent: agent, Dangerous: dangerous, PromptAgent: promptAgent, NoWorktree: m.projForm.noWorktree}
		return m, func() tea.Msg {
			err := m.backend.AddProject(name, p)
			return ProjectAddedMsg{Kind: "add", Name: name, Project: p, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	}
	if m.projForm.focus < projFormInputCount {
		var cmd tea.Cmd
		m.projForm.inputs[m.projForm.focus], cmd = m.projForm.inputs[m.projForm.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateEditSession(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		// A background op is in flight (possibly a session create started
		// before this overlay opened); still let the user close the form
		// instead of being stuck in an unresponsive modal.
		if key.Matches(msg, m.keys.Cancel) {
			m.mode = m.sessionDialogReturn
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab),
		key.Matches(msg, m.keys.FormDown), key.Matches(msg, m.keys.FormUp):
		forward := !(key.Matches(msg, m.keys.ShiftTab) || key.Matches(msg, m.keys.FormUp))
		m.sessionForm.nameInput.Blur()
		if forward {
			m.sessionForm.focus = (m.sessionForm.focus + 1) % 3
		} else {
			m.sessionForm.focus = (m.sessionForm.focus + 2) % 3
		}
		if m.sessionForm.focus == sessionFormNameFocus {
			m.sessionForm.nameInput.Focus()
		}
	case key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Right):
		switch m.sessionForm.focus {
		case sessionFormAgentFocus:
			if key.Matches(msg, m.keys.Left) {
				m.sessionForm.agentIdx = (m.sessionForm.agentIdx - 1 + len(m.agentNames())) % len(m.agentNames())
			} else {
				m.sessionForm.agentIdx = (m.sessionForm.agentIdx + 1) % len(m.agentNames())
			}
		case sessionFormDangerousFocus:
			m.sessionForm.dangerous = !m.sessionForm.dangerous
		case sessionFormNameFocus:
			var cmd tea.Cmd
			m.sessionForm.nameInput, cmd = m.sessionForm.nameInput.Update(msg)
			return m, cmd
		}
	case key.Matches(msg, m.keys.Enter):
		id := m.sessionForm.id
		newName := m.sessionForm.nameInput.Value()
		agent := m.agentNames()[m.sessionForm.agentIdx]
		dangerous := m.sessionForm.dangerous
		m.busy = true
		m.sessionForm.err = ""
		return m, func() tea.Msg {
			if _, err := m.backend.RenameSession(id, newName); err != nil {
				return SessionAgentUpdatedMsg{Err: err}
			}
			s, err := m.backend.SetSessionAgent(id, agent, dangerous)
			return SessionAgentUpdatedMsg{Session: s, Err: err}
		}
	default:
		if m.sessionForm.focus == sessionFormNameFocus {
			var cmd tea.Cmd
			m.sessionForm.nameInput, cmd = m.sessionForm.nameInput.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func editProjectFocuses(p config.Project) []int {
	if p.IsPlain() {
		return []int{1, projFormInputCount, projFormInputCount + 1, projFormInputCount + 2}
	}
	return []int{1, 2, 3, projFormInputCount, projFormInputCount + 1, projFormInputCount + 2, projFormInputCount + 3}
}

func (m *Model) cycleEditProjectFocus(forward bool) {
	focuses := editProjectFocuses(m.cfg.Projects[m.editProjectName])
	current := 0
	for i, focus := range focuses {
		if focus == m.projForm.focus {
			current = i
			break
		}
	}
	if m.projForm.focus < len(m.projForm.inputs) {
		m.projForm.inputs[m.projForm.focus].Blur()
	}
	if forward {
		current = (current + 1) % len(focuses)
	} else {
		current = (current - 1 + len(focuses)) % len(focuses)
	}
	m.projForm.focus = focuses[current]
	if m.projForm.focus < len(m.projForm.inputs) {
		m.projForm.inputs[m.projForm.focus].Focus()
	}
}

func (m *Model) updateEditProject(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		if key.Matches(msg, m.keys.Cancel) {
			m.mode = m.projectDialogReturn
		}
		return m, nil
	}
	project := m.cfg.Projects[m.editProjectName]
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.projectDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.FormDown):
		m.cycleEditProjectFocus(true)
		return m, nil
	case key.Matches(msg, m.keys.ShiftTab), key.Matches(msg, m.keys.FormUp):
		m.cycleEditProjectFocus(false)
		return m, nil
	case key.Matches(msg, m.keys.Left):
		if m.adjustProjFormField(-1) {
			return m, nil
		}
	case key.Matches(msg, m.keys.Right):
		if m.adjustProjFormField(1) {
			return m, nil
		}
	case key.Matches(msg, m.keys.Enter):
		project.Repo = m.projForm.inputs[1].Value()
		project.Emoji = projectEmojiFieldValue(m.projForm.emojiChoices, m.projForm.emojiIdx)
		project.Agent, project.Dangerous, project.PromptAgent = m.projectAgentFields(m.projForm.agentIdx, m.projForm.dangerous)
		if !project.IsPlain() {
			project.BaseBranch = m.projForm.inputs[2].Value()
			project.BranchPrefix = m.projForm.inputs[3].Value()
			project.NoWorktree = m.projForm.noWorktree
		}
		name := m.editProjectName
		m.busy = true
		m.projForm.err = ""
		return m, func() tea.Msg {
			err := m.backend.UpdateProject(name, project)
			return ProjectUpdatedMsg{Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	}
	if m.projForm.focus < len(m.projForm.inputs) {
		var cmd tea.Cmd
		m.projForm.inputs[m.projForm.focus], cmd = m.projForm.inputs[m.projForm.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateTagForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.FormDown), key.Matches(msg, m.keys.ShiftTab), key.Matches(msg, m.keys.FormUp):
		forward := !(key.Matches(msg, m.keys.ShiftTab) || key.Matches(msg, m.keys.FormUp))
		cycleFormFocus(m.tagForm.inputs, &m.tagForm.focus, len(m.tagForm.inputs), forward)
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		if len(m.sessions) == 0 {
			m.mode = m.sessionDialogReturn
			return m, nil
		}
		id := m.sessions[m.cursor].ID
		ticket := m.tagForm.inputs[0].Value()
		pr := m.tagForm.inputs[1].Value()
		m.mode = m.sessionDialogReturn
		return m, func() tea.Msg {
			s, err := m.backend.SetSessionTags(id, ticket, pr)
			if err != nil {
				return ErrorMsg{Err: err}
			}
			return SessionTaggedMsg{Session: s}
		}
	}
	var cmd tea.Cmd
	m.tagForm.inputs[m.tagForm.focus], cmd = m.tagForm.inputs[m.tagForm.focus].Update(msg)
	return m, cmd
}

// indexOfProject returns name's index in m.projects, or -1 if it's not
// there (e.g. removed, or never added).
func indexOfProject(projects []string, name string) int {
	for i, n := range projects {
		if n == name {
			return i
		}
	}
	return -1
}

func (m *Model) activateProject(name string) {
	m.refreshProjects()
	if i := indexOfProject(m.projects, name); i >= 0 {
		m.activeProj = i
	}
	m.cursor = 0
	m.refreshSessions()
}

// finishProjectAdded is the shared tail of every successful ProjectAddedMsg
// branch (add/init/plain). Adding a project from the main list still funnels
// into creating its first session, but adding one from the picker returns to
// the picker — with the cursor following the project just added — rather
// than yanking the user into an unrelated dialog they didn't ask for.
func (m *Model) finishProjectAdded(name string) {
	if m.projectDialogReturn == ModeProjectPicker {
		if i := indexOfProject(m.projects, name); i >= 0 {
			m.pickerCursor = i
		}
		m.mode = ModeProjectPicker
		m.resetOverlayViewport()
		return
	}
	m.openNewSessionForm()
}

func (m *Model) updateProjectInitChoice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Cancel) {
		m.mode = ModeNewProject
		return m, nil
	}
	switch msg.String() {
	case "i":
		name := m.pending.name
		p := m.pending.p
		return m, func() tea.Msg {
			err := m.backend.InitProjectAndAdd(name, p)
			return ProjectAddedMsg{Kind: "init", Name: name, Project: p, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	case "s":
		name := m.pending.name
		p := m.pending.p
		return m, func() tea.Msg {
			err := m.backend.AddPlainProject(name, p)
			return ProjectAddedMsg{Kind: "plain", Name: name, Project: p, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	case "esc", "b":
		m.mode = ModeNewProject
		return m, nil
	}
	return m, nil
}

func (m *Model) updateConfirmDeleteProject(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if len(m.projects) == 0 {
			m.mode = m.projectDialogReturn
			return m, nil
		}
		name := m.projects[m.activeProj]
		m.mode = m.projectDialogReturn
		return m, func() tea.Msg {
			err := m.backend.RemoveProject(name)
			return ProjectRemovedMsg{Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	case "n", "esc":
		m.mode = m.projectDialogReturn
	}
	return m, nil
}

// updateSearch handles keys while ModeSearch is open: typing narrows
// m.searchResults live (via refreshSearchResults), FormUp/FormDown (arrow-
// only — j/k must stay literal text here, unlike updateProjectPicker's
// cursor list) move the selection, enter jumps to the selected session and
// closes the dialog, esc backs out to wherever search was opened from.
func (m *Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.FormUp):
		if len(m.searchResults) > 0 {
			m.searchCursor = (m.searchCursor - 1 + len(m.searchResults)) % len(m.searchResults)
		}
		return m, nil
	case key.Matches(msg, m.keys.FormDown):
		if len(m.searchResults) > 0 {
			m.searchCursor = (m.searchCursor + 1) % len(m.searchResults)
		}
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		if len(m.searchResults) == 0 || m.searchCursor >= len(m.searchResults) {
			return m, nil
		}
		s := m.searchResults[m.searchCursor]
		if idx := indexOfProject(m.projects, s.Project); idx >= 0 {
			m.activeProj = idx
		}
		// The target session only appears once m.showArchived matches its
		// own Archived state — refreshSessions filters on that equality.
		m.showArchived = s.Archived
		m.refreshSessions()
		m.focusSession(s.ID)
		// Return to wherever search was opened from. If that's MultiView,
		// fold the newly-focused session into that project's own panel
		// state (m.multiCursors) and move panel focus (m.multiFocus) to it
		// — the same bookkeeping delegateToList does around updateList —
		// so the jump lands on the right panel/row instead of leaving the
		// previously-focused panel still highlighted.
		m.mode = m.sessionDialogReturn
		if m.mode == ModeMultiView {
			m.leaveSingleProjectContext(s.Project)
			if pi := indexOfProject(m.multiViewEligibleProjects(), s.Project); pi >= 0 {
				m.multiFocus = pi
				m.ensureMultiFocusVisible()
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.refreshSearchResults()
	return m, cmd
}

// updateProjectPicker handles keys while ModeProjectPicker is open: ↑↓/jk
// move the cursor, enter/o jumps to the highlighted project and closes the
// dialog, esc backs out without changing the active project.
func (m *Model) updateProjectPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if len(m.projects) > 0 {
			m.pickerCursor = (m.pickerCursor - 1 + len(m.projects)) % len(m.projects)
		}
	case key.Matches(msg, m.keys.Down):
		if len(m.projects) > 0 {
			m.pickerCursor = (m.pickerCursor + 1) % len(m.projects)
		}
	// Reorder with the same up/down chords the session list uses (shift+↑↓ /
	// KJ) rather than MoveProjLeft/Right — this list is vertical, like the
	// session list, not the header's horizontal tabs.
	case key.Matches(msg, m.keys.MoveUp):
		if len(m.projects) > 0 && m.pickerCursor > 0 {
			return m, m.moveProjectCmd(m.projects[m.pickerCursor], -1)
		}
	case key.Matches(msg, m.keys.MoveDown):
		if len(m.projects) > 0 && m.pickerCursor < len(m.projects)-1 {
			return m, m.moveProjectCmd(m.projects[m.pickerCursor], 1)
		}
	// d rather than the main list's D (capitalized there to stay distinct
	// from the session-level d shown on the same screen) — the picker only
	// ever lists projects, so there's no session-level d to collide with.
	// e has no main-list equivalent at all anymore; edit only happens here.
	case key.Matches(msg, m.keys.Delete):
		if len(m.projects) > 0 {
			proj := m.projects[m.pickerCursor]
			if n := m.projectSessionCountFor(proj); n > 0 {
				return m.flashError(fmt.Errorf("%s has %d session(s) (incl. archived) — delete them first", proj, n))
			}
			// ModeConfirmDeleteProject acts on m.activeProj, so land on the
			// highlighted project first — the same "jump to it" step Open
			// does, just followed immediately by the confirm dialog instead
			// of returning to the list. Route through activateProject (not a
			// bare m.activeProj assignment) so m.sessions/m.cursor stay in
			// sync too — otherwise canceling back out of the confirm dialog
			// leaves the header pointing at this project while the session
			// list still shows the previous one's sessions.
			m.activateProject(proj)
			m.projectDialogReturn = ModeProjectPicker
			m.mode = ModeConfirmDeleteProject
			m.resetOverlayViewport()
		}
		return m, nil
	case key.Matches(msg, m.keys.EditSession):
		// m.busy stays true (not reset) if a previous edit's save is still
		// in flight when its Cancel is pressed — see updateEditProject's own
		// busy guard — so without this check a second edit opened here could
		// have its state clobbered when that first ProjectUpdatedMsg lands.
		if len(m.projects) > 0 && !m.busy {
			name := m.projects[m.pickerCursor]
			m.editProjectName = name
			m.projForm = m.editProjectForm(name, m.cfg.Projects[name])
			m.activateProject(name) // keeps m.sessions/m.cursor in sync — see the delete case above
			m.projectDialogReturn = ModeProjectPicker
			m.mode = ModeEditProject
			m.resetOverlayViewport()
			m.resizeFormInputs()
		}
		return m, nil
	case key.Matches(msg, m.keys.New):
		// No len(m.projects)==0 guard: this is now the only way to add a
		// project at all (past the zero-projects startup flow), so it must
		// work from the picker's own empty state too — see its "press n to
		// add one" hint.
		m.projectDialogReturn = ModeProjectPicker
		m.mode = ModeNewProject
		m.projForm = newProjectForm()
		m.resetOverlayViewport()
		m.resizeFormInputs()
		return m, nil
	case key.Matches(msg, m.keys.Open):
		if len(m.projects) > 0 {
			m.activeProj = m.pickerCursor
			m.cursor = 0
			m.refreshSessions()
			// Multi-view only shows projects with something to display (see
			// multiViewEligibleProjects) — pin this one so picking it
			// actually lands on it instead of returning to whatever was
			// focused before, or nothing at all if it has no sessions yet.
			proj := m.projects[m.activeProj]
			m.multiPinned = proj
			if idx := indexOfProject(m.multiViewEligibleProjects(), proj); idx >= 0 {
				m.multiFocus = idx
				m.ensureMultiFocusVisible()
			}
		}
		m.mode = m.sessionDialogReturn
	}
	return m, nil
}

// openThemePicker opens ModeThemePicker, seeding the cursor and live-preview
// appearance from the persisted config so Esc has something to revert to.
// Only reachable from the settings screen's Theme row, so it always returns
// to ModeSettings rather than ModeList — it deliberately leaves
// sessionDialogReturn untouched, since ModeSettings itself still needs that
// value for its own Esc handling.
func (m *Model) openThemePicker() {
	m.themeCursor = themeIndex(m.cfg.Theme)
	m.previewAppearance = m.cfg.Appearance
	m.mode = ModeThemePicker
	m.resetOverlayViewport()
}

// openSettings opens ModeSettings at its first row. Like the other dialogs,
// it records where Esc should return to via sessionDialogReturn — delegateToList
// bumps this to ModeMultiView when opened from there (see its switch).
func (m *Model) openSettings() {
	m.settingsCursor = 0
	m.mode = ModeSettings
	m.sessionDialogReturn = ModeList
	m.resetOverlayViewport()
}

// updateSettings handles keys while ModeSettings is open. Up/down move the
// cursor; enter (or left/right, for the boolean rows) applies the row's
// change immediately — same as the O key it replaces for sort mode. The
// theme row is the exception: enter drills into ModeThemePicker instead of
// toggling anything itself, since a theme is a choice among many, not a
// boolean.
func (m *Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Up):
		m.settingsCursor = (m.settingsCursor - 1 + len(settingsRows)) % len(settingsRows)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.settingsCursor = (m.settingsCursor + 1) % len(settingsRows)
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		return m.applySettingsRow(m.settingsCursor)
	case key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Right):
		if settingsRows[m.settingsCursor].kind == settingsRowToggle {
			return m.applySettingsRow(m.settingsCursor)
		}
		return m, nil
	}
	return m, nil
}

// applySettingsRow applies row i's action: toggling a boolean setting and
// persisting it through the backend (errors aren't surfaced — same as the O
// key this replaces, config saves have no user-visible failure mode worth a
// dialog), or — for the theme row — drilling into ModeThemePicker.
//
// row.set(m.cfg, ...) writes m.cfg directly rather than going through a
// Msg's Cfg field like every other config mutation (see cfgSnapshotOnSuccess)
// — safe only because row.persist below runs synchronously, still on the
// Update() goroutine, unlike every other backend mutator which runs inside a
// tea.Cmd closure on its own goroutine. If SetSortRecentFirst/SetAutoTmux/
// SetCompactDetail/SetAutoSubmitDefault's blocking socket round trip
// (ipc.Client.mut) is ever moved into a Cmd to stop it freezing the UI over a
// slow connection, this direct write becomes racy too and needs the same
// Msg.Cfg treatment as the rest.
func (m *Model) applySettingsRow(i int) (tea.Model, tea.Cmd) {
	row := settingsRows[i]
	if row.kind == settingsRowDrill {
		m.openThemePicker()
		return m, nil
	}
	next := !row.get(m.cfg)
	row.set(m.cfg, next)
	m.setFlash("info", row.flash(next))
	_ = row.persist(m.backend, next)
	if row.refreshSessions {
		m.refreshSessions()
	}
	return m, nil
}

// updateThemePicker handles keys while ModeThemePicker is open. Up/down and
// 'a' apply their choice immediately (live preview); enter persists it, esc
// reverts to the config's last-saved theme/appearance.
func (m *Model) updateThemePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		applyTheme(m.cfg.Theme)
		applyAppearance(m.cfg.Appearance)
		m.mode = ModeSettings
		return m, nil
	case key.Matches(msg, m.keys.Up):
		m.themeCursor = (m.themeCursor - 1 + len(themeNames)) % len(themeNames)
		applyTheme(themeNames[m.themeCursor])
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.themeCursor = (m.themeCursor + 1) % len(themeNames)
		applyTheme(themeNames[m.themeCursor])
		return m, nil
	case msg.String() == "a":
		m.previewAppearance = nextAppearance(m.previewAppearance)
		applyAppearance(m.previewAppearance)
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		theme := themeNames[m.themeCursor]
		appearance := m.previewAppearance
		m.mode = ModeSettings
		return m, func() tea.Msg {
			if err := m.backend.SetTheme(theme, appearance); err != nil {
				return ErrorMsg{Err: err}
			}
			snap := m.backend.ConfigSnapshot()
			return ThemeSetMsg{Theme: theme, Appearance: appearance, Cfg: &snap}
		}
	}
	return m, nil
}

func (m *Model) flashError(err error) (tea.Model, tea.Cmd) {
	m.setError(err)
	return m, nil
}
