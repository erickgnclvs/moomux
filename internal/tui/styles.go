package tui

import "github.com/charmbracelet/lipgloss"

var (
	colFg      = lipgloss.Color("#e6e6e6")
	colMute    = lipgloss.Color("#7a7a85")
	colAccent  = lipgloss.Color("#7aa2f7")
	colWorking = lipgloss.Color("#9ece6a")
	colWaiting = lipgloss.Color("#e0af68")
	colParked  = lipgloss.Color("#565a6e")
	colDanger  = lipgloss.Color("#f7768e")
	colBorder  = lipgloss.Color("#2d2f3a")
	colSelBg   = lipgloss.Color("#1f2233")

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

	// hintStyle is the contextual, per-field explainer shown in forms —
	// italic to read as a transient tip rather than a persistent label.
	hintStyle = lipgloss.NewStyle().Foreground(colMute).Italic(true)

	// help overlay
	helpGroupStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	helpKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(colFg)
	helpDescStyle  = lipgloss.NewStyle().Foreground(colMute)
)
