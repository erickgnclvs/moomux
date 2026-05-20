package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func (m *Model) renderList(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("SESSIONS"))
	b.WriteString("\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(muteStyle.Render("  no sessions — press n to create"))
		return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
	}
	for i, s := range m.sessions {
		row := renderRow(s, m.effectiveState(s), width-4)
		if i == m.cursor {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(b.String())
}

func renderRow(s session.Session, st watcher.State, width int) string {
	dot := dotParked
	label := "park"
	switch st {
	case watcher.Working:
		dot = dotWorking
		label = "work"
	case watcher.Waiting:
		dot = dotWaiting
		label = "wait"
	}
	nameWidth := width - 10
	if nameWidth < 4 {
		nameWidth = 4
	}
	name := truncate(s.Name, nameWidth)
	return fmt.Sprintf("%-*s %s %s", nameWidth, name, dot+" ", muteStyle.Render(label))
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	if n < 2 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
