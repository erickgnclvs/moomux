package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) View() string {
	if m.width == 0 {
		return "starting…"
	}

	header := m.renderHeader()

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

	bodyHeight := m.height - 6
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	left := panelBorder.Width(listW).Height(bodyHeight).Render(m.renderList(listW-2, bodyHeight-2))
	right := panelBorder.Width(detailW).Height(bodyHeight).Render(m.renderDetail(detailW-2, bodyHeight-2))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	footer := m.renderFooter()
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

func (m *Model) renderFooter() string {
	left := "n:new  enter:open  x:kill  d:delete  t:tag  tab:switch  r:refresh  q:quit"
	if m.flash != "" {
		left = m.flash + "  •  " + left
	}
	right := "P:+project  D:-project"
	// subtract 2 for the footer's horizontal padding (Padding(0,1) = 1 each side)
	inner := m.width - 2
	leftW := inner - lipgloss.Width(right)
	if leftW < 0 {
		leftW = 0
	}
	row := lipgloss.JoinHorizontal(lipgloss.Bottom,
		lipgloss.NewStyle().Width(leftW).Render(left),
		lipgloss.NewStyle().Width(lipgloss.Width(right)).Align(lipgloss.Right).Render(right),
	)
	return footerStyle.Width(m.width).Render(row)
}
