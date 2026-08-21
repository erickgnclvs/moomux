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
// depends on whether the archived view is active, and `R`'s since it depends
// on whether copy-instead-of-open is currently forced on.
func (m *Model) helpGroups() []helpGroup {
	archiveDesc := "archive session"
	archivedDesc := "show archived"
	if m.showArchived {
		archiveDesc = "restore session"
		archivedDesc = "show active"
	}
	linkDesc := "force-copy ticket/PR links: off"
	if m.forceCopyLinks {
		linkDesc = "force-copy ticket/PR links: on"
	}
	reorderDesc := "reorder session"
	if m.cfg.SortRecentFirst {
		reorderDesc = "reorder session (off, see settings)"
	}
	return []helpGroup{
		{
			title: "Sessions",
			entries: []helpEntry{
				{"n", "new session"},
				{"f", "find session (all projects)"},
				{"enter / o", "open (attach tmux)"},
				{"x", "park (kill tmux)"},
				{"d", "delete worktree"},
				{"a", archiveDesc},
				{"A", archivedDesc},
				{"t", "tag ticket / PR"},
				{"e", "edit session"},
			},
		},
		{
			title: "Navigation",
			entries: []helpEntry{
				{"↑ / k", "move up"},
				{"↓ / j", "move down"},
				// Plain-letter alternates share a row with the chord they stand in
				// for; the chords need terminal extended-keys support, the letters
				// don't. Keep these labels short — the overlay is height-limited.
				{"shift+↑↓ / KJ", reorderDesc},
				{"tab / ] / →", "next project"},
				{"shift+tab / [ / ←", "prev project"},
				{"} / {", "...incl. empty ones"},
				{"shift+←→ / HL", "reorder project"},
				{"R", linkDesc},
			},
		},
		{
			title: "Projects",
			entries: []helpEntry{
				{"/", "pick project"},
				{"D", "remove project"},
				// Adding/editing a project only happens inside the picker —
				// there's no main-list P/E anymore.
				{"n/e/d", "add/edit/remove (picker)"},
			},
		},
		{
			title: "General",
			entries: []helpEntry{
				{"?", "toggle this help"},
				{"q", "quit"},
				{"r", "refresh"},
				{"s", "settings (theme, sort mode, auto-tmux, auto-submit)"},
			},
		},
	}
}

// renderHelp renders the command reference shown behind the ModeHelp overlay.
// Groups are laid out two per row so the panel stays compact on typical
// terminals rather than scrolling off the bottom; on narrow terminals they
// stack one per row instead so the overlay never exceeds the screen width.
func (m *Model) renderHelp() string {
	groups := m.helpGroups()

	// Keep two columns when each has enough room to remain readable; otherwise
	// use the entire available width for one wrapping column.
	avail := m.overlayWidth(formHintWidth)
	perRow := 2
	if avail < 56 {
		perRow = 1
	}
	colWidth := avail / perRow
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
			prefix := "  " + key + pad + "  "
			descWidth := colWidth - lipgloss.Width(prefix)
			if descWidth < 8 {
				descWidth = 8
			}
			desc := helpDescStyle.Width(descWidth).Render(e.desc)
			b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, prefix, desc) + "\n")
		}
		cols[i] = lipgloss.NewStyle().Width(colWidth).Render(b.String())
	}

	var rows []string
	for i := 0; i < len(cols); i += perRow {
		if len(rows) > 0 {
			rows = append(rows, "") // blank line between group-pairs, not after the last
		}
		if perRow == 2 && i+1 < len(cols) {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cols[i], cols[i+1]))
		} else {
			rows = append(rows, cols[i])
		}
	}

	tableWidth := colWidth * perRow
	if len(groups) == 1 {
		tableWidth = colWidth
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("moomux commands"))
	b.WriteString("\n\n")
	b.WriteString(lipgloss.JoinVertical(lipgloss.Left, rows...))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Width(tableWidth).Render(
		"tip: park from agent: Claude /kill · Codex $kill",
	))
	b.WriteString("\n")
	b.WriteString(hintStyle.Width(tableWidth).Render(
		"tip: tag from agent: Claude /tag · Codex $tag",
	))
	return b.String()
}
