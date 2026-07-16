package tui

import "github.com/charmbracelet/lipgloss"

var (
	colFg      = lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#e6e6e6"}
	colMute    = lipgloss.AdaptiveColor{Light: "#5b5b66", Dark: "#7a7a85"}
	colAccent  = lipgloss.AdaptiveColor{Light: "#2952cc", Dark: "#7aa2f7"}
	colWorking = lipgloss.AdaptiveColor{Light: "#4b7a1f", Dark: "#9ece6a"}
	colWaiting = lipgloss.AdaptiveColor{Light: "#946f1a", Dark: "#e0af68"}
	colParked  = lipgloss.AdaptiveColor{Light: "#a3a3ab", Dark: "#565a6e"}
	colDanger  = lipgloss.AdaptiveColor{Light: "#c0293f", Dark: "#f7768e"}
	colBorder  = lipgloss.AdaptiveColor{Light: "#9a9aa5", Dark: "#2d2f3a"}
	colSelBg   = lipgloss.AdaptiveColor{Light: "#dce3f5", Dark: "#1f2233"}

	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	cowStyle    = lipgloss.NewStyle().Foreground(colMute)
	muteStyle   = lipgloss.NewStyle().Foreground(colMute)
	tabActive   = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Padding(0, 1)
	tabInactive = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)

	listRow         = lipgloss.NewStyle().Padding(0, 1)
	listRowSelected = lipgloss.NewStyle().Padding(0, 1).Background(colSelBg).Foreground(colFg).Bold(true)

	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)

	infoFlashStyle  = lipgloss.NewStyle().Foreground(colFg).Bold(true)
	errorFlashStyle = lipgloss.NewStyle().Foreground(colDanger).Bold(true)

	dotWorking = lipgloss.NewStyle().Foreground(colWorking).Render("⬤")
	dotWaiting = lipgloss.NewStyle().Foreground(colWaiting).Render("⬤")
	dotParked  = lipgloss.NewStyle().Foreground(colParked).Render("⬤")

	iconTicket = lipgloss.NewStyle().Foreground(colMute).Render("🎫")
	iconPR     = lipgloss.NewStyle().Foreground(colMute).Render("🔀")

	overlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent).
			Padding(1, 2)

	dangerStyle = lipgloss.NewStyle().Foreground(colDanger).Bold(true)

	// help overlay
	helpGroupStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	helpKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(colFg)
	helpDescStyle  = lipgloss.NewStyle().Foreground(colMute)
)
