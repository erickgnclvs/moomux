package tui

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// BatchSummaryMsg carries the batch delete dialog's fresh checks, one per
// session in IDs. IDs guards against a result for a dialog that has since
// closed or reopened on a different selection.
type BatchSummaryMsg struct {
	IDs       []string
	Summaries map[string]changeSummary
}

// BatchDeletedMsg reports a finished batch delete. Err is the first failure,
// if any; Failed counts them all.
type BatchDeletedMsg struct {
	Deleted, Failed int
	Err             error
	Hint            string
}

// toggleMark flips the cursor session's batch-delete mark and steps down a
// row, so marking a run of sessions is one key per row.
func (m *Model) toggleMark() {
	if len(m.sessions) == 0 {
		return
	}
	id := m.sessions[m.cursor].ID
	if m.sessions[m.cursor].Protected {
		m.setFlash("error", m.sessions[m.cursor].Name+" is protected — can't mark it for deletion")
		return
	}
	if m.marked[id] {
		delete(m.marked, id)
	} else {
		if m.marked == nil {
			m.marked = map[string]bool{}
		}
		m.marked[id] = true
	}
	if m.cursor < len(m.sessions)-1 {
		m.cursor++
	}
}

// markFinished marks every session, in every project, whose PR has merged
// or closed: the worktrees that most likely have nothing left to give.
// The dialog still lists them and still warns on unpushed or uncommitted
// work before anything is deleted.
func (m *Model) markFinished() {
	n := 0
	for _, s := range m.allSessions() {
		pr := m.viewFor(s.ID).PR
		if pr == nil || (pr.State != "MERGED" && pr.State != "CLOSED") || m.marked[s.ID] || s.Protected {
			continue
		}
		if m.marked == nil {
			m.marked = map[string]bool{}
		}
		m.marked[s.ID] = true
		n++
	}
	if n == 0 {
		m.setFlash("info", "no merged or closed PRs to mark")
		return
	}
	m.setFlash("info", fmt.Sprintf("marked %s with a merged/closed PR — d to delete", pluralCount(n, "session", "sessions")))
}

// openBatchDelete opens the delete dialog on every marked session that
// still exists. Like the single dialog, it opens at once and runs a fresh
// local check in the background, ignoring y until that lands.
func (m *Model) openBatchDelete() (tea.Model, tea.Cmd) {
	m.batchIDs = nil
	for _, s := range m.allSessions() {
		// A session protected after it was marked drops out here.
		if m.marked[s.ID] && !s.Protected {
			m.batchIDs = append(m.batchIDs, s.ID)
		}
	}
	if len(m.batchIDs) == 0 {
		// Everything marked was deleted from somewhere else meanwhile.
		m.marked = nil
		return m, nil
	}
	m.batchSummary = nil
	m.confirmAck = false
	m.confirmChecking = true
	m.mode = ModeConfirmDelete
	m.sessionDialogReturn = ModeList
	m.resetOverlayViewport()
	return m, fetchBatchSummaryCmd(m.backend, m.batchIDs)
}

// fetchBatchSummaryCmd runs ChangeSummary for every id, a few at a time.
// ponytail: fixed fan-out of 8 — it's local git only, and 8 keeps a
// 100-session batch from forking 100 gits at once.
func fetchBatchSummaryCmd(backend Backend, ids []string) tea.Cmd {
	return func() tea.Msg {
		sums := make([]changeSummary, len(ids))
		sem := make(chan struct{}, 8)
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				files, commits, ok := backend.ChangeSummary(id)
				sums[i] = changeSummary{filesChanged: files, unpushedCommits: commits, ok: ok}
			}()
		}
		wg.Wait()
		out := make(map[string]changeSummary, len(ids))
		for i, id := range ids {
			out[id] = sums[i]
		}
		return BatchSummaryMsg{IDs: ids, Summaries: out}
	}
}

// deleteBatchCmd deletes ids one after another. ponytail: sequential —
// they share one repo's worktree and ref locks, and each delete is a
// rename plus a few git calls now, so a big batch is seconds, not minutes.
func deleteBatchCmd(backend Backend, ids []string) tea.Cmd {
	return func() tea.Msg {
		var res BatchDeletedMsg
		for _, id := range ids {
			hint, err := backend.DeleteSession(id)
			if err != nil {
				res.Failed++
				if res.Err == nil {
					res.Err = err
				}
				continue
			}
			res.Deleted++
			if hint != "" && res.Hint != "" {
				res.Hint += "; "
			}
			res.Hint += hint
		}
		return res
	}
}

func (m *Model) handleBatchSummary(msg BatchSummaryMsg) {
	if m.mode != ModeConfirmDelete || !slices.Equal(m.batchIDs, msg.IDs) {
		return
	}
	m.batchSummary = msg.Summaries
	m.confirmChecking = false
}

func (m *Model) handleBatchDeleted(msg BatchDeletedMsg) {
	switch {
	case msg.Err != nil:
		m.setError(fmt.Errorf("deleted %d, %d failed: %w", msg.Deleted, msg.Failed, msg.Err))
	case msg.Hint != "":
		m.setFlash("info", "deleted "+pluralCount(msg.Deleted, "session", "sessions")+" — "+msg.Hint)
	default:
		m.setFlash("info", "deleted "+pluralCount(msg.Deleted, "session", "sessions"))
	}
	m.deleting = nil
	m.sessionsChanged()
	m.refreshSessions()
}

// batchRisk counts the sessions in the batch whose fresh check found
// uncommitted changes or unpushed commits.
func (m *Model) batchRisk() (dirty, unpushed int) {
	for _, id := range m.batchIDs {
		sum := m.batchSummary[id]
		if !sum.ok {
			continue
		}
		if sum.filesChanged > 0 {
			dirty++
		}
		if sum.unpushedCommits > 0 {
			unpushed++
		}
	}
	return dirty, unpushed
}

func (m *Model) updateBatchConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		if m.confirmChecking {
			return m, nil
		}
		if dirty, unpushed := m.batchRisk(); dirty+unpushed > 0 && !m.confirmAck {
			m.confirmAck = true
			return m, nil
		}
		ids := m.batchIDs
		m.batchIDs, m.batchSummary, m.marked = nil, nil, nil
		m.mode = m.sessionDialogReturn
		m.setFlash("info", "deleting "+pluralCount(len(ids), "session", "sessions")+"…")
		if m.deleting == nil {
			m.deleting = map[string]bool{}
		}
		for _, id := range ids {
			m.deleting[id] = true
		}
		m.invalidateSessions()
		m.refreshSessions()
		return m, deleteBatchCmd(m.backend, ids)
	case key.Matches(msg, m.keys.No), key.Matches(msg, m.keys.Cancel):
		m.batchIDs, m.batchSummary = nil, nil
		m.mode = m.sessionDialogReturn
	}
	return m, nil
}

// renderBatchConfirm returns the dialog's scrolling body (the sessions) and
// its pinned footer (the warning and the y/n line), so a long batch can't
// scroll what you're agreeing to out of view.
func (m *Model) renderBatchConfirm() (content, footer string) {
	avail := m.overlayWidth(formHintWidth)
	byID := map[string]string{}
	for _, s := range m.allSessions() {
		byID[s.ID] = s.Project + "/" + s.Name
	}
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Delete " + pluralCount(len(m.batchIDs), "session", "sessions") + "?"))
	b.WriteString("\n")
	// Width wraps it rather than letting a narrow overlay clip it mid-word.
	b.WriteString(muteStyle.Width(avail).Render("This kills their tmux sessions and removes their worktrees."))
	b.WriteString("\n\n")
	for i, id := range m.batchIDs {
		if i > 0 {
			b.WriteString("\n")
		}
		name := byID[id]
		sum := m.batchSummary[id]
		git := gitStatusInfo{dirty: sum.filesChanged > 0, unpushed: sum.unpushedCommits > 0, ok: sum.ok}
		if !git.ok || (!git.dirty && !git.unpushed) {
			b.WriteString(truncate(name, avail))
			continue
		}
		// The name keeps at least half the line; the warning gets the rest.
		detail := "  " + changeSummaryLabel(git, sum)
		if room := avail - lipgloss.Width(name); room < lipgloss.Width(detail) {
			detail = truncate(detail, max(room, avail/2))
			name = truncate(name, avail-lipgloss.Width(detail))
		}
		b.WriteString(name + warnStyle.Render(detail))
	}

	var f strings.Builder
	dirty, unpushed := m.batchRisk()
	warn := dirty+unpushed > 0
	switch {
	case m.confirmChecking:
		f.WriteString(muteStyle.Render("checking git status…") + "\n")
	case warn:
		var parts []string
		if dirty > 0 {
			parts = append(parts, fmt.Sprintf("%d with uncommitted changes", dirty))
		}
		if unpushed > 0 {
			parts = append(parts, fmt.Sprintf("%d with unpushed commits", unpushed))
		}
		f.WriteString(warnStyle.Width(avail).Render("⚠ "+strings.Join(parts, ", ")) + "\n")
	}
	controls := "y to confirm   n/esc to cancel"
	if warn && !m.confirmAck {
		if withWarning := "y to confirm you understand   n/esc to cancel"; lipgloss.Width(withWarning) <= avail {
			controls = withWarning
		}
	}
	f.WriteString(controls)
	return b.String(), f.String()
}
