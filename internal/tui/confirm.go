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
	// Wrapped, not clipped: the worktree path is what the user checks before
	// confirming a delete, and its tail is the part that identifies it.
	wrap := func(text string) string { return strings.Join(wrapLines(text, avail), "\n") }
	b.WriteString(wrap(fmt.Sprintf("name:     %s", s.Name)) + "\n")
	b.WriteString(wrap(fmt.Sprintf("branch:   %s", s.Branch)) + "\n")
	b.WriteString(wrap(fmt.Sprintf("worktree: %s", s.WorktreePath)) + "\n")
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
	b.WriteString(muteStyle.Render(wrap("This kills the tmux session and removes the worktree.")))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("The branch is kept."))
	b.WriteString("\n\n")
	controls := "y to confirm   n/esc to cancel"
	if warn {
		// The first y only acknowledges the warning, so the two steps must
		// read differently at every width; otherwise the second y looks
		// like the first one did nothing.
		tiers := []string{"y to confirm you understand   n/esc to cancel", "y I understand   n/esc cancel", "y ack  n/esc"}
		if m.confirmAck {
			tiers = []string{"y again to delete   n/esc to cancel", "y again   n/esc cancel", "y again  n/esc"}
		}
		controls = tiers[len(tiers)-1]
		for _, t := range tiers {
			if lipgloss.Width(t) <= avail {
				controls = t
				break
			}
		}
	}
	b.WriteString(controls)
	return b.String()
}
