package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) renderConfirm() string {
	i := m.indexOfSession(m.confirmID)
	if i < 0 {
		return ""
	}
	s := m.sessions[i]
	avail := m.overlayWidth(formHintWidth)
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Delete session?"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("name:     %s\n", s.Name))
	b.WriteString(fmt.Sprintf("branch:   %s\n", s.Branch))
	b.WriteString(fmt.Sprintf("worktree: %s\n", s.WorktreePath))
	b.WriteString("\n")
	warn := m.confirmGit.ok && (m.confirmGit.dirty || m.confirmGit.unpushed)
	switch {
	case warn:
		// truncate (not a hard clip) so a narrow overlay degrades to "⚠
		// uncommitted…" instead of cutting off mid-word.
		b.WriteString(warnStyle.Render("⚠ " + truncate(changeSummaryLabel(m.confirmGit, m.confirmSummary), avail-2)))
		b.WriteString("\n\n")
	case m.confirmChecking:
		b.WriteString(muteStyle.Render("checking git status…"))
		b.WriteString("\n\n")
	}
	b.WriteString(muteStyle.Render("This kills the tmux session and removes the worktree."))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("The branch is kept."))
	b.WriteString("\n\n")
	controls := "y to confirm   n/esc to cancel"
	if warn && !m.confirmAck {
		withWarning := "y to confirm you understand   n/esc to cancel"
		// Same width-tiered fallback as formFooter/helpFooter/etc. (see
		// view.go): drop back to the plain phrasing rather than hard-clip
		// mid-word when the overlay is too narrow for the longer one.
		if lipgloss.Width(withWarning) <= avail {
			controls = withWarning
		}
	}
	b.WriteString(controls)
	return b.String()
}
