package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/erickgnclvs/moomux/internal/sessionview"
	"github.com/erickgnclvs/moomux/internal/usage"
)

func TestCountdown(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	for in, want := range map[string]string{
		at(41 * time.Minute):             "41m",
		at(2*time.Hour + 5*time.Minute):  "2h05m",
		at(6*24*time.Hour + 3*time.Hour): "6d",
		at(-time.Minute):                 "",
		at(30 * time.Second):             "<1m",
		"":                               "",
		"not a time":                     "",
	} {
		if got := countdown(in, now); got != want {
			t.Errorf("countdown(%q) = %q, want %q", in, got, want)
		}
	}
}

func usageModel(u *usage.Usage, width int) *Model {
	m := layoutTestModel(1)
	m.width = width
	m.usage = u
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	return m
}

func plainUsage(m *Model) []string {
	var out []string
	for _, c := range m.usageCandidates() {
		out = append(out, ansi.Strip(c))
	}
	return out
}

func TestUsageCandidates(t *testing.T) {
	windows := []usage.Window{
		{Name: "5h", Percent: 41, ResetsAt: "2026-10-03T12:41:00Z", Level: "ok", Headline: true},
		{Name: "Week", Percent: 83, Level: "warn", Headline: true},
		{Name: "Fable", Percent: 96, Level: "critical"},
		{Name: "Opus", Percent: 12, Level: "ok"}, // not headline, not warn: hidden
	}
	got := plainUsage(usageModel(&usage.Usage{Status: "ok", Windows: windows}, 100))
	want := []string{"5h 41% ↻41m  Week 83%  Fable 96% used", "5h 41% ↻41m  Week 83%  Fable 96%", "5h 41%  Week 83%  Fable 96%"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("ok = %q, want %q", got, want)
	}

	// The week and its carve-out, stamped a second apart, share one countdown
	// after the last of them; a window resetting at another time keeps its own.
	grouped := []usage.Window{
		{Name: "Week", Percent: 83, ResetsAt: "2026-10-09T13:59:59Z", Level: "warn", Headline: true},
		{Name: "5h", Percent: 41, ResetsAt: "2026-10-03T14:41:00Z", Level: "ok", Headline: true},
		{Name: "Fable", Percent: 96, ResetsAt: "2026-10-09T14:00:00Z", Level: "critical"},
	}
	got = plainUsage(usageModel(&usage.Usage{Status: "ok", Windows: grouped}, 100))
	if want := "Week 83%  Fable 96% ↻6d  5h 41% ↻2h41m used"; got[0] != want {
		t.Errorf("grouped = %q, want %q", got[0], want)
	}

	got = plainUsage(usageModel(&usage.Usage{Status: "stale", Windows: windows}, 100))
	if len(got) != 3 || !strings.HasSuffix(got[0], " · stale") || !strings.HasSuffix(got[2], " · stale") {
		t.Errorf("stale = %q, want all marked stale", got)
	}

	if got := plainUsage(usageModel(&usage.Usage{Status: "signed_out", Windows: []usage.Window{}}, 100)); len(got) != 1 || got[0] != "claude signed out" {
		t.Errorf("signed_out = %q", got)
	}
	if got := usageModel(nil, 100).usageCandidates(); got != nil {
		t.Errorf("no usage = %q, want nothing", got)
	}
	// Only hidden windows: nothing to draw, rather than an empty slot.
	if got := usageModel(&usage.Usage{Status: "ok", Windows: windows[3:]}, 100).usageCandidates(); got != nil {
		t.Errorf("only hidden windows = %q, want nothing", got)
	}
}

// On a narrow terminal the quota rides the footer, gives up its countdowns
// before its numbers, and outranks the version; on a wide one it's in the
// header and the footer is untouched.
func TestFooterUsageFallsBack(t *testing.T) {
	u := &usage.Usage{Status: "ok", Windows: []usage.Window{
		{Name: "5h", Percent: 41, ResetsAt: "2026-10-03T14:41:00Z", Level: "ok", Headline: true},
		{Name: "Week", Percent: 83, Level: "warn", Headline: true},
	}}
	row := func(width int) string {
		m := usageModel(u, width)
		m.Version = "0.5.3"
		return ansi.Strip(m.hintRowWithVersion("? help", width-2))
	}
	for width, want := range map[int][]string{
		60: {"5h 41% ↻2h41m  Week 83%", "v0.5.3"},
		36: {"5h 41% ↻2h41m  Week 83%"}, // version dropped first
		30: {"5h 41%  Week 83%"},        // then the countdown
		20: {"? help", "v0.5.3"},        // then the quota, before clipping
	} {
		got := row(width)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("width %d: footer %q, want %q", width, got, w)
			}
		}
		if width < 40 && width > 20 && strings.Contains(got, "v0.5.3") {
			t.Errorf("width %d: footer %q kept the version over the quota", width, got)
		}
		if lipgloss.Width(got) > width-2 {
			t.Errorf("width %d: footer %q overflows", width, got)
		}
	}
	if got := row(100); strings.Contains(got, "41%") {
		t.Errorf("wide footer %q should leave the quota to the header", got)
	}
}

// An available update outranks the quota's countdowns on a narrow footer,
// but never its numbers.
func TestFooterUpdateNoticeOutranksCountdowns(t *testing.T) {
	u := &usage.Usage{Status: "ok", Windows: []usage.Window{
		{Name: "5h", Percent: 41, ResetsAt: "2026-10-03T14:41:00Z", Level: "ok", Headline: true},
		{Name: "Week", Percent: 83, ResetsAt: "2026-10-09T15:00:00Z", Level: "warn", Headline: true},
	}}
	m := usageModel(u, 50)
	m.Version, m.UpdateVersion = "0.5.3", "0.5.4"
	got := ansi.Strip(m.hintRowWithVersion("? help", 48))
	if !strings.Contains(got, "5h 41%  Week 83%") || !strings.Contains(got, "→ v0.5.4") {
		t.Errorf("footer %q, want the quota without countdowns and the update arrow", got)
	}
}

// The error-only snapshot a dropped connection produces (nil Views) must not
// wipe the quota: like everything else, the TUI keeps the last real answer.
func TestUsageSurvivesErrorOnlySnapshot(t *testing.T) {
	m := layoutTestModel(1)
	want := &usage.Usage{Status: "ok", Windows: []usage.Window{}}
	m.Update(StatusTickMsg{Snap: sessionview.Snapshot{Views: map[string]sessionview.View{}, Usage: want}})
	m.Update(StatusTickMsg{Snap: sessionview.Snapshot{Err: "connection lost"}})
	if m.usage != want {
		t.Errorf("usage after error-only snapshot = %+v, want the last real one", m.usage)
	}
	m.Update(StatusTickMsg{Snap: sessionview.Snapshot{Views: map[string]sessionview.View{}}})
	if m.usage != nil {
		t.Errorf("a real snapshot without usage should clear it, got %+v", m.usage)
	}
}
