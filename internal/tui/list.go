package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// linkHit records where a clickable ticket/PR icon landed within the
// rendered list, in the list panel's own local coordinates (line index and
// column range on that line). The TUI translates these to absolute terminal
// coordinates once the surrounding layout (header height, panel border) is
// known, so a click can be matched back to the URL to open.
type linkHit struct {
	sessionID  string
	url        string
	line       int
	col0, col1 int // half-open column range
}

func (m *Model) renderList(width, height int) (string, []linkHit) {
	var b strings.Builder
	title := "SESSIONS"
	empty := "  no sessions — press n to create"
	if len(m.projects) == 0 {
		empty = "  no projects yet — press P to add one"
	} else if m.showArchived {
		title = "ARCHIVED"
		empty = "  no archived sessions"
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(muteStyle.Render(empty))
		return lipgloss.NewStyle().Width(width).Height(height).Render(b.String()), nil
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
	var hits []linkHit
	for i := start; i < end; i++ {
		s := m.sessions[i]
		row, rowHits := renderRow(s, m.effectiveState(s), width-4)
		for _, h := range rowHits {
			h.sessionID = s.ID
			// +2 lines for the "SESSIONS" title and blank line above;
			// +1 column for the row style's own left padding.
			h.line = 2 + (i - start)
			h.col0++
			h.col1++
			hits = append(hits, h)
		}
		if i == m.cursor {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String()), hits
}

func renderRow(s session.Session, st watcher.State, width int) (string, []linkHit) {
	dot := dotParked
	switch st {
	case watcher.Working:
		dot = dotWorking
	case watcher.Waiting:
		dot = dotWaiting
	}
	var icons string
	var hits []linkHit
	col := 0
	addIcon := func(icon, url string) {
		w := lipgloss.Width(icon)
		hits = append(hits, linkHit{url: url, col0: col, col1: col + w})
		icons += icon + " "
		col += w + 1
	}
	if s.Ticket != "" {
		addIcon(iconTicket, s.Ticket)
	}
	if s.PR != "" {
		addIcon(iconPR, s.PR)
	}
	suffix := icons + dot
	nameWidth := width - 1 - lipgloss.Width(suffix)
	if nameWidth < 4 {
		nameWidth = 4
	}
	name := truncate(s.Name, nameWidth)
	offset := nameWidth + 1
	for i := range hits {
		hits[i].col0 += offset
		hits[i].col1 += offset
	}
	return fmt.Sprintf("%-*s %s", nameWidth, name, suffix), hits
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
