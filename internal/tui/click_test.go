package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

type fakeBackend struct {
	sessions []session.Session
}

func (f *fakeBackend) CreateSession(project, name, agent, existingBranch, ticket string) (session.Session, string, error) {
	return session.Session{}, "", nil
}
func (f *fakeBackend) OpenSession(id string) (string, error) { return "", nil }
func (f *fakeBackend) DeleteSession(id string) error         { return nil }
func (f *fakeBackend) KillTmux(id string) error              { return nil }
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) TmuxAliveAll() map[string]bool                         { return map[string]bool{} }
func (f *fakeBackend) Sessions() []session.Session                           { return f.sessions }
func (f *fakeBackend) Projects() []string                                    { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) error        { return nil }
func (f *fakeBackend) InitProjectAndAdd(name string, p config.Project) error { return nil }
func (f *fakeBackend) AddPlainProject(name string, p config.Project) error   { return nil }
func (f *fakeBackend) RemoveProject(name string) error                       { return nil }

// TestLinkHitsResolveClicks renders a full frame and asserts that clicking
// on the printed ticket/PR icon glyphs resolves to the session's URLs, and
// that clicking one column outside the icon range does not.
func TestLinkHitsResolveClicks(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one", Ticket: "https://ticket.example/1", PR: "https://pr.example/1"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, statusCh, func() {})
	m.width, m.height = 80, 24

	frame := m.View()
	lines := strings.Split(frame, "\n")

	findCol := func(icon string) (line, col int) {
		for li, l := range lines {
			if idx := strings.Index(l, icon); idx >= 0 {
				return li, lipgloss.Width(l[:idx])
			}
		}
		t.Fatalf("icon %q not found in rendered frame:\n%s", icon, frame)
		return -1, -1
	}

	ticketLine, ticketCol := findCol(iconTicket)
	prLine, prCol := findCol(iconPR)

	if got := m.linkAt(ticketCol, ticketLine); got != be.sessions[0].Ticket {
		t.Errorf("click on ticket icon at (%d,%d) = %q, want %q", ticketCol, ticketLine, got, be.sessions[0].Ticket)
	}
	if got := m.linkAt(prCol, prLine); got != be.sessions[0].PR {
		t.Errorf("click on pr icon at (%d,%d) = %q, want %q", prCol, prLine, got, be.sessions[0].PR)
	}
	if got := m.linkAt(ticketCol-1, ticketLine); got != "" {
		t.Errorf("click one column left of ticket icon resolved to %q, want empty", got)
	}
}
