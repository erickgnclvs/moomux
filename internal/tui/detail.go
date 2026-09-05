package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func (m *Model) renderDetail(width, height int) (string, []linkHit) {
	// The side-by-side layout's list pane reserves 2 rows for its own
	// "SESSIONS" title (see renderList's compact check) whenever
	// !m.compactScreen() — matching that here, gap but no text, keeps both
	// columns' content starting on the same row instead of drifting out of
	// alignment now that neither pane prints a title.
	titleGap := !m.compactScreen()
	if len(m.sessions) == 0 {
		return m.renderDetailFor(session.Session{}, false, width, height, titleGap)
	}
	return m.renderDetailFor(m.sessions[m.cursor], true, width, height, titleGap)
}

// renderDetailFor is renderDetail's body, parameterized on an explicit
// session instead of always reading m.sessions[m.cursor] — shared by the
// normal list+detail layout and ModeMultiView's per-project detail panel,
// which each have their own notion of "the selected session". There's no
// "DETAIL" title text anywhere; titleGap only controls whether its blank-row
// spacing is still reserved (see renderDetail) — ModeMultiView's panels
// never need it, having no side-by-side sibling column to line up with.
func (m *Model) renderDetailFor(s session.Session, hasSelection bool, width, height int, titleGap bool) (string, []linkHit) {
	content, allHits, preCowLines := m.renderDetailContent(s, hasSelection, width, titleGap)
	// Anchor the closing cowsay block to the bottom of height instead of
	// leaving it wherever the (variable-length) fields above it happen to
	// end: renderMultiPanel/renderListView now size height off the detail
	// panel's structural worst case (maxDetailContentHeight), so a session
	// with fewer fields than that worst case would otherwise leave the cow
	// sitting higher up — visibly hopping between rows as you move the
	// cursor between sessions with different field counts, even though the
	// box itself no longer resizes. preCowLines is -1 when there's no cow
	// block to anchor (nothing selected, or compact narrow mode already
	// shows one in the header) — leave those top-anchored as before.
	if preCowLines >= 0 {
		contentHeight := lipgloss.Height(lipgloss.NewStyle().Width(width).Render(content))
		if pad := height - contentHeight; pad > 0 {
			lines := strings.Split(content, "\n")
			padded := make([]string, 0, len(lines)+pad)
			padded = append(padded, lines[:preCowLines]...)
			for i := 0; i < pad; i++ {
				padded = append(padded, "")
			}
			padded = append(padded, lines[preCowLines:]...)
			content = strings.Join(padded, "\n")
		}
	}
	var hits []linkHit
	for _, h := range allHits {
		// MaxHeight below clips the rendered detail. Do not leave invisible
		// link targets behind in footer or border coordinates. Padding
		// above never shifts these: every hit is emitted from a field or
		// prompt row, always before preCowLines.
		if h.line < height {
			hits = append(hits, h)
		}
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(content), hits
}

// detailContentHeight reports how many rows renderDetailContent's output for
// s actually takes at width — its content (fields, wrapped prompt, closing
// cowsay art) varies per session, so renderMultiPanel measures it rather than
// guessing, sizing each panel's detail section around what it truly needs
// instead of a flat fraction of the panel that starves the cow whenever the
// session list next to it is long.
func (m *Model) detailContentHeight(s session.Session, hasSelection bool, width int) int {
	content, _, _ := m.renderDetailContent(s, hasSelection, width, false)
	return lipgloss.Height(lipgloss.NewStyle().Width(width).Render(content))
}

// maxDetailContentHeight reports the tallest the detail panel can ever be at
// width: every optional field present, the prompt wrapped to its 3-line cap,
// and full cowsay art. renderListView and renderMultiPanel size their
// list/detail split off this instead of the selected session's own height,
// so switching the selected session never changes how many rows the list
// gets — the split only moves when width (or compact mode) does.
func (m *Model) maxDetailContentHeight(width int) int {
	worst := session.Session{
		Project:      "x",
		Name:         "x",
		Ticket:       "https://x",
		PR:           "https://x",
		WorktreePath: "x",
		CreatedAt:    time.Now(),
		Prompt:       strings.Repeat("word ", 60),
	}
	// +2 for the "git" and "pr status" rows: both are keyed off
	// m.gitStatus/m.prStatus by session id, which this unattached probe
	// session can never populate, but both are always exactly one
	// unwrapped line when present.
	return m.detailContentHeight(worst, true, width) + 2
}

// renderDetailContent builds renderDetailFor's content and link hits without
// applying its final Height/MaxHeight clipping — shared by renderDetailFor
// (which then clips to the space actually available, drops hits that land
// past it, and bottom-anchors the closing cowsay block using the returned
// preCowLines) and detailContentHeight (which measures the unclipped result
// to decide how much space to give it in the first place). preCowLines is
// the line count of everything before the blank+quip+cow block, or -1 when
// there's no such block to anchor (nothing selected, or compact narrow mode
// already shows a cow in the header).
func (m *Model) renderDetailContent(s session.Session, hasSelection bool, width int, titleGap bool) (string, []linkHit, int) {
	var b strings.Builder
	if titleGap {
		b.WriteString("\n\n")
	}
	if !hasSelection {
		b.WriteString(muteStyle.Render("nothing selected"))
		return b.String(), nil, -1
	}
	st := m.effectiveState(s)
	dot := dotParked
	label := "in the barn"
	switch st {
	case watcher.Working:
		dot, label = dotWorking, "grazing"
	case watcher.Done:
		dot, label = dotDone, "chewing cud"
	case watcher.NeedsInput:
		dot, label = dotNeedsInput, "mooing for you"
	}
	var hits []linkHit
	rowLink := func(k, v, url string, copyOnly bool) {
		// Measure the rendered height, not logical newlines: the final
		// Width(width) render *wraps* long rows rather than clipping, and a
		// wrapped row above would shift every later hitbox down.
		line := lipgloss.Height(lipgloss.NewStyle().Width(width).Render(b.String())) - 1
		key := muteStyle.Render(fmt.Sprintf("%-10s", k+":"))
		if url != "" {
			col0 := lipgloss.Width(key) + 1
			col1 := min(width, col0+lipgloss.Width(v))
			if col0 < col1 {
				hits = append(hits, linkHit{
					sessionID: s.ID,
					url:       url,
					copyOnly:  copyOnly,
					line:      line,
					col0:      col0,
					col1:      col1,
				})
			}
			v = renderLink(v)
		}
		b.WriteString(fmt.Sprintf("%s %s\n", key, v))
	}
	row := func(k, v, url string) { rowLink(k, v, url, false) }
	valueWidth := width - 14
	if valueWidth < 8 {
		valueWidth = 8
	}
	compact := m.cfg.CompactDetail
	// Ordered most-to-least useful for "what's this session and does it need
	// me": identity/state first, then actionable links, then reference
	// details a user only needs occasionally.
	if !compact {
		row("project", truncate(s.Project, valueWidth), "")
	}
	row("status", dot+"  "+label, "")
	row("name", truncate(s.Name, valueWidth), "")
	if !compact {
		row("agent", s.AgentName(), "")
	}
	if git := m.gitStatus[s.ID]; git.ok {
		row("git", gitStatusLabel(git), "")
	}
	if s.Ticket != "" && !compact {
		row("ticket", truncateLeft(s.Ticket, valueWidth), s.Ticket)
	}
	if s.PR != "" {
		prValue := truncateLeft(s.PR, valueWidth)
		if compact {
			prValue = prNumberLabel(s.PR)
		}
		row("pr", prValue, s.PR)
		if pr := m.prStatus[s.ID]; pr.ok {
			row("pr status", prStatusLabel(pr.info), "")
		}
	}
	rowLink("tmux", truncate(s.TmuxSession, valueWidth), "tmux attach -t "+s.TmuxSession, true)
	// worktree/created are reference details rarely needed at a glance —
	// on mobile (or in compact mode) they cost rows better spent on the
	// prompt/cow below, which is more often what you'd scroll for.
	if !m.compactScreen() && !compact {
		row("worktree", truncateLeft(s.WorktreePath, valueWidth), "")
		row("created", humanizeAge(time.Since(s.CreatedAt)), "")
	}
	prompt := m.prompts[s.ID]
	if prompt == "" {
		prompt = s.Prompt
	}
	if prompt != "" {
		oneline := strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", " "), "\n", " ")
		const maxPromptLines = 3
		lines := wrapLines(oneline, valueWidth)
		if len(lines) > maxPromptLines {
			lines = lines[:maxPromptLines]
			last := []rune(lines[maxPromptLines-1])
			if len(last) > valueWidth-1 {
				last = last[:valueWidth-1]
			}
			lines[maxPromptLines-1] = string(last) + "…"
		}
		key := muteStyle.Render(fmt.Sprintf("%-10s", "prompt:"))
		blank := muteStyle.Render(fmt.Sprintf("%-10s", ""))
		for i, ln := range lines {
			label := blank
			if i == 0 {
				label = key
			}
			line := lipgloss.Height(lipgloss.NewStyle().Width(width).Render(b.String())) - 1
			col0 := lipgloss.Width(label) + 1
			col1 := min(width, col0+lipgloss.Width(ln))
			if col0 < col1 {
				hits = append(hits, linkHit{sessionID: s.ID, url: oneline, copyOnly: true, line: line, col0: col0, col1: col1})
			}
			b.WriteString(fmt.Sprintf("%s %s\n", label, renderLink(ln)))
		}
	}
	// In compact mode, narrow layouts already show this same small cow (and
	// its quip) in the header — see renderHeader's m.width < narrowWidthBreak
	// branch — so repeating it here would just be the same critter twice.
	preCowLines := -1
	if !compact || m.width >= narrowWidthBreak {
		preCowLines = lipgloss.Height(lipgloss.NewStyle().Width(width).Render(b.String()))
		b.WriteString("\n")
		quip := pickQuip(s.ID, quipPool(st))
		if compact {
			b.WriteString(cowStyle.Render(cowsaySmall(quip, valueWidth+10, st)))
		} else {
			b.WriteString(cowStyle.Render(cowsay(quip, valueWidth+10, st)))
		}
	}
	return b.String(), hits, preCowLines
}

// prNumberLabel shortens a PR URL to just its number (e.g.
// ".../pull/5478" -> "#5478") for the compact detail view, where the ticket
// link is dropped and the PR is the one link kept.
func prNumberLabel(url string) string {
	trimmed := strings.TrimRight(url, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	if trimmed == "" {
		return url
	}
	return "#" + trimmed
}

// gitStatusLabel renders a gitStatusInfo (git.ok must already be true) as the
// short text shown in the detail panel's "git" row.
func gitStatusLabel(git gitStatusInfo) string {
	var parts []string
	if git.dirty {
		parts = append(parts, "uncommitted changes")
	}
	if git.unpushed {
		parts = append(parts, "unpushed commits")
	}
	if len(parts) == 0 {
		return "clean, pushed"
	}
	return strings.Join(parts, ", ")
}

// changeSummaryLabel renders the delete dialog's warning detail: gitStatusLabel's
// wording, with file/commit counts appended when the summary fetch resolved
// in time (it's fetched alongside git status, but isn't gated on — the
// dialog still opens and can be confirmed without it).
func changeSummaryLabel(git gitStatusInfo, sum changeSummary) string {
	label := gitStatusLabel(git)
	if !sum.ok {
		return label
	}
	var parts []string
	if git.dirty {
		parts = append(parts, pluralCount(sum.filesChanged, "file", "files")+" changed")
	}
	if git.unpushed {
		parts = append(parts, pluralCount(sum.unpushedCommits, "commit", "commits")+" unpushed")
	}
	if len(parts) == 0 {
		return label
	}
	return strings.Join(parts, ", ")
}

// pluralCount renders n with singular or plural noun ("1 file", "3 files").
func pluralCount(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// prStatusLabel renders a prstatus.Info (pr.ok must already be true) as the
// short text shown in the detail panel's "pr status" row. Merged/closed wins
// outright since mergeable/CI stop meaning anything once the PR is done.
func prStatusLabel(info prstatus.Info) string {
	switch info.State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	}
	var parts []string
	if info.Mergeable == "CONFLICTING" {
		parts = append(parts, "conflicts")
	}
	switch info.CI {
	case "FAILING":
		parts = append(parts, "CI failing")
	case "PENDING":
		parts = append(parts, "CI running")
	case "PASSING":
		parts = append(parts, "CI passing")
	}
	if len(parts) == 0 {
		return "open"
	}
	return "open, " + strings.Join(parts, ", ")
}

func cowsay(msg string, maxWidth int, st watcher.State) string {
	const lineMax = 38
	w := lineMax
	if maxWidth > 0 && maxWidth < w {
		w = maxWidth
	}
	lines := wrapLines(msg, w)
	if len(lines) > 4 {
		lines = lines[:4]
		r := []rune(lines[3])
		if len(r) > w-1 {
			r = r[:w-1]
		}
		lines[3] = string(r) + "…"
	}
	border := strings.Repeat("_", w+2)
	var b strings.Builder
	b.WriteString(" " + border + "\n")
	for i, l := range lines {
		pad := w - len([]rune(l))
		padded := l + strings.Repeat(" ", pad)
		switch {
		case len(lines) == 1:
			b.WriteString("< " + padded + " >\n")
		case i == 0:
			b.WriteString("/ " + padded + " \\\n")
		case i == len(lines)-1:
			b.WriteString("\\ " + padded + " /\n")
		default:
			b.WriteString("| " + padded + " |\n")
		}
	}
	b.WriteString(" " + strings.Repeat("-", w+2) + "\n")
	eyes := stateEyes(st)
	b.WriteString(`        \   ^__^` + "\n")
	b.WriteString("         \\  (" + eyes + `)\_______` + "\n")
	b.WriteString(`            (__)\       )\/\` + "\n")
	b.WriteString(`                ||----w |` + "\n")
	b.WriteString(`                ||     ||`)
	return b.String()
}

// cowsaySmall is cowsay's compact-detail counterpart: the same small cow
// shown in the header (see headerCowArt) sitting beside its one-line quip,
// instead of the full speech-bubble-and-body art — a fraction of the rows,
// while still actually reading as a cow.
func cowsaySmall(msg string, maxWidth int, st watcher.State) string {
	cow := cowStyle.Render(headerCowArt(stateEyes(st), false, true))
	quipWidth := maxWidth - lipgloss.Width(cow) - 2
	if quipWidth < 3 {
		return cow
	}
	quip := muteStyle.Render(truncateToWidth(msg, quipWidth))
	return lipgloss.JoinHorizontal(lipgloss.Center, cow, "  ", quip)
}

func wrapLines(s string, width int) []string {
	s = strings.ReplaceAll(s, "\r", "")
	var out []string
	for _, line := range strings.Split(s, "\n") {
		runes := []rune(strings.TrimSpace(line))
		for len(runes) > width {
			cut := width
			for i := width - 1; i > width-15 && i > 0; i-- {
				if runes[i] == ' ' {
					cut = i
					break
				}
			}
			out = append(out, string(runes[:cut]))
			runes = []rune(strings.TrimLeft(string(runes[cut:]), " "))
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
	}
	return out
}

func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
