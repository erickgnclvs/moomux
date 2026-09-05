package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
)

// searchRowMarker prefixes the currently highlighted row so
// focusedOverlayLine can find it after compactOverlayContent has collapsed
// blank lines — mirrors projectPickerRowMarker.
const searchRowMarker = "▸ "

// matchSessions returns every session across every project — active or
// archived — whose Name contains query, case-insensitively. An empty query
// matches everything, letting the search overlay double as a browse-all-
// sessions list before the user types anything. Results are sorted by
// archived status (active first), then project, then name for a stable,
// predictable order (sessions carry no "last used" timestamp to rank by
// instead).
func matchSessions(all []session.Session, query string) []session.Session {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]session.Session, 0, len(all))
	for _, s := range all {
		if query == "" || strings.Contains(strings.ToLower(s.Name), query) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Archived != out[j].Archived {
			return !out[i].Archived
		}
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// refreshSearchResults recomputes m.searchResults from the current query,
// clamping m.searchCursor back into range if the new result set shrank.
func (m *Model) refreshSearchResults() {
	m.searchResults = matchSessions(m.allSessions(), m.searchInput.Value())
	if m.searchCursor >= len(m.searchResults) {
		m.searchCursor = 0
	}
}

// renderSearch renders the flattened, filtered session list opened by
// Search — modeled on renderProjectPicker, but rows carry a project tag
// (since results span every project) and an "archived" marker instead of an
// active/archived count column.
func (m *Model) renderSearch() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("SEARCH SESSIONS"))
	b.WriteString("\n\n")
	b.WriteString(m.searchInput.View())
	b.WriteString("\n\n")

	if len(m.searchResults) == 0 {
		if m.searchInput.Value() == "" {
			b.WriteString(muteStyle.Render("no sessions yet"))
		} else {
			b.WriteString(muteStyle.Render("no matches"))
		}
		return b.String()
	}

	rowWidth := m.overlayWidth(formHintWidth) - 2

	for i, s := range m.searchResults {
		selected := i == m.searchCursor
		prefix := "  "
		if selected {
			prefix = searchRowMarker
		}

		tag := s.Project
		if s.Archived {
			tag += " archived"
		}
		tagStyle := muteStyle
		if selected {
			tagStyle = tagStyle.Background(colSelBg)
		}
		detail := tagStyle.Render(tag)

		avail := rowWidth - lipgloss.Width(prefix)
		nameWidth := avail - lipgloss.Width(detail) - 1 // gap before the tag column
		if nameWidth < 4 {
			nameWidth = 4
		}
		row := prefix + fmt.Sprintf("%-*s", nameWidth, truncate(s.Name, nameWidth)) + " " + detail
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

// searchFooter mirrors projectPickerFooter's width-tiered approach.
func (m *Model) searchFooter() string {
	full := "↑↓ select  enter open  esc cancel"
	short := "esc cancel  enter open"
	controls := full
	if lipgloss.Width(controls) > m.overlayWidth(formHintWidth) {
		controls = short
	}
	return muteStyle.Render(controls)
}
