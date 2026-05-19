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

	bodyHeight := m.height - 4
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
	}
	return base
}

func (m *Model) renderHeader() string {
	left := titleStyle.Render("curral")
	tabs := []string{}
	for i, p := range m.projects {
		if i == m.activeProj {
			tabs = append(tabs, tabActive.Render(p))
		} else {
			tabs = append(tabs, tabInactive.Render(p))
		}
	}
	right := strings.Join(tabs, " ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(left + strings.Repeat(" ", gap) + right)
}

func (m *Model) renderFooter() string {
	help := "n:new  enter:open  d:delete  r:refresh  tab:project  q:quit"
	if m.flash != "" {
		help = m.flash + "  •  " + help
	}
	return footerStyle.Width(m.width).Render(help)
}
