package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// displayLine is one rendered line of the session list: either a real
// session (sessionIdx indexes m.sessions) or a folder header (folder is the
// name, sessionIdx meaningless).
type displayLine struct {
	folder     string
	sessionIdx int
}

// buildDisplayLines groups m.sessions — already in final display order — by
// folder: the first session belonging to a not-yet-seen folder gets a header
// line right before it, followed immediately by every other session sharing
// that folder (wherever it lands in m.sessions), so a folder's members
// always render as one contiguous, indented block. Collapsed folders never
// have members in m.sessions at all (see refreshSessions), so they have no
// member to anchor a header on; those are spliced in afterward at the
// position their FolderMeta.Order says they belong, compared directly
// against the Order of the surrounding lines (an expanded folder's own
// position in that comparison is its anchor member's Order, matching how
// Session.Order already places it in m.sessions).
func (m *Model) buildDisplayLines() []displayLine {
	seen := map[string]bool{}
	var lines []displayLine
	var orders []int64 // orders[i] is lines[i]'s Order, for splicing collapsed folders in below
	for i, s := range m.sessions {
		if s.Folder == "" {
			lines = append(lines, displayLine{sessionIdx: i})
			orders = append(orders, s.Order)
			continue
		}
		if seen[s.Folder] {
			continue
		}
		seen[s.Folder] = true
		lines = append(lines, displayLine{folder: s.Folder})
		orders = append(orders, s.Order)
		for j, other := range m.sessions {
			if other.Folder == s.Folder {
				lines = append(lines, displayLine{sessionIdx: j})
				orders = append(orders, other.Order)
			}
		}
	}
	if len(m.projects) == 0 {
		return lines
	}
	proj := m.projects[m.activeProj]
	var collapsed []string
	for name := range m.cfg.Projects[proj].Folders {
		if !seen[name] {
			collapsed = append(collapsed, name)
		}
	}
	sort.Strings(collapsed) // stable tiebreak among folders landing at the same position
	for _, name := range collapsed {
		order := m.cfg.Projects[proj].Folders[name].Order
		at := len(lines)
		for i, o := range orders {
			if o > order {
				at = i
				break
			}
		}
		lines = append(lines, displayLine{})
		copy(lines[at+1:], lines[at:])
		lines[at] = displayLine{folder: name}
		orders = append(orders, 0)
		copy(orders[at+1:], orders[at:])
		orders[at] = order
	}
	return lines
}

// cursorDisplayLine finds cursor's (an m.sessions index) position within
// lines, so scrolling/rendering can operate on display-line coordinates
// (which include folder headers) while m.cursor stays a plain m.sessions
// index everywhere else in the codebase.
func cursorDisplayLine(lines []displayLine, cursor int) int {
	for i, l := range lines {
		if l.folder == "" && l.sessionIdx == cursor {
			return i
		}
	}
	return 0
}

// visualSessionOrder returns m.sessions indices in the order sessions are
// actually displayed (buildDisplayLines' session lines, skipping folder
// headers). updateList's Up/Down cursor movement and MoveUp/MoveDown
// reordering must walk this instead of m.sessions' own index order: a
// folder's members aren't necessarily contiguous in m.sessions (Folder
// membership doesn't constrain Order — see SetSessionFolder), only in the
// grouped display buildDisplayLines produces, so m.cursor±1 can silently
// point at a session other than the one visually above/below.
func (m *Model) visualSessionOrder() []int {
	lines := m.buildDisplayLines()
	order := make([]int, 0, len(m.sessions))
	for _, l := range lines {
		if l.folder == "" {
			order = append(order, l.sessionIdx)
		}
	}
	return order
}

// renderFolderHeaderLine renders one collapsible-group header row. It is
// decoration only — never cursor-addressable or clickable; collapsing,
// renaming, and deleting a folder go through the Folders (G) overlay instead
// (see internal/tui/folders.go), so this needs no hit-testing of its own.
func (m *Model) renderFolderHeaderLine(name string, collapsed bool, count int, width int) string {
	glyph := "▾"
	if collapsed {
		glyph = "▸"
	}
	text := fmt.Sprintf("%s %s (%d)", glyph, name, count)
	return muteStyle.Render(truncate(text, width))
}

// folderMemberCount returns how many of the active project's sessions
// (matching the current archived view) are filed under folder — used for a
// collapsed folder's header count, since its members are excluded from
// m.sessions entirely while collapsed.
func (m *Model) folderMemberCount(folder string) int {
	if len(m.projects) == 0 {
		return 0
	}
	proj := m.projects[m.activeProj]
	n := 0
	for _, s := range m.backend.Sessions() {
		if s.Project == proj && s.Folder == folder && s.Archived == m.showArchived {
			n++
		}
	}
	return n
}

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
// not just on a ticket/PR icon — can select and open that session.
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
	lines := m.buildDisplayLines()
	if len(lines) == 0 {
		b.WriteString(muteStyle.Render(empty))
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(b.String()), nil, nil
	}
	visible := height - titleRows
	if visible < 1 {
		visible = 1
	}
	cursorLine := cursorDisplayLine(lines, m.cursor)
	start, end := scrollWindow(cursorLine, len(lines), visible)
	hasAbove, hasBelow := start > 0, end < len(lines)
	rowOffset := 0
	if hasAbove {
		b.WriteString(scrollHintLine("⌃", width))
		b.WriteString("\n")
		rowOffset = 1
	}
	var hits []linkHit
	var rows []rowHit
	for li := start; li < end; li++ {
		dl := lines[li]
		// titleRows lines for the "SESSIONS" title and blank line above (0 on
		// short terminals, where it's hidden), plus rowOffset for the "⌃ more
		// above" hint line (0 unless it's actually shown).
		line := titleRows + rowOffset + (li - start)
		if dl.folder != "" {
			collapsed := m.cfg.Projects[m.projects[m.activeProj]].Folders[dl.folder].Collapsed
			count := m.folderMemberCount(dl.folder)
			b.WriteString(m.renderFolderHeaderLine(dl.folder, collapsed, count, width))
			b.WriteString("\n")
			continue
		}
		s := m.sessions[dl.sessionIdx]
		selected := dl.sessionIdx == m.cursor
		indent := ""
		rowWidth := width - 2
		if s.Folder != "" {
			indent = "  "
			rowWidth -= 2
		}
		rows = append(rows, rowHit{sessionID: s.ID, line: line})
		row, iconHits := renderRow(s, m.effectiveState(s), rowWidth, selected, "", m.gitStatus[s.ID])
		colOffset := 1 + len(indent) // +1 for the row style's own left padding
		for _, h := range iconHits {
			h.sessionID = s.ID
			h.line = line
			h.col0 += colOffset
			h.col1 += colOffset
			hits = append(hits, h)
		}
		row = indent + row
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
