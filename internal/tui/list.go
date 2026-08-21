package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// linkHit records where a clickable ticket/PR icon landed within the
// rendered list, in the list panel's own local coordinates (line index and
// column range on that line). The TUI translates these to absolute terminal
// coordinates once the surrounding layout (header height, panel border) is
// known, so a click can be matched back to the URL to open.
type linkHit struct {
	sessionID  string
	url        string
	copyOnly   bool // force clipboard copy instead of browser.Open (e.g. a tmux command, not a URL)
	line       int
	col0, col1 int // half-open column range
}

// rowHit records a session row's line within the rendered list (local
// coordinates, like linkHit), so a mouse click landing anywhere on the row —
// not just on a ticket/PR icon — can select that session (or, in
// ModeMultiView, pick that row's project as the focused panel).
type rowHit struct {
	sessionID string
	line      int
}

func (m *Model) renderList(width, height int) (string, []linkHit, []rowHit) {
	var b strings.Builder
	title := "SESSIONS"
	empty := "  no sessions — press n to create"
	if len(m.projects) == 0 {
		empty = "  no projects yet — press / then n to add one"
	} else if m.showArchived {
		title = "ARCHIVED"
		empty = "  no archived sessions"
	} else if n := m.archivedCount(); n > 0 {
		title += superscript(n)
	}
	// On small screens the title (plus its blank line beneath) costs two
	// rows that are worth more as extra visible sessions than as a label —
	// including ARCHIVED, even though that means mobile has no on-screen
	// signal telling the archived view apart from the active one.
	compact := m.compactScreen()
	titleRows := 0
	if !compact {
		b.WriteString(titleStyle.Render(title))
		b.WriteString("\n\n")
		titleRows = 2
	}
	if len(m.sessions) == 0 {
		b.WriteString(muteStyle.Render(empty))
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String()), nil, nil
	}
	visible := height - titleRows
	if visible < 1 {
		visible = 1
	}
	start, end := scrollWindow(m.cursor, len(m.sessions), visible)
	hasAbove, hasBelow := start > 0, end < len(m.sessions)
	rowOffset := 0
	if hasAbove {
		b.WriteString(scrollHintLine("⌃", width))
		b.WriteString("\n")
		rowOffset = 1
	}
	var hits []linkHit
	var rows []rowHit
	for i := start; i < end; i++ {
		s := m.sessions[i]
		selected := i == m.cursor
		// titleRows lines for the "SESSIONS" title and blank line above (0 on
		// short terminals, where it's hidden), plus rowOffset for the "⌃ more
		// above" hint line (0 unless it's actually shown).
		line := titleRows + rowOffset + (i - start)
		rows = append(rows, rowHit{sessionID: s.ID, line: line})
		row, iconHits := renderRow(s, m.effectiveState(s), width-2, selected, "", m.gitStatus[s.ID])
		for _, h := range iconHits {
			h.sessionID = s.ID
			h.line = line
			// +1 column for the row style's own left padding.
			h.col0++
			h.col1++
			hits = append(hits, h)
		}
		if selected {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	if hasBelow {
		b.WriteString(scrollHintLine("⌄", width))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String()), hits, rows
}

// scrollWindow computes the [start, end) window into a total-item list of
// visible rows around cursor. When the list doesn't fully fit, it reserves a
// row at whichever edge(s) are actually cut off — one above for "more above"
// (⌃) if start ends up > 0, one below for "more below" (⌄) if end ends up <
// total — so the reservation lines up with where the caller actually draws
// the hint. Reserving is still driven only by (cursor, total, visible), so
// it never depends on incidental resize elsewhere on screen the way sizing
// the split off the selected session's own content used to (see
// maxDetailContentHeight).
func scrollWindow(cursor, total, visible int) (start, end int) {
	if total <= visible {
		return 0, total
	}
	start, end = clampWindow(cursor, total, visible)
	reserve := 0
	if start > 0 {
		reserve++
	}
	if end < total {
		reserve++
	}
	v := visible - reserve
	if v < 1 {
		v = 1
	}
	return clampWindow(cursor, total, v)
}

// clampWindow centers a visible-row window on cursor within [0, total),
// clamped so it never runs past either end of the list.
func clampWindow(cursor, total, visible int) (start, end int) {
	start = cursor - visible/2
	if start < 0 {
		start = 0
	}
	if max := total - visible; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	end = start + visible
	if end > total {
		end = total
	}
	return start, end
}

// scrollHintLine centers a single scrollWindow truncation glyph (⌃ or ⌄,
// DOWN/UP ARROWHEAD rather than the taller ▲/▼ triangles) within width, muted
// — a quieter, single-glyph cue that a truncated list shouldn't look
// identical to a complete one.
func scrollHintLine(glyph string, width int) string {
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(muteStyle.Render(glyph))
}

func renderRow(s session.Session, st watcher.State, width int, selected bool, projectLabel string, git gitStatusInfo) (string, []linkHit) {
	dotStyle := dotParkedStyle
	switch st {
	case watcher.Working:
		dotStyle = dotWorkingStyle
	case watcher.Done:
		dotStyle = dotDoneStyle
	case watcher.NeedsInput:
		dotStyle = dotNeedsInputStyle
	}
	iconTicketStyle, iconPRStyle, gitWarnStyle := iconTicketStyle, iconPRStyle, warnStyle
	if selected {
		dotStyle = dotStyle.Background(colSelBg)
		iconTicketStyle = iconTicketStyle.Background(colSelBg)
		iconPRStyle = iconPRStyle.Background(colSelBg)
		gitWarnStyle = gitWarnStyle.Background(colSelBg)
	}
	dot := dotStyle.Render("⬤")

	// Candidate icons in priority order — ticket/PR are clickable links and
	// stay longest; the git-status icons are the newest, lowest-priority
	// addition and are the first dropped when width is tight. Building this
	// list up front (rather than unconditionally rendering every icon) lets
	// the loop below drop from the end until the row actually fits width,
	// instead of just flooring nameWidth at 4 and letting a wide suffix blow
	// through the caller's requested width on narrow terminals.
	type iconCandidate struct {
		style      lipgloss.Style
		glyph, url string
	}
	var candidates []iconCandidate
	if s.Ticket != "" {
		candidates = append(candidates, iconCandidate{iconTicketStyle, "🎫", s.Ticket})
	}
	if s.PR != "" {
		candidates = append(candidates, iconCandidate{iconPRStyle, "🔀", s.PR})
	}
	if git.ok && git.dirty {
		candidates = append(candidates, iconCandidate{gitWarnStyle, "±", ""})
	}
	if git.ok && git.unpushed {
		candidates = append(candidates, iconCandidate{gitWarnStyle, "↑", ""})
	}
	const minNameWidth = 4
	dotWidth := lipgloss.Width(dot)
	for len(candidates) > 0 {
		iconsWidth := 0
		for _, c := range candidates {
			iconsWidth += lipgloss.Width(c.glyph) + 1
		}
		if width-1-iconsWidth-dotWidth >= minNameWidth {
			break
		}
		candidates = candidates[:len(candidates)-1]
	}

	var icons string
	var hits []linkHit
	col := 0
	addIcon := func(style lipgloss.Style, glyph, url string) {
		w := lipgloss.Width(glyph)
		hits = append(hits, linkHit{url: url, col0: col, col1: col + w})
		icons += style.Render(glyph) + style.Render(" ")
		col += w + 1
	}
	for _, c := range candidates {
		addIcon(c.style, c.glyph, c.url)
	}
	suffix := icons + dot
	nameWidth := width - 1 - lipgloss.Width(suffix)
	if nameWidth < minNameWidth {
		nameWidth = minNameWidth
	}
	var prefix string
	if projectLabel != "" {
		// Budget at most half the name column for the project tag, so a long
		// project name can't crowd the session name down to nothing.
		labelWidth := nameWidth / 2
		if labelWidth > lipgloss.Width(projectLabel)+1 {
			labelWidth = lipgloss.Width(projectLabel) + 1
		}
		if labelWidth > 0 {
			label := truncate(projectLabel, labelWidth-1)
			// Width() (not fmt's %-*s, which pads by rune count) pads to the
			// true terminal column budget — emoji tags are 1 rune but 2
			// display columns, so rune-count padding under-budgeted by a
			// column and threw off every position after it, including the
			// selected-row background.
			style := muteStyle.Width(labelWidth)
			if selected {
				style = style.Background(colSelBg)
			}
			prefix = style.Render(label)
			nameWidth -= labelWidth
			// Unlike the floor applied above (before the label was known),
			// re-flooring this back up to 4 would grow the row past the
			// width budget the icon-dropping logic upstream already fit to
			// — at extremely tight widths the name column degrades toward
			// empty instead, same as icons already do.
			if nameWidth < 0 {
				nameWidth = 0
			}
		}
	}
	// The name text needs its own explicit style (not just reliance on the
	// caller's outer Background wrap): when a prefix is present, its Render()
	// call emits a closing ANSI reset that would otherwise wipe out any
	// color set before it, leaving this plain text with no background at
	// all — the exact bug behind the selected-row highlight vanishing once
	// a project-emoji prefix was introduced.
	nameStyle := lipgloss.NewStyle()
	sepStyle := lipgloss.NewStyle()
	if selected {
		nameStyle = nameStyle.Background(colSelBg).Foreground(colFg).Bold(true)
		sepStyle = sepStyle.Background(colSelBg)
	}
	name := prefix + nameStyle.Render(fmt.Sprintf("%-*s", nameWidth, truncate(s.Name, nameWidth)))
	offset := nameWidth + lipgloss.Width(prefix) + 1
	for i := range hits {
		hits[i].col0 += offset
		hits[i].col1 += offset
	}
	return name + sepStyle.Render(" ") + suffix, hits
}

// projectEmojiPalette is the fallback set for projects that haven't chosen
// their own emoji (config.Project.Emoji) — picked deterministically per
// project name so the same project always gets the same glyph.
var projectEmojiPalette = []string{"🐙", "🦊", "🚀", "🔥", "🌊", "🍀", "⚡", "🎯", "🐝", "🦉"}

// projectEmojiChoices is the project-form emoji selector's cycle order:
// "auto" (index 0, the deterministic-pick sentinel) followed by the palette.
var projectEmojiChoices = append([]string{"auto"}, projectEmojiPalette...)

// projectEmojiFieldValue converts a projectForm.emojiIdx into the
// config.Project.Emoji value to store: "" for auto (idx 0), else the picked
// glyph — from choices (normally projectEmojiChoices, but editProjectForm
// may have inserted the project's existing out-of-palette emoji into it).
func projectEmojiFieldValue(choices []string, idx int) string {
	if idx <= 0 || idx >= len(choices) {
		return ""
	}
	return choices[idx]
}

func cycleProjectEmojiIdx(choices []string, idx, delta int) int {
	n := len(choices)
	return (idx + delta + n) % n
}

// projectEmoji returns the project's configured emoji, falling back to a
// deterministic pick from projectEmojiPalette if none is set.
func (m *Model) projectEmoji(project string) string {
	if e := m.cfg.Projects[project].Emoji; e != "" {
		return e
	}
	var h int
	for _, r := range project {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return projectEmojiPalette[h%len(projectEmojiPalette)]
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	if n < 2 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// truncateLeft keeps the end of a value visible. It is useful for read-only
// paths, where the directory or branch name at the right is usually more
// informative than a common leading prefix.
func truncateLeft(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 2 {
		return string(r[len(r)-n:])
	}
	return "…" + string(r[len(r)-(n-1):])
}
