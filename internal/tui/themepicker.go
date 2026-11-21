package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// themePickerRowMarker prefixes the currently highlighted row so
// focusedOverlayLine can find it, same as projectPickerRowMarker.
const themePickerRowMarker = "▸ "

// renderThemePicker renders the cursor-navigable theme list opened by T.
// Moving the cursor (or pressing 'a' for appearance) applies the choice
// immediately via updateThemePicker, so what's on screen behind this overlay
// is always a live preview of the highlighted row.
func (m *Model) renderThemePicker() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("THEMES"))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("appearance: " + appearanceLabel(m.previewAppearance) + "  (a to cycle)"))
	b.WriteString("\n\n")

	// listRow/listRowSelected each add one column of padding on both sides.
	rowWidth := m.overlayWidth(formHintWidth) - 2

	for i, name := range themeNames {
		selected := i == m.themeCursor
		prefix := "  "
		if selected {
			prefix = themePickerRowMarker
		}
		nameWidth := rowWidth - lipgloss.Width(prefix)
		if nameWidth < 4 {
			nameWidth = 4
		}
		row := prefix + padTrunc(name, nameWidth)
		if selected {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return b.String()
}
