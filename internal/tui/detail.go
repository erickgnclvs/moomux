package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/watcher"
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
	b.WriteString("\n")
	cowMsg := m.prompts[s.ID]
	if cowMsg == "" {
		cowMsg = label
	}
	b.WriteString(cowStyle.Render(cowsay(cowMsg, valueWidth+10)))
	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

func cowsay(msg string, maxWidth int) string {
	const lineMax = 38
	if maxWidth > 0 && maxWidth < lineMax {
		maxWidth = lineMax
	}
	w := lineMax
	lines := wrapLines(msg, w)
	// cap at 4 lines, truncate last with ellipsis
	if len(lines) > 4 {
		lines = lines[:4]
		r := []rune(lines[3])
		if len(r) > w-1 {
			r = r[:w-1]
		}
		lines[3] = string(r) + "…"
	}
	border := strings.Repeat("_", w+2)
	var b strings.Builder
	b.WriteString(" " + border + "\n")
	for i, l := range lines {
		pad := w - len([]rune(l))
		padded := l + strings.Repeat(" ", pad)
		switch {
		case len(lines) == 1:
			b.WriteString("< " + padded + " >\n")
		case i == 0:
			b.WriteString("/ " + padded + " \\\n")
		case i == len(lines)-1:
			b.WriteString("\\ " + padded + " /\n")
		default:
			b.WriteString("| " + padded + " |\n")
		}
	}
	b.WriteString(" " + strings.Repeat("-", w+2) + "\n")
	b.WriteString(`        \   ^__^` + "\n")
	b.WriteString(`         \  (oo)\_______` + "\n")
	b.WriteString(`            (__)\       )\/\` + "\n")
	b.WriteString(`                ||----w |` + "\n")
	b.WriteString(`                ||     ||`)
	return b.String()
}

func wrapLines(s string, width int) []string {
	s = strings.ReplaceAll(s, "\r", "")
	var out []string
	for _, line := range strings.Split(s, "\n") {
		runes := []rune(strings.TrimSpace(line))
		for len(runes) > width {
			cut := width
			for i := width - 1; i > width-15 && i > 0; i-- {
				if runes[i] == ' ' {
					cut = i
					break
				}
			}
			out = append(out, string(runes[:cut]))
			runes = []rune(strings.TrimLeft(string(runes[cut:]), " "))
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
	}
	return out
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
