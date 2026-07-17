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

	moveSessionCalls []moveSessionCall
	moveSessionErr   error

	moveProjectCalls []moveProjectCall
	moveProjectErr   error

	createCalls []createCall
	createErr   error
	createHint  string

	deleteCalls []string
	deleteErr   error

	openCalls []string
	openErr   error
	openHint  string

	killCalls []string

	tagCalls []tagCall
	tagErr   error

	archiveCalls []archiveCall
	archiveErr   error

	addProjectCalls  []projectCall
	addProjectErr    error
	initProjectCalls []projectCall
	initProjectErr   error
	plainCalls       []projectCall
	plainErr         error

	removeProjectCalls []string
	removeProjectErr   error
}

type createCall struct{ project, name, agent, branch, ticket string }
type tagCall struct{ id, ticket, pr string }
type archiveCall struct {
	id       string
	archived bool
}
type projectCall struct {
	name string
	p    config.Project
}

type moveSessionCall struct {
	id    string
	delta int
}

type moveProjectCall struct {
	name  string
	delta int
}

func (f *fakeBackend) CreateSession(project, name, agent, existingBranch, ticket string) (session.Session, string, error) {
	f.createCalls = append(f.createCalls, createCall{project, name, agent, existingBranch, ticket})
	if f.createErr != nil {
		return session.Session{}, "", f.createErr
	}
	label := name
	if label == "" {
		label = existingBranch
	}
	s := session.Session{ID: session.MakeID(project, label), Project: project, Name: label, Agent: agent, Ticket: ticket}
	f.sessions = append(f.sessions, s)
	return s, f.createHint, nil
}
func (f *fakeBackend) OpenSession(id string) (string, error) {
	f.openCalls = append(f.openCalls, id)
	return f.openHint, f.openErr
}
func (f *fakeBackend) DeleteSession(id string) error {
	f.deleteCalls = append(f.deleteCalls, id)
	return f.deleteErr
}
func (f *fakeBackend) KillTmux(id string) error {
	f.killCalls = append(f.killCalls, id)
	return nil
}
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	f.tagCalls = append(f.tagCalls, tagCall{id, ticket, pr})
	if f.tagErr != nil {
		return session.Session{}, f.tagErr
	}
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Ticket, f.sessions[i].PR = ticket, pr
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Ticket: ticket, PR: pr}, nil
}
func (f *fakeBackend) SetSessionArchived(id string, archived bool) (session.Session, error) {
	f.archiveCalls = append(f.archiveCalls, archiveCall{id, archived})
	if f.archiveErr != nil {
		return session.Session{}, f.archiveErr
	}
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Archived = archived
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Archived: archived}, nil
}
func (f *fakeBackend) MoveSession(id string, delta int) error {
	f.moveSessionCalls = append(f.moveSessionCalls, moveSessionCall{id: id, delta: delta})
	return f.moveSessionErr
}
func (f *fakeBackend) MoveProject(name string, delta int) error {
	f.moveProjectCalls = append(f.moveProjectCalls, moveProjectCall{name: name, delta: delta})
	return f.moveProjectErr
}
func (f *fakeBackend) TmuxAliveAll() map[string]bool { return map[string]bool{} }
func (f *fakeBackend) Sessions() []session.Session   { return f.sessions }
func (f *fakeBackend) Projects() []string            { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) error {
	f.addProjectCalls = append(f.addProjectCalls, projectCall{name, p})
	return f.addProjectErr
}
func (f *fakeBackend) InitProjectAndAdd(name string, p config.Project) error {
	f.initProjectCalls = append(f.initProjectCalls, projectCall{name, p})
	return f.initProjectErr
}
func (f *fakeBackend) AddPlainProject(name string, p config.Project) error {
	f.plainCalls = append(f.plainCalls, projectCall{name, p})
	return f.plainErr
}
func (f *fakeBackend) RemoveProject(name string) error {
	f.removeProjectCalls = append(f.removeProjectCalls, name)
	return f.removeProjectErr
}

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
