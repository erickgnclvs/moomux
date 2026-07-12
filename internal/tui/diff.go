package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
)

// formatDiffStat renders a session.DiffStat as a compact one-liner for the
// detail pane, e.g. "3 files  +82 −14", or "clean" when there are no changes.
func formatDiffStat(d session.DiffStat) string {
	if d.Clean() {
		return muteStyle.Render("clean")
	}
	unit := "files"
	if d.Files == 1 {
		unit = "file"
	}
	adds := statAddStyle.Render(fmt.Sprintf("+%d", d.Additions))
	dels := statDelStyle.Render(fmt.Sprintf("−%d", d.Deletions))
	return fmt.Sprintf("%d %s  %s %s", d.Files, unit, adds, dels)
}

// colorizeDiff applies syntax coloring to raw `git diff` output: added lines
// green, removed lines red, hunk headers accent, and file/metadata headers
// muted-bold. Leading +++/--- file markers are treated as metadata, not
// add/remove lines, so they don't drown the header in color.
func colorizeDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			lines[i] = diffMetaStyle.Render(line)
		case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"),
			strings.HasPrefix(line, "rename "), strings.HasPrefix(line, "similarity "):
			lines[i] = diffMetaStyle.Render(line)
		case strings.HasPrefix(line, "@@"):
			lines[i] = diffHunkStyle.Render(line)
		case strings.HasPrefix(line, "+"):
			lines[i] = diffAddStyle.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = diffDelStyle.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// diffWidth and diffHeight size the diff viewport to fill the screen minus a
// 1-column side margin and the four chrome lines renderDiff adds (title +
// blank + blank + hints). They floor at small positive values so a tiny
// terminal never produces a zero/negative-sized viewport.
func (m *Model) diffWidth() int {
	w := m.width - 2
	if w < 10 {
		w = 10
	}
	return w
}

func (m *Model) diffHeight() int {
	h := m.height - 4
	if h < 3 {
		h = 3
	}
	return h
}

// renderDiff draws the full-screen diff view: a title bar, the scrollable
// viewport (or an error/empty message), and a hints row.
func (m *Model) renderDiff() string {
	title := titleStyle.Render("DIFF") + "  " + muteStyle.Render(m.diffTitle)

	var body string
	switch {
	case m.diffLoading:
		body = muteStyle.Render("loading diff…")
	case m.diffErr != "":
		body = errorFlashStyle.Render(m.diffErr)
	case strings.TrimSpace(m.diffVP.View()) == "":
		body = muteStyle.Render("no changes — worktree matches its base branch")
	default:
		body = m.diffVP.View()
	}

	scrollPct := fmt.Sprintf("%3.0f%%", m.diffVP.ScrollPercent()*100)
	hints := muteStyle.Render("↑/↓ scroll  ·  space/f pgdn  ·  b pgup  ·  esc/q close") +
		lipgloss.NewStyle().Foreground(colMute).Render("   "+scrollPct)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", hints)
}
