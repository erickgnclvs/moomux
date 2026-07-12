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
	if s.Archived {
		b.WriteString(row("archived", "yes"))
	}
	b.WriteString(row("agent", s.AgentName()))
	b.WriteString(row("name", truncate(s.Name, valueWidth)))
	b.WriteString(row("worktree", truncate(s.WorktreePath, valueWidth)))
	b.WriteString(row("tmux", s.TmuxSession))
	b.WriteString(row("created", humanizeAge(time.Since(s.CreatedAt))))
	if stat, ok := m.diffStats[s.ID]; ok {
		b.WriteString(row("diff", formatDiffStat(stat)))
	}
	if s.Ticket != "" {
		b.WriteString(row("ticket", truncate(s.Ticket, valueWidth)))
	}
	if s.PR != "" {
		b.WriteString(row("pr", truncate(s.PR, valueWidth)))
	}
	if prompt := m.prompts[s.ID]; prompt != "" {
		oneline := strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", " "), "\n", " ")
		b.WriteString(row("prompt", truncate(oneline, valueWidth)))
	}
	b.WriteString("\n")
	var cowMsg string
	switch st {
	case watcher.Working:
		cowMsg = pickQuip(s.ID, quipsWorking)
	case watcher.Waiting:
		cowMsg = pickQuip(s.ID, quipsWaiting)
	default:
		cowMsg = pickQuip(s.ID, quipsParked)
	}
	b.WriteString(cowStyle.Render(cowsay(cowMsg, valueWidth+10, st)))
	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

func cowsay(msg string, maxWidth int, st watcher.State) string {
	const lineMax = 38
	w := lineMax
	if maxWidth > 0 && maxWidth < w {
		w = maxWidth
	}
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
	var eyes string
	switch st {
	case watcher.Working:
		eyes = "**"
	case watcher.Waiting:
		eyes = "oo"
	default:
		eyes = "--"
	}
	b.WriteString(`        \   ^__^` + "\n")
	b.WriteString("         \\  (" + eyes + `)\_______` + "\n")
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
