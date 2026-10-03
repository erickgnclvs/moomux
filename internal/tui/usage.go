package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// usageCandidates renders the core's Claude quota (sessionview.Snapshot.Usage)
// from most to least detailed (labelled "used", then bare, then without
// countdowns), so a caller can take the first that fits instead of clipping
// it mid-word. Nil when there's nothing to draw — the
// core omits usage for everyone not running agent-usage.
//
// Everything here is served: which windows are headline, their level and
// display name. The TUI only filters (headline windows, plus any other one
// that has reached warn) and formats the countdown, which the core leaves to
// clients because it changes every minute.
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
	var full, short []string
	for _, w := range u.Windows {
		if !w.Headline && w.Level == "ok" {
			continue
		}
		style := muteStyle
		switch {
		case u.Status == "stale":
			// Old numbers in warning colours would read as current.
		case w.Level == "critical":
			style = errorFlashStyle
		case w.Level == "warn":
			style = warnStyle
		}
		pct := fmt.Sprintf("%s %d%%", w.Name, w.Percent)
		short = append(short, style.Render(pct))
		if left := countdown(w.ResetsAt, now); left != "" {
			pct += " " + muteStyle.Render("↻"+left)
		}
		full = append(full, style.Render(pct))
	}
	if len(full) == 0 {
		return nil
	}
	suffix := ""
	if u.Status == "stale" {
		suffix = muteStyle.Render(" · stale")
	}
	// "used" is said once per line, not per window, and is the first detail
	// to go when space is short.
	return []string{
		muteStyle.Render("used ") + strings.Join(full, "  ") + suffix,
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
