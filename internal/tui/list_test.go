package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// TestRenderRowProjectLabelFitsWidthBudget guards against the project-label
// prefix (a wide emoji glyph — 1 rune but 2 terminal columns) overflowing the
// width budget passed to renderRow, which happened when the prefix was
// padded with fmt's rune-counting %-*s instead of a display-width-aware
// style. An overflowing row breaks the fixed-width list panel it's rendered
// into.
func TestRenderRowProjectLabelFitsWidthBudget(t *testing.T) {
	s := session.Session{Name: "feature-auth", Ticket: "https://x/1", PR: "https://x/2"}
	for _, width := range []int{20, 30, 40, 60} {
		row, _ := renderRow(s, watcher.Working, width, false, "🚀", gitStatusInfo{})
		if got := lipgloss.Width(row); got > width {
			t.Fatalf("width %d: rendered row width = %d, want <= %d (row=%q)", width, got, width, row)
		}
	}
}

// TestRenderRowGitStatusIconsFitWidthBudget guards against the dirty/
// unpushed git-status icons (± and ↑) overflowing the width budget on
// narrow terminals: with ticket, PR, dirty, and unpushed all present, the
// icon suffix is wide enough that flooring nameWidth at 4 without capping
// the icon count let the row exceed its requested width at widths as high
// as 14-15 — the same overflow class TestRenderRowProjectLabelFitsWidthBudget
// guards for ticket/PR alone.
func TestRenderRowGitStatusIconsFitWidthBudget(t *testing.T) {
	s := session.Session{Name: "feature-auth", Ticket: "https://x/1", PR: "https://x/2"}
	git := gitStatusInfo{ok: true, dirty: true, unpushed: true}
	for width := 8; width <= 40; width++ {
		row, _ := renderRow(s, watcher.Working, width, false, "🚀", git)
		if got := lipgloss.Width(row); got > width {
			t.Fatalf("width %d: rendered row width = %d, want <= %d (row=%q)", width, got, width, row)
		}
	}
}

// TestRenderRowLinkHitOffsetsAccountForProjectLabel guards the ticket/PR
// link-hit column math when a project-label prefix is present: nameWidth is
// shrunk by exactly the label's rendered width, so the icons' column budget
// — and thus the hit offset — should be unchanged from the no-label case,
// and the hit must always land inside the row's own rendered width.
func TestRenderRowLinkHitOffsetsAccountForProjectLabel(t *testing.T) {
	s := session.Session{Name: "feature-auth", Ticket: "https://x/1"}
	withLabel, hitsWithLabel := renderRow(s, watcher.Parked, 40, false, "🚀", gitStatusInfo{})
	_, hitsNoLabel := renderRow(s, watcher.Parked, 40, false, "", gitStatusInfo{})

	if len(hitsWithLabel) != 1 || len(hitsNoLabel) != 1 {
		t.Fatalf("expected exactly one ticket link hit each, got %d and %d", len(hitsWithLabel), len(hitsNoLabel))
	}
	if hitsWithLabel[0].col0 != hitsNoLabel[0].col0 {
		t.Fatalf("hit col0 = %d with label, %d without — the label's width should be fully absorbed by shrinking nameWidth", hitsWithLabel[0].col0, hitsNoLabel[0].col0)
	}
	// The hit must land inside the rendered row's own width, not past it.
	if got := lipgloss.Width(withLabel); hitsWithLabel[0].col1 > got {
		t.Fatalf("hit col1=%d falls outside rendered row width=%d", hitsWithLabel[0].col1, got)
	}
}

// TestRenderRowSelectedBackgroundCoversWholeRow guards against the plain
// session-name text silently losing its selected-row background: the
// project-label prefix's own Render() call emits an ANSI reset that, if the
// name text isn't given its own explicit style, wipes out any background
// applied before it, leaving a gap in the highlight.
func TestRenderRowSelectedBackgroundCoversWholeRow(t *testing.T) {
	// Force SGR codes to be emitted regardless of terminal detection; this is
	// process-global state, so it must be restored or every later test in
	// this package's binary renders with a different color profile than it
	// expects.
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(2)
	defer lipgloss.SetColorProfile(orig)
	s := session.Session{Name: "feature-auth"}
	row, _ := renderRow(s, watcher.Parked, 40, true, "🚀", gitStatusInfo{})
	rendered := listRowSelected.Render(row)

	idx := strings.Index(rendered, "feature-auth")
	if idx < 0 {
		t.Fatalf("session name not found in rendered row:\n%q", rendered)
	}
	// The text must be immediately preceded by a non-bare SGR "set"
	// sequence — a bare "\x1b[0m" (or no escape at all) right before it
	// means a prior span's reset wiped out any background, leaving the name
	// with no styling of its own.
	start := strings.LastIndex(rendered[:idx], "\x1b[")
	if start < 0 {
		t.Fatalf("no preceding SGR sequence before session name:\n%q", rendered)
	}
	end := strings.Index(rendered[start:idx], "m") + start
	code := rendered[start+2 : end]
	if code == "" || code == "0" {
		t.Fatalf("session name sits right after a bare/empty reset (code=%q) — background is lost:\n%q", code, rendered)
	}
}

func TestProjectEmojiFieldValue(t *testing.T) {
	choices := []string{"auto", "🐙", "🦊"}
	cases := []struct {
		idx  int
		want string
	}{
		{-1, ""},  // below range
		{0, ""},   // auto sentinel
		{1, "🐙"},  // first real choice
		{2, "🦊"},  // last real choice
		{3, ""},   // at len(choices), out of range
		{100, ""}, // far out of range
	}
	for _, c := range cases {
		if got := projectEmojiFieldValue(choices, c.idx); got != c.want {
			t.Errorf("projectEmojiFieldValue(choices, %d) = %q, want %q", c.idx, got, c.want)
		}
	}
}

func TestCycleProjectEmojiIdx(t *testing.T) {
	choices := []string{"auto", "🐙", "🦊", "🚀"} // len 4

	cases := []struct {
		idx, delta int
		want       int
	}{
		{0, 1, 1},
		{1, 1, 2},
		{3, 1, 0},  // wraps forward off the end
		{0, -1, 3}, // wraps backward off the start
		{2, -1, 1},
	}
	for _, c := range cases {
		if got := cycleProjectEmojiIdx(choices, c.idx, c.delta); got != c.want {
			t.Errorf("cycleProjectEmojiIdx(choices, %d, %d) = %d, want %d", c.idx, c.delta, got, c.want)
		}
	}
}

// TestBuildDisplayLinesPositionsCollapsedFolderByOrder guards against a
// collapsed folder always sinking to the bottom of the list: since none of
// its members are in m.sessions while collapsed, there's no visible member
// to anchor its header on, so its position must come from FolderMeta.Order
// compared against the surrounding sessions' own Order instead.
func TestBuildDisplayLinesPositionsCollapsedFolderByOrder(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	proj := m.cfg.Projects["demo"]
	proj.Folders = map[string]config.FolderMeta{
		"grp": {Collapsed: true, Order: 2},
	}
	m.cfg.Projects["demo"] = proj
	m.sessions = []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a", Order: 1},
		{ID: "demo:z", Project: "demo", Name: "z", Order: 3},
	}

	lines := m.buildDisplayLines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 display lines (a, grp header, z), got %d: %+v", len(lines), lines)
	}
	if lines[0].folder != "" || m.sessions[lines[0].sessionIdx].ID != "demo:a" {
		t.Fatalf("expected line 0 to be session a, got %+v", lines[0])
	}
	if lines[1].folder != "grp" {
		t.Fatalf("expected line 1 to be the grp folder header (Order 2, between a=1 and z=3), got %+v", lines[1])
	}
	if lines[2].folder != "" || m.sessions[lines[2].sessionIdx].ID != "demo:z" {
		t.Fatalf("expected line 2 to be session z, got %+v", lines[2])
	}
}

func TestModelProjectEmojiPrefersConfiguredOverFallback(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	m.cfg.Projects = map[string]config.Project{
		"demo": {Repo: "/tmp/demo", Emoji: "🎯"},
	}
	if got := m.projectEmoji("demo"); got != "🎯" {
		t.Fatalf("projectEmoji(%q) = %q, want configured 🎯", "demo", got)
	}
}

func TestModelProjectEmojiFallsBackDeterministically(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	m.cfg.Projects = map[string]config.Project{
		"demo": {Repo: "/tmp/demo"}, // no Emoji set
	}
	first := m.projectEmoji("demo")
	if first == "" {
		t.Fatalf("projectEmoji(%q) = empty, want a fallback glyph", "demo")
	}
	found := false
	for _, e := range projectEmojiPalette {
		if e == first {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("projectEmoji(%q) = %q, not in projectEmojiPalette", "demo", first)
	}
	if second := m.projectEmoji("demo"); second != first {
		t.Fatalf("projectEmoji(%q) not deterministic: %q then %q", "demo", first, second)
	}
}
