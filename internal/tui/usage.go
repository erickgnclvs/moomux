package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/erickgnclvs/moomux/internal/usage"
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
func (m *Model) agentUsageCandidates(u *usage.Usage, agent string) []string {
	if u == nil {
		return nil
	}
	if u.Status == "signed_out" {
		return []string{muteStyle.Render(agent + " signed out")}
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
		if u.Tokens == nil {
			return nil
		}
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
	if len(groups) == 0 {
		return []string{tokenLabel(u.Tokens) + suffix}
	}
	// "used" is said once per line, not per window, and is the first detail
	// to go when space is short.
	return []string{
		strings.Join(full, "  ") + muteStyle.Render(" used") + suffix,
		strings.Join(full, "  ") + suffix,
		strings.Join(short, "  ") + suffix,
	}
}

func tokenLabel(t *usage.Tokens) string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("%dd local %s tokens", t.WindowDays, compactTokens(t.Input+t.Output+t.CacheRead+t.CacheWrite))
}
func compactTokens(n int64) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprint(n)
	}
}
func (m *Model) usageCandidates() []string {
	a := m.agentUsageCandidates(m.usage, "claude")
	b := m.agentUsageCandidates(m.codexUsage, "codex")
	if b == nil && (m.usage == nil || m.usage.Tokens == nil) {
		return a
	}
	var result []string
	// Preserve both agents as space permits; then retain quota without tokens.
	for _, detail := range []bool{true, false} {
		for i := 0; i < 3; i++ {
			var parts []string
			for _, entry := range []struct {
				name string
				u    *usage.Usage
				c    []string
			}{{"Claude", m.usage, a}, {"Codex", m.codexUsage, b}} {
				if len(entry.c) == 0 {
					continue
				}
				c := entry.c[min(i, len(entry.c)-1)]
				if detail && entry.u.Tokens != nil && (len(entry.u.Windows) > 0 || entry.u.Status == "signed_out") {
					c += "  " + muteStyle.Render(tokenLabel(entry.u.Tokens))
				}
				parts = append(parts, muteStyle.Render(entry.name+" ")+c)
			}
			if len(parts) > 0 {
				result = append(result, strings.Join(parts, "  ·  "))
			}
		}
	}

	// A compact headline and local total keeps both agents visible in narrower terminals.
	var compact, quotaOnly []string
	for _, entry := range []struct {
		name string
		u    *usage.Usage
	}{{"Claude", m.usage}, {"Codex", m.codexUsage}} {
		if entry.u == nil {
			continue
		}
		var headline []string
		for _, w := range entry.u.Windows {
			if w.Headline || w.Level == "warn" || w.Level == "critical" {
				style := infoFlashStyle
				if entry.u.Status == "stale" {
					style = muteStyle.Bold(true)
				} else if w.Level == "critical" {
					style = errorFlashStyle
				} else if w.Level == "warn" {
					style = warnStyle
				}
				headline = append(headline, muteStyle.Render(w.Name+" ")+style.Render(fmt.Sprintf("%d%%", w.Percent)))
			}
		}
		if entry.u.Status == "signed_out" {
			headline = append(headline, "signed out")
		}
		if entry.u.Status == "stale" {
			headline = append(headline, "stale")
		}
		base := entry.name
		if len(headline) > 0 {
			base += " " + strings.Join(headline, " ")
		}
		if entry.u.Tokens != nil {
			compact = append(compact, base+" "+tokenLabel(entry.u.Tokens))
		} else if len(headline) > 0 {
			compact = append(compact, base)
		}
		if len(headline) > 0 {
			quotaOnly = append(quotaOnly, base)
		}
	}
	if len(compact) > 0 {
		result = append([]string{strings.Join(compact, " · ")}, result...)
	}
	if len(quotaOnly) > 0 {
		result = append(result, strings.Join(quotaOnly, " · "))
	}
	var minimal []string
	for _, entry := range []struct {
		name string
		u    *usage.Usage
	}{{"Claude", m.usage}, {"Codex", m.codexUsage}} {
		if entry.u == nil {
			continue
		}
		var values []string
		for _, w := range entry.u.Windows {
			if w.Headline {
				label := w.Name
				if w.Kind == "weekly_all" || label == "Week" {
					label = "7d"
				}
				values = append(values, fmt.Sprintf("%s%d%%", label, w.Percent))
			}
		}
		if entry.u.Status == "signed_out" {
			values = append(values, "signed out")
		}
		if entry.u.Status == "stale" {
			values = append(values, "stale")
		}
		if len(values) > 0 {
			minimal = append(minimal, entry.name+" "+strings.Join(values, " "))
		}
	}
	if len(minimal) > 0 {
		result = append(result, strings.Join(minimal, " "))
	}
	return result
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
