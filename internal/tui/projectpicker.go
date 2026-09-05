package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// projectPickerRowMarker prefixes the currently highlighted row so
// focusedOverlayLine can find it after compactOverlayContent has collapsed
// blank lines.
const projectPickerRowMarker = "▸ "

// renderProjectPicker renders the cursor-navigable project list opened by
// ProjectPicker (jump straight to a project without cycling tabs). Each row
// is the project name on the left and a small right-aligned detail column —
// active session count plus archived count (dimmed), e.g. "1/0" — rather
// than a superscript glued onto the name, so the name stays plain and
// readable and archived sessions aren't silently left out of the count.
func (m *Model) renderProjectPicker() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("PROJECTS"))
	b.WriteString("\n\n")
	if len(m.projects) == 0 {
		b.WriteString(muteStyle.Render("no projects yet — press n to add one"))
		return b.String()
	}

	active := map[string]int{}
	archived := map[string]int{}
	for _, s := range m.allSessions() {
		if s.Archived {
			archived[s.Project]++
		} else {
			active[s.Project]++
		}
	}

	// listRow/listRowSelected each add one column of padding on both sides.
	rowWidth := m.overlayWidth(formHintWidth) - 2

	// A legend for the detail column below — "2/1" alone doesn't say which
	// number is which. Sized by hand to exactly rowWidth (like every row's
	// content) and then wrapped in the same bare Padding(0,1) the rows get
	// from listRow/listRowSelected — combining Width and Padding in a single
	// style instead (as an earlier version of this did) makes Width count
	// the padding as part of its budget rather than adding to it, so the
	// result comes out 2 columns narrower than an actual row and the
	// legend's right edge lands short of the counts below it.
	legendText := fmt.Sprintf("%*s", rowWidth, "active/archived")
	legend := lipgloss.NewStyle().Padding(0, 1).Render(muteStyle.Render(legendText))
	b.WriteString(legend)
	b.WriteString("\n")

	for i, p := range m.projects {
		selected := i == m.pickerCursor
		prefix := "  "
		if selected {
			prefix = projectPickerRowMarker
		}

		a, r := active[p], archived[p]
		detail := ""
		if a > 0 || r > 0 {
			// listRow/listRowSelected wrap the finished row in their own style
			// afterward, which doesn't reach back into text already rendered
			// with its own style — so the archived number's highlight
			// background has to be set explicitly to match, the same way
			// renderRow does for its icons/dot.
			archivedStyle := muteStyle
			if selected {
				archivedStyle = archivedStyle.Background(colSelBg)
			}
			detail = fmt.Sprintf("%d", a) + archivedStyle.Render(fmt.Sprintf("/%d", r))
		}

		avail := rowWidth - lipgloss.Width(prefix)
		nameWidth := avail - lipgloss.Width(detail)
		if detail != "" {
			nameWidth-- // gap before the detail column
		}
		if nameWidth < 4 {
			nameWidth = 4
		}
		row := prefix + fmt.Sprintf("%-*s", nameWidth, truncate(p, nameWidth))
		if detail != "" {
			row += " " + detail
		}
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
