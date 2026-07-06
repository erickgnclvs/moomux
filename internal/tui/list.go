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
	visible := height - 2 // header + blank line above
	if visible < 1 {
		visible = 1
	}
	start := 0
	if len(m.sessions) > visible {
		start = m.cursor - visible/2
		if start < 0 {
			start = 0
		}
		if max := len(m.sessions) - visible; start > max {
			start = max
		}
	}
	end := start + visible
	if end > len(m.sessions) {
		end = len(m.sessions)
	}
	for i := start; i < end; i++ {
		s := m.sessions[i]
		row := renderRow(s, m.effectiveState(s), width-4)
		if i == m.cursor {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String())
}

func renderRow(s session.Session, st watcher.State, width int) string {
	dot := dotParked
	label := "parked"
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
