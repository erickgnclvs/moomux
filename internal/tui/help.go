package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpEntry is a single "key → description" line in the help overlay.
type helpEntry struct {
	key  string
	desc string
}

// helpGroup is a titled block of related commands.
type helpGroup struct {
	title   string
	entries []helpEntry
}

// helpGroups is the full command reference shown in the help overlay. The
// footer only advertises "?:help"; this is the exhaustive list so users don't
// have to memorize anything. The `a`/`A` copy is filled in per-render since it
// depends on whether the archived view is active.
func (m *Model) helpGroups() []helpGroup {
	archiveDesc := "archive session"
	archivedDesc := "show archived"
	if m.showArchived {
		archiveDesc = "restore session"
		archivedDesc = "show active"
	}
	return []helpGroup{
		{
			title: "Sessions",
			entries: []helpEntry{
				{"n", "new session"},
				{"enter / o", "open (attach tmux)"},
				{"x", "park (kill tmux)"},
				{"d", "delete worktree"},
				{"a", archiveDesc},
				{"A", archivedDesc},
				{"t", "tag ticket / PR"},
			},
		},
		{
			title: "Navigation",
			entries: []helpEntry{
				{"↑ / k", "move up"},
				{"↓ / j", "move down"},
				{"shift+↑ / ↓", "reorder session"},
				{"tab", "next project"},
				{"shift+tab", "prev project"},
				{"shift+← / →", "reorder project"},
				{"r", "refresh"},
			},
		},
		{
			title: "Projects",
			entries: []helpEntry{
				{"P", "add project"},
				{"D", "remove project"},
			},
		},
		{
			title: "General",
			entries: []helpEntry{
				{"?", "toggle this help"},
				{"q", "quit"},
			},
		},
	}
}

// renderHelp renders the command reference shown behind the ModeHelp overlay.
// Groups are laid out two per row so the panel stays compact on typical
// terminals rather than scrolling off the bottom.
func (m *Model) renderHelp() string {
	groups := m.helpGroups()

	// Render each group into its own fixed-width column so the two-up rows
	// line up regardless of how long any single description is.
	const colWidth = 34
	cols := make([]string, len(groups))
	for i, g := range groups {
		var b strings.Builder
		b.WriteString(helpGroupStyle.Render(g.title))
		b.WriteString("\n")
		keyW := 0
		for _, e := range g.entries {
			if w := lipgloss.Width(e.key); w > keyW {
				keyW = w
			}
		}
		for _, e := range g.entries {
			key := helpKeyStyle.Render(e.key)
			pad := strings.Repeat(" ", keyW-lipgloss.Width(e.key))
			b.WriteString("  " + key + pad + "  " + helpDescStyle.Render(e.desc) + "\n")
		}
		cols[i] = lipgloss.NewStyle().Width(colWidth).Render(b.String())
	}

	var rows []string
	for i := 0; i < len(cols); i += 2 {
		if len(rows) > 0 {
			rows = append(rows, "") // blank line between group-pairs, not after the last
		}
		if i+1 < len(cols) {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cols[i], cols[i+1]))
		} else {
			rows = append(rows, cols[i])
		}
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("moomux commands"))
	b.WriteString("\n\n")
	b.WriteString(lipgloss.JoinVertical(lipgloss.Left, rows...))
	b.WriteString(muteStyle.Render("?/esc to close"))
	return b.String()
}
