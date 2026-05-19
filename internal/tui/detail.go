package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/curral/internal/watcher"
)

func (m *Model) renderDetail(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("DETAIL"))
	b.WriteString("\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(muteStyle.Render("nothing selected"))
		return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
	}
	s := m.sessions[m.cursor]
	st := m.states[s.WorktreePath]
	dot := dotParked
	label := "parked"
	switch st {
	case watcher.Working:
		dot, label = dotWorking, "working"
	case watcher.Waiting:
		dot, label = dotWaiting, "waiting"
	}
	row := func(k, v string) string {
		return fmt.Sprintf("%s %s\n", muteStyle.Render(fmt.Sprintf("%-10s", k+":")), v)
	}
	valueWidth := width - 14
	if valueWidth < 8 {
		valueWidth = 8
	}
	b.WriteString(row("status", dot+"  "+label))
	b.WriteString(row("name", truncate(s.Name, valueWidth)))
	b.WriteString(row("branch", truncate(s.Branch, valueWidth)))
	b.WriteString(row("worktree", truncate(s.WorktreePath, valueWidth)))
	b.WriteString(row("tmux", s.TmuxSession))
	b.WriteString(row("created", humanizeAge(time.Since(s.CreatedAt))))
	b.WriteString("\n")
	b.WriteString(cowStyle.Render(cowsay(label)))
	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

func cowsay(msg string) string {
	top := " " + strings.Repeat("_", len(msg)+2)
	mid := "< " + msg + " >"
	bot := " " + strings.Repeat("-", len(msg)+2)
	cow := `        \   ^__^
         \  (oo)\_______
            (__)\       )\/\
                ||----w |
                ||     ||`
	return top + "\n" + mid + "\n" + bot + "\n" + cow
}

func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
