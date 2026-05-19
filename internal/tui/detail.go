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
	st := m.effectiveState(s)
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
	if p := m.prompts[s.ID]; p != "" {
		b.WriteString("\n")
		b.WriteString(muteStyle.Render("task:"))
		b.WriteString("\n")
		b.WriteString(wrap(p, valueWidth+10))
	}
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

// wrap soft-wraps s at width on rune boundaries, preferring spaces.
func wrap(s string, width int) string {
	if width < 8 {
		width = 8
	}
	s = strings.ReplaceAll(s, "\r", "")
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		runes := []rune(line)
		for len(runes) > width {
			cut := width
			for i := width - 1; i > width-20 && i > 0; i-- {
				if runes[i] == ' ' {
					cut = i
					break
				}
			}
			out.WriteString(string(runes[:cut]))
			out.WriteString("\n")
			runes = runes[cut:]
			for len(runes) > 0 && runes[0] == ' ' {
				runes = runes[1:]
			}
		}
		out.WriteString(string(runes))
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
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
