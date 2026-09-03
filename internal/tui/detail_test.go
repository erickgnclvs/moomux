package tui

import (
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// TestDetailTruncatesLongTicketURLButKeepsItClickable asserts a ticket/PR
// URL too long for the detail panel's value column is truncated on screen,
// but the click target recorded for that row still carries the full URL —
// so tapping the truncated text still opens/copies the real link.
func TestDetailTruncatesLongTicketURLButKeepsItClickable(t *testing.T) {
	longURL := "https://tracker.example.com/projects/some-very-long-project-name/issues/TICK-123456"
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one", Ticket: longURL},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24

	frame, hits := m.renderDetail(80-2, 24-2)
	joined := strings.Join(strings.Fields(frame), "")
	if strings.Contains(joined, strings.ReplaceAll(longURL, " ", "")) {
		t.Errorf("expected the long ticket URL to be truncated on screen, but it appeared unbroken; frame:\n%s", frame)
	}

	var found bool
	for _, h := range hits {
		if h.url == longURL {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a link hit for the full ticket URL %q, got hits: %+v", longURL, hits)
	}
}

func TestDetailLinkHitsSurviveWrappedRows(t *testing.T) {
	// A long value that wraps in the rendered panel must not shift the
	// hitboxes of the rows below it.
	m := layoutTestModel(1)
	m.sessions[0].WorktreePath = "/tmp/wt"
	m.sessions[0].Agent = "an-agent-name-far-longer-than-any-narrow-detail-pane-can-hold-on-one-line"
	m.sessions[0].Ticket = "https://t/1"

	width := 30
	rendered, hits := m.renderDetail(width, 24)
	var ticketHit *linkHit
	for i := range hits {
		if hits[i].url == "https://t/1" {
			ticketHit = &hits[i]
		}
	}
	if ticketHit == nil {
		t.Fatalf("no ticket hit found, hits = %+v", hits)
	}
	lines := strings.Split(rendered, "\n")
	ticketLine := -1
	for i, l := range lines {
		if strings.Contains(l, "ticket") {
			ticketLine = i
			break
		}
	}
	if ticketLine == -1 {
		t.Fatalf("no ticket row rendered:\n%s", rendered)
	}
	if ticketHit.line != ticketLine {
		t.Fatalf("hit line = %d, rendered ticket row = %d\n%s", ticketHit.line, ticketLine, rendered)
	}
}

// TestPRStatusLabel guards the label mapping: merged/closed wins outright
// (mergeable/CI stop being meaningful once a PR is done), otherwise
// conflicts and CI state are reported together.
func TestPRStatusLabel(t *testing.T) {
	cases := []struct {
		name string
		info prstatus.Info
		want string
	}{
		{"merged", prstatus.Info{State: "MERGED", Mergeable: "CONFLICTING", CI: "FAILING"}, "merged"},
		{"closed", prstatus.Info{State: "CLOSED"}, "closed"},
		{"open, no checks", prstatus.Info{State: "OPEN", Mergeable: "MERGEABLE", CI: "NONE"}, "open"},
		{"open, passing", prstatus.Info{State: "OPEN", Mergeable: "MERGEABLE", CI: "PASSING"}, "open, CI passing"},
		{"open, conflicting", prstatus.Info{State: "OPEN", Mergeable: "CONFLICTING", CI: "NONE"}, "open, conflicts"},
		{"open, conflicting and failing", prstatus.Info{State: "OPEN", Mergeable: "CONFLICTING", CI: "FAILING"}, "open, conflicts, CI failing"},
		{"open, pending", prstatus.Info{State: "OPEN", Mergeable: "MERGEABLE", CI: "PENDING"}, "open, CI running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prStatusLabel(tc.info); got != tc.want {
				t.Fatalf("prStatusLabel(%+v) = %q, want %q", tc.info, got, tc.want)
			}
		})
	}
}

// TestDetailShowsPRStatusRowOnlyWhenCached guards the detail panel's wiring:
// the "pr status" row only appears once a status has actually resolved into
// m.prStatus, not merely because the session has a PR attached — otherwise
// a session that just gained a PR (before its first gh pr view resolves)
// would show a misleading or empty status.
func TestDetailShowsPRStatusRowOnlyWhenCached(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one", PR: "https://github.com/example/repo/pull/1"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24

	frame, _ := m.renderDetail(80-2, 24-2)
	if strings.Contains(frame, "pr status") {
		t.Fatalf("expected no pr status row before a status resolves:\n%s", frame)
	}

	m.prStatus["demo:one"] = prStatusInfo{ok: true, info: prstatus.Info{State: "OPEN", Mergeable: "CONFLICTING", CI: "FAILING"}}
	frame, _ = m.renderDetail(80-2, 24-2)
	if !strings.Contains(frame, "conflicts") || !strings.Contains(frame, "CI failing") {
		t.Fatalf("expected the cached pr status to render:\n%s", frame)
	}
}

// TestCompactDetailTrimsFieldsAndShortensPR guards CompactDetail's whole
// point: with it on, low-signal fields (project/agent/ticket/worktree/
// created) disappear, the cowsay art shrinks to a one-line quip and face
// instead of vanishing outright, and the PR link shrinks to just its number
// — while pr status (merged/CI state) stays, since that's the whole reason
// to keep looking at a tagged session. Without CompactDetail, none of that
// trimming should happen.
func TestCompactDetailTrimsFieldsAndShortensPR(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{
			ID:           "demo:one",
			Project:      "demo",
			Name:         "one",
			Agent:        "claude",
			Ticket:       "https://tracker.example.com/TICK-1",
			PR:           "https://github.com/example/repo/pull/5478",
			WorktreePath: "/tmp/demo/one",
		},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24

	frame, _ := m.renderDetail(80-2, 24-2)
	for _, want := range []string{"project", "agent", "ticket", "worktree", "created", "^__^", "||----w"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("expected %q in the default (non-compact) frame\n%s", want, frame)
		}
	}
	if !strings.Contains(frame, "https://github.com/example/repo/pull/5478") {
		t.Fatalf("expected the full PR URL in the non-compact frame:\n%s", frame)
	}

	// pr status only ever shows once cached (TestDetailShowsPRStatusRowOnlyWhenCached
	// guards that generally); set it here so this test can confirm compact
	// mode keeps that row instead of accidentally guarding it on !compact.
	m.prStatus["demo:one"] = prStatusInfo{ok: true, info: prstatus.Info{State: "MERGED"}}

	cfg.CompactDetail = true
	frame, hits := m.renderDetail(80-2, 24-2)
	for _, unwanted := range []string{"project", "agent", "ticket", "worktree", "created", "||----w"} {
		if strings.Contains(frame, unwanted) {
			t.Fatalf("expected compact detail to drop %q:\n%s", unwanted, frame)
		}
	}
	if !strings.Contains(frame, "#5478") {
		t.Fatalf("expected compact detail to show the PR as #5478:\n%s", frame)
	}
	if strings.Contains(frame, "https://github.com/example/repo/pull/5478") {
		t.Fatalf("expected compact detail to hide the full PR URL:\n%s", frame)
	}
	if !strings.Contains(frame, "pr status") || !strings.Contains(frame, "merged") {
		t.Fatalf("expected compact detail to still show the cached pr status:\n%s", frame)
	}
	var found bool
	for _, h := range hits {
		if h.url == "https://github.com/example/repo/pull/5478" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the #5478 label to still be clickable to the full PR URL, hits: %+v", hits)
	}
	if !strings.Contains(frame, "^__^") || !strings.Contains(frame, "(--)") {
		t.Fatalf("expected compact detail to still show the small header-style cow (ears + eyes):\n%s", frame)
	}
}

// TestCompactDetailHidesCowOnNarrowLayout guards against showing the same
// small cow+quip twice: renderHeader's narrow-width branch already renders
// it in the header, so the compact detail panel below should omit it there
// (it still shows it at normal widths, and non-compact mode is unaffected
// either way — see TestCompactDetailTrimsFieldsAndShortensPR).
func TestCompactDetailHidesCowOnNarrowLayout(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}, CompactDetail: true}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})

	m.width, m.height = narrowWidthBreak, 24
	if frame, _ := m.renderDetail(narrowWidthBreak-2, 24-2); !strings.Contains(frame, "^__^") {
		t.Fatalf("expected the cow at a width right at the narrow break:\n%s", frame)
	}

	m.width = narrowWidthBreak - 1
	if frame, _ := m.renderDetail(m.width-2, 24-2); strings.Contains(frame, "^__^") {
		t.Fatalf("expected no cow in the detail panel below the narrow break (already shown in the header):\n%s", frame)
	}
}

func TestPRNumberLabel(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://github.com/example/repo/pull/5478", "#5478"},
		{"https://github.com/example/repo/pull/5478/", "#5478"},
		{"not-a-url", "#not-a-url"},
	}
	for _, tc := range cases {
		if got := prNumberLabel(tc.url); got != tc.want {
			t.Fatalf("prNumberLabel(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
