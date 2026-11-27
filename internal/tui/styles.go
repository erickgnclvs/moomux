package tui

import (
	"net/url"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Every var below is reassigned by buildStyles whenever the active theme
// changes (see theme.go's applyTheme) — declarations only, no values here.
var (
	colFg         lipgloss.AdaptiveColor
	colMute       lipgloss.AdaptiveColor
	colAccent     lipgloss.AdaptiveColor
	colWorking    lipgloss.AdaptiveColor
	colDone       lipgloss.AdaptiveColor
	colBackground lipgloss.AdaptiveColor
	colNeedsInput lipgloss.AdaptiveColor
	colParked     lipgloss.AdaptiveColor
	colWarn       lipgloss.AdaptiveColor
	colDanger     lipgloss.AdaptiveColor
	colBorder     lipgloss.AdaptiveColor
	colSelBg      lipgloss.AdaptiveColor

	titleStyle lipgloss.Style
	cowStyle   lipgloss.Style
	muteStyle  lipgloss.Style

	listRow         lipgloss.Style
	listRowSelected lipgloss.Style

	panelBorder lipgloss.Style

	footerStyle lipgloss.Style

	infoFlashStyle  lipgloss.Style
	errorFlashStyle lipgloss.Style

	dotWorkingStyle    lipgloss.Style
	dotDoneStyle       lipgloss.Style
	dotBackgroundStyle lipgloss.Style
	dotNeedsInputStyle lipgloss.Style
	dotParkedStyle     lipgloss.Style
	dotWorking         string
	dotDone            string
	dotBackground      string
	dotNeedsInput      string
	dotParked          string

	iconTicketStyle lipgloss.Style
	iconPRStyle     lipgloss.Style
	// iconTicket/iconPR are pre-rendered (used by click_test.go to locate the
	// icon in a rendered frame); like the dots above, they must be rebuilt in
	// buildStyles, not just their styles, or they'd keep the previous theme's
	// escape codes after a swap.
	iconTicket      string
	iconPR          string
	detailLinkStyle lipgloss.Style

	overlayBox lipgloss.Style

	dangerStyle lipgloss.Style
	// warnStyle is built from colWarn, not colDone — see config.Theme.Warn.
	warnStyle lipgloss.Style

	// hintStyle is the contextual, per-field explainer shown in forms —
	// italic to read as a transient tip rather than a persistent label.
	hintStyle lipgloss.Style

	// help overlay
	helpGroupStyle lipgloss.Style
	helpKeyStyle   lipgloss.Style
	helpDescStyle  lipgloss.Style
)

func init() {
	applyTheme("default")
}

// buildStyles derives every style (and the handful of strings pre-rendered
// from one) from the current colXxx vars. Called by applyTheme whenever the
// active theme changes — dotWorking/dotDone/dotNeedsInput/dotParked in
// particular must be rebuilt here rather than once at package init, since
// they bake in their style's escape codes at Render() time and would
// otherwise keep the previous theme's colors after a swap.
func buildStyles() {
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	cowStyle = lipgloss.NewStyle().Foreground(colMute)
	muteStyle = lipgloss.NewStyle().Foreground(colMute)

	listRow = lipgloss.NewStyle().Padding(0, 1)
	listRowSelected = lipgloss.NewStyle().Padding(0, 1).Background(colSelBg).Foreground(colFg).Bold(true)

	panelBorder = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Padding(0, 1)

	footerStyle = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)

	infoFlashStyle = lipgloss.NewStyle().Foreground(colFg).Bold(true)
	errorFlashStyle = lipgloss.NewStyle().Foreground(colDanger).Bold(true)

	dotWorkingStyle = lipgloss.NewStyle().Foreground(colWorking)
	dotDoneStyle = lipgloss.NewStyle().Foreground(colDone)
	dotBackgroundStyle = lipgloss.NewStyle().Foreground(colBackground)
	dotNeedsInputStyle = lipgloss.NewStyle().Foreground(colNeedsInput)
	dotParkedStyle = lipgloss.NewStyle().Foreground(colParked)
	dotWorking = dotWorkingStyle.Render("⬤")
	dotDone = dotDoneStyle.Render("⬤")
	dotBackground = dotBackgroundStyle.Render("⬤")
	dotNeedsInput = dotNeedsInputStyle.Render("⬤")
	dotParked = dotParkedStyle.Render("⬤")

	iconTicketStyle = lipgloss.NewStyle().Foreground(colMute)
	iconPRStyle = lipgloss.NewStyle().Foreground(colMute)
	iconTicket = iconTicketStyle.Render("🎫")
	iconPR = iconPRStyle.Render("🔀")
	detailLinkStyle = lipgloss.NewStyle().Foreground(colAccent)

	overlayBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent).
		Padding(1, 2)

	dangerStyle = lipgloss.NewStyle().Foreground(colDanger).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(colWarn).Bold(true)

	hintStyle = lipgloss.NewStyle().Foreground(colMute).Italic(true)

	helpGroupStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	helpKeyStyle = lipgloss.NewStyle().Bold(true).Foreground(colFg)
	helpDescStyle = lipgloss.NewStyle().Foreground(colMute)
}

// renderLink styles detail-pane link text (URLs, the tmux target, the
// session's first prompt). The underline is a bare SGR pair rather than
// lipgloss's Underline(true): lipgloss styles underlined text one rune at a
// time (to leave spaces un-underlined), which splits multi-rune grapheme
// clusters such as "☁️" (U+2601 U+FE0F) into separately styled runes. Width
// measurement then sees two clusters of width 1+0 instead of one of width
// 2, the row gets padded one cell too wide, the terminal wraps it, and every
// repaint below that row lands one line lower — stale duplicate rows on
// screen. Prompts are arbitrary pasted text, so this is where it bites.
func renderLink(s string) string {
	return "\x1b[4m" + detailLinkStyle.Render(s) + "\x1b[24m"
}

// renderLinkTo is renderLink for text that stands for a URL, wrapping it in
// an OSC 8 hyperlink to target. The visible text is often truncated (the
// detail pane left-truncates a PR URL to "…b.com/org/repo/pull/1"), and
// terminals that detect URLs in on-screen text — cmd-click in iTerm2,
// Ghostty, WezTerm — would otherwise open what's visible, a non-URL like
// "https://b.com/...". An explicit hyperlink makes them open the full target.
// Anything that isn't a plain http(s) URL is rendered without one: target
// comes from session data, and a stray control byte inside the OSC payload
// would end the sequence early and spill the rest onto the screen.
func renderLinkTo(s, target string) string {
	if !isHyperlinkTarget(target) {
		return renderLink(s)
	}
	return ansi.SetHyperlink(target) + renderLink(s) + ansi.ResetHyperlink()
}

func isHyperlinkTarget(target string) bool {
	if strings.ContainsFunc(target, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	u, err := url.Parse(target)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
