package tui

import (
	"sync"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
)

// ApplySettings applies a loaded config's saved theme/appearance before the
// TUI's first render — main wires this in ahead of tea.NewProgram.
func ApplySettings(cfg *config.Config) {
	applyAppearance(cfg.Appearance)
	applyTheme(cfg.Theme)
}

// themeNames is the display/cycle order for the theme picker, derived from
// the core's table (config.Themes) rather than restated here — that table is
// also what internal/ipc serves a second front end, so there is one list of
// theme names in the process, not one per front end.
var themeNames = func() []string {
	all := config.Themes()
	names := make([]string, len(all))
	for i, t := range all {
		names[i] = t.Name
	}
	return names
}()

// adaptive converts a served config.Color to the lipgloss pair the styles are
// built from. Color.System is ignored on purpose: a terminal has no system
// accent to follow, and Light/Dark always carry a usable fallback.
func adaptive(c config.Color) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: c.Light, Dark: c.Dark}
}

// themeIndex returns name's position in themeNames, or 0 ("default") if name
// is empty or unrecognized (e.g. an unset or hand-edited config field).
func themeIndex(name string) int {
	for i, n := range themeNames {
		if n == name {
			return i
		}
	}
	return 0
}

// applyTheme swaps the active color palette and rebuilds every style and
// pre-rendered string derived from it. Unknown names fall back to "default"
// rather than erroring, since this also runs against whatever a hand-edited
// config.toml contains.
func applyTheme(name string) {
	p := config.ThemeByName(name)
	colFg = adaptive(p.Fg)
	colMute = adaptive(p.Mute)
	colAccent = adaptive(p.Accent)
	colWorking = adaptive(p.Working)
	colDone = adaptive(p.Done)
	colNeedsInput = adaptive(p.NeedsInput)
	colParked = adaptive(p.Parked)
	colWarn = adaptive(p.Warn)
	colDanger = adaptive(p.Danger)
	colBorder = adaptive(p.Border)
	colSelBg = adaptive(p.SelBg)
	buildStyles()
}

// autoDark lazily detects and caches the terminal's actual background once,
// the first time "auto" appearance is applied — not at package init, so
// tests and non-interactive runs that never touch appearance don't pay for
// an OSC-11 terminal query. lipgloss.SetHasDarkBackground latches its value
// permanently once called, so restoring "auto" after an explicit override
// requires remembering the real detected value ourselves.
var (
	autoDarkOnce sync.Once
	autoDarkVal  bool
)

func autoDark() bool {
	autoDarkOnce.Do(func() {
		autoDarkVal = lipgloss.HasDarkBackground()
	})
	return autoDarkVal
}

// applyAppearance overrides (or restores) which half of every AdaptiveColor
// pair renders. "light"/"dark" force it; anything else (including "", the
// zero-value config default) restores auto-detection.
func applyAppearance(mode string) {
	switch mode {
	case "light":
		lipgloss.SetHasDarkBackground(false)
	case "dark":
		lipgloss.SetHasDarkBackground(true)
	default:
		lipgloss.SetHasDarkBackground(autoDark())
	}
}

// nextAppearance cycles auto ("") -> light -> dark -> auto, the order the
// theme picker's 'a' key steps through.
func nextAppearance(cur string) string {
	switch cur {
	case "":
		return "light"
	case "light":
		return "dark"
	default:
		return ""
	}
}

// appearanceLabel renders an appearance value for display, since the
// zero-value "" is displayed as "auto" rather than left blank.
func appearanceLabel(mode string) string {
	if mode == "" {
		return "auto"
	}
	return mode
}
