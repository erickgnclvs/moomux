package tui

import (
	"fmt"
	"strings"
)

func (m *Model) renderConfirm() string {
	if len(m.sessions) == 0 {
		return ""
	}
	s := m.sessions[m.cursor]
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Delete session?"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("name:     %s\n", s.Name))
	b.WriteString(fmt.Sprintf("branch:   %s\n", s.Branch))
	b.WriteString(fmt.Sprintf("worktree: %s\n", s.WorktreePath))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("This kills the tmux session and removes the worktree."))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("The branch is kept."))
	b.WriteString("\n\n")
	b.WriteString("y to confirm   n/esc to cancel")
	return b.String()
}
