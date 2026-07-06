package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// truncateToWidth clips s to at most w cells, appending an ellipsis if it
// had to cut. Used to keep footer rows to a single, fixed-height line so the
// overall layout never shifts between frames.
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	r := []rune(s)
	out := make([]rune, 0, len(r))
	width := 0
	for _, c := range r {
		cw := lipgloss.Width(string(c))
		if width+cw > w-1 {
			break
		}
		out = append(out, c)
		width += cw
	}
	return string(out) + "…"
}

func (m *Model) View() string {
	if m.width == 0 {
		return "starting…"
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	listW := 42
	if m.width-listW < 30 {
		listW = m.width / 2
	}
	if listW < 20 {
		listW = 20
	}
	detailW := m.width - listW - 2
	if detailW < 20 {
		detailW = 20
	}

	// -2 accounts for panelBorder's top/bottom border lines, which sit
	// outside the Height() passed to it below.
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 2
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	listContent, hits := m.renderList(listW-2, bodyHeight-2)
	left := panelBorder.Width(listW).Height(bodyHeight).Render(listContent)
	right := panelBorder.Width(detailW).Height(bodyHeight).Render(m.renderDetail(detailW-2, bodyHeight-2))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	m.updateLinkHits(header, hits)

	base := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)

	switch m.mode {
	case ModeNewForm:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox.Render(m.renderNewForm()))
	case ModeConfirmDelete:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox.Render(m.renderConfirm()))
	case ModeNewProject:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox.Render(m.renderNewProject()))
	case ModeConfirmDeleteProject:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox.Render(m.renderConfirmDeleteProject()))
	case ModeProjectInitChoice:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox.Render(m.renderProjectInitChoice()))
	case ModeTagForm:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlayBox.Render(m.renderTagForm()))
	}
	return base
}

func (m *Model) renderHeader() string {
	cow := cowStyle.Render("  ^__^\n  (oo)\\_\n  (__)\\ )")
	wordmark := titleStyle.Render("moomux")
	left := lipgloss.JoinHorizontal(lipgloss.Center, cow, "  ", wordmark)

	tabs := []string{}
	for i, p := range m.projects {
		if i == m.activeProj {
			tabs = append(tabs, tabActive.Render(p))
		} else {
			tabs = append(tabs, tabInactive.Render(p))
		}
	}
	right := strings.Join(tabs, " ")

	remaining := m.width - 2 - lipgloss.Width(left)
	if remaining < lipgloss.Width(right) {
		remaining = lipgloss.Width(right)
	}
	rightCol := lipgloss.NewStyle().Width(remaining).Align(lipgloss.Right).Render(right)
	row := lipgloss.JoinHorizontal(lipgloss.Center, left, rightCol)
	return lipgloss.NewStyle().Padding(0, 1).Render(row)
}

// renderFooter always returns exactly two lines — a message row (blank when
// there's no flash) and a hints row — so the overall layout height never
// changes between frames. Both rows are truncated rather than word-wrapped;
// letting them grow to variable heights previously caused the body/footer
// split to jitter across renders, which could leave stale content on screen
// or push the hints row out of view.
func (m *Model) renderFooter() string {
	hints := "n:new  enter:open  x:park  d:delete  t:tag  tab:project  r:refresh  q:quit"
	right := "P:+project  D:-project"
	// subtract 2 for the footer's horizontal padding (Padding(0,1) = 1 each side)
	inner := m.width - 2
	hintsW := inner - lipgloss.Width(right)
	if hintsW < 0 {
		hintsW = 0
	}
	hintRow := lipgloss.JoinHorizontal(lipgloss.Bottom,
		lipgloss.NewStyle().Width(hintsW).Render(truncateToWidth(hints, hintsW)),
		lipgloss.NewStyle().Width(lipgloss.Width(right)).Align(lipgloss.Right).Render(right),
	)

	messageLine := ""
	if m.flash != "" {
		flashStyle := infoFlashStyle
		prefix := "✓ "
		if m.flashKind == "error" {
			flashStyle = errorFlashStyle
			prefix = "✖ "
		}
		messageLine = flashStyle.Render(truncateToWidth(prefix+m.flash, inner))
	}
	messageRow := lipgloss.NewStyle().Width(inner).Render(messageLine)

	rows := lipgloss.JoinVertical(lipgloss.Left, messageRow, hintRow)
	return footerStyle.Width(m.width).Render(rows)
}
