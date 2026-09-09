package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
)

// settingsRowMarker prefixes the currently highlighted row so
// focusedOverlayLine can find it, same as themePickerRowMarker.
const settingsRowMarker = "▸ "

type settingsRowKind int

const (
	settingsRowToggle settingsRowKind = iota
	settingsRowDrill
	settingsRowText
)

// settingsRow describes one row of the settings screen. Toggle rows flip a
// boolean in place; the theme row (settingsRowDrill) instead opens the
// existing ModeThemePicker, since a theme is a choice among several, not a
// binary; a settingsRowText row (diff tool) edits its value inline in the
// row itself, which is cheaper than a whole overlay for one free-text
// field.
type settingsRow struct {
	label           string
	kind            settingsRowKind
	get             func(*config.Config) bool  // toggle rows only
	set             func(*config.Config, bool) // toggle rows only
	persist         func(Backend, bool) error  // toggle rows only
	flash           func(next bool) string     // toggle rows only; status line shown after applying
	renderValue     func(m *Model) string      // right-hand column
	refreshSessions bool                       // whether applying should also refresh the session list (sort mode only)
}

var settingsRows = []settingsRow{
	{
		label:   "sort mode",
		kind:    settingsRowToggle,
		get:     func(c *config.Config) bool { return c.SortRecentFirst },
		set:     func(c *config.Config, v bool) { c.SortRecentFirst = v },
		persist: func(b Backend, v bool) error { return b.SetSortRecentFirst(v) },
		flash: func(next bool) string {
			if next {
				return "session sort: most-recently-opened first"
			}
			return "session sort: manual (shift+↑↓)"
		},
		renderValue: func(m *Model) string {
			if m.cfg.SortRecentFirst {
				return "most-recently-opened"
			}
			return "manual (shift+↑↓)"
		},
		refreshSessions: true,
	},
	{
		label: "theme & appearance",
		kind:  settingsRowDrill,
		renderValue: func(m *Model) string {
			return themeNames[themeIndex(m.cfg.Theme)] + " / " + appearanceLabel(m.cfg.Appearance)
		},
	},
	{
		label:   "auto-tmux",
		kind:    settingsRowToggle,
		get:     func(c *config.Config) bool { return c.AutoTmux },
		set:     func(c *config.Config, v bool) { c.AutoTmux = v },
		persist: func(b Backend, v bool) error { return b.SetAutoTmux(v) },
		flash: func(next bool) string {
			if next {
				return "auto-tmux: on — moomux will relaunch itself inside tmux on startup"
			}
			return "auto-tmux: off"
		},
		renderValue: func(m *Model) string { return renderToggle(m.cfg.AutoTmux, false) },
	},
	{
		label:   "auto-submit default",
		kind:    settingsRowToggle,
		get:     func(c *config.Config) bool { return c.AutoSubmitDefault },
		set:     func(c *config.Config, v bool) { c.AutoSubmitDefault = v },
		persist: func(b Backend, v bool) error { return b.SetAutoSubmitDefault(v) },
		flash: func(next bool) string {
			if next {
				return "auto-submit default: on"
			}
			return "auto-submit default: off"
		},
		renderValue: func(m *Model) string { return renderToggle(m.cfg.AutoSubmitDefault, false) },
	},
	{
		label:   "compact detail view",
		kind:    settingsRowToggle,
		get:     func(c *config.Config) bool { return c.CompactDetail },
		set:     func(c *config.Config, v bool) { c.CompactDetail = v },
		persist: func(b Backend, v bool) error { return b.SetCompactDetail(v) },
		flash: func(next bool) string {
			if next {
				return "compact detail view: on"
			}
			return "compact detail view: off"
		},
		renderValue: func(m *Model) string { return renderToggle(m.cfg.CompactDetail, false) },
	},
	{
		label: "diff tool",
		kind:  settingsRowText,
		renderValue: func(m *Model) string {
			if m.settingsEditing {
				return m.settingsInput.View()
			}
			if m.client.DiffTool == "" {
				return "not set"
			}
			return m.client.DiffTool
		},
	},
}

// settingsInputWidth sizes the inline editor so the row still fits its
// label on a narrow terminal — the overlay clips rather than wraps.
func settingsInputWidth(overlay int) int {
	w := overlay - 20
	if w < 10 {
		w = 10
	}
	if w > 48 {
		w = 48
	}
	return w
}

// renderSettings renders the cursor-navigable settings list opened by 's'.
func (m *Model) renderSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("SETTINGS"))
	b.WriteString("\n\n")

	// listRow/listRowSelected each add one column of padding on both sides.
	rowWidth := m.overlayWidth(formHintWidth) - 2

	for i, row := range settingsRows {
		selected := i == m.settingsCursor
		prefix := "  "
		if selected {
			prefix = settingsRowMarker
		}

		value := row.renderValue(m)
		avail := rowWidth - lipgloss.Width(prefix)
		labelWidth := avail - lipgloss.Width(value)
		if labelWidth > 0 {
			labelWidth--
		}
		if labelWidth < 4 {
			labelWidth = 4
		}
		line := prefix + fmt.Sprintf("%-*s", labelWidth, truncate(row.label, labelWidth)) + " " + value
		switch {
		// The inline editor brings its own styling (prompt, cursor,
		// placeholder), and wrapping that in the selected-row background
		// leaves the bar ending mid-row where the input's first reset code
		// lands. Its cursor marks the focused row well enough on its own.
		case selected && m.settingsEditing && row.kind == settingsRowText:
			line = listRow.Render(line)
		case selected:
			line = listRowSelected.Render(line)
		default:
			line = listRow.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// settingsFooter mirrors themePickerFooter's width-tiered approach.
func (m *Model) settingsFooter() string {
	full := "↑↓ select  enter/←→ change  esc close"
	short := "esc close  enter change"
	if m.settingsEditing {
		full = "enter save  esc cancel"
		short = full
	}
	controls := full
	if lipgloss.Width(controls) > m.overlayWidth(formHintWidth) {
		controls = short
	}
	return muteStyle.Render(controls)
}
