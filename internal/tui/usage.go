package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// usageCandidates renders the core's Claude quota (sessionview.Snapshot.Usage)
// from most to least detailed (with a trailing "used", then without, then
// without countdowns), so a caller can take the first that fits instead of
// clipping it mid-word. Nil when there's nothing to draw — the core omits
// usage for everyone not running agent-usage.
//
// Everything here is served: which windows are headline, their level and
// display name. The TUI only filters (headline windows, plus any other one
// that has reached warn) and formats the countdown, which the core leaves to
// clients because it changes every minute. Same line as the Mac and iPhone
// apps' UsageLine.
func (m *Model) usageCandidates() []string {
	u := m.usage
	if u == nil {
		return nil
	}
	if u.Status == "signed_out" {
		return []string{muteStyle.Render("claude signed out")}
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	// Windows that reset together share one countdown, printed after the
	// group's last window. Grouped by the phrase, not the timestamp: the
	// server stamps the week and its carve-outs a second apart.
	type group struct {
		left    string
		windows []string
	}
	var groups []group
	for _, w := range u.Windows {
		if !w.Headline && w.Level == "ok" {
			continue
		}
		// The name is the label and the number the reading, so the number
		// carries the weight and the level's colour.
		style := infoFlashStyle
		switch {
		case u.Status == "stale":
			// Old numbers in warning colours would read as current.
			style = muteStyle.Bold(true)
		case w.Level == "critical":
			style = errorFlashStyle
		case w.Level == "warn":
			style = warnStyle
		}
		text := muteStyle.Render(w.Name+" ") + style.Render(fmt.Sprintf("%d%%", w.Percent))
		left := countdown(w.ResetsAt, now)
		i := slices.IndexFunc(groups, func(g group) bool { return g.left == left })
		if i < 0 {
			groups = append(groups, group{left: left})
			i = len(groups) - 1
		}
		groups[i].windows = append(groups[i].windows, text)
	}
	if len(groups) == 0 {
		return nil
	}
	var full, short []string
	for _, g := range groups {
		windows := strings.Join(g.windows, "  ")
		short = append(short, windows)
		if g.left != "" {
			windows += " " + muteStyle.Render("↻"+g.left)
		}
		full = append(full, windows)
	}
	suffix := ""
	if u.Status == "stale" {
		suffix = muteStyle.Render(" · stale")
	}
	// "used" is said once per line, not per window, and is the first detail
	// to go when space is short.
	return []string{
		strings.Join(full, "  ") + muteStyle.Render(" used") + suffix,
		strings.Join(full, "  ") + suffix,
		strings.Join(short, "  ") + suffix,
	}
}

// fitUsage is the most detailed usage candidate no wider than width, or "".
func (m *Model) fitUsage(width int) string {
	for _, c := range m.usageCandidates() {
		if lipgloss.Width(c) <= width {
			return c
		}
	}
	return ""
}

// countdown formats the time until an RFC 3339 reset as "<1m", "41m",
// "2h41m" or "6d", or "" when it's missing, unparseable or already past.
func countdown(resetsAt string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, resetsAt)
	if err != nil {
		return ""
	}
	d := t.Sub(now)
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}
