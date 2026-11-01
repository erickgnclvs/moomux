package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/sessionview"
)

// TestNewFormDefaultsToLastAgent: the new-session form starts on the agent
// the last session was created with — global, so switching project keeps it —
// and on "default" model and thinking, which are never remembered. Creating a
// session refreshes the default from the core's config.
func TestNewFormDefaultsToLastAgent(t *testing.T) {
	cfg := &config.Config{LastAgent: "codex", Projects: map[string]config.Project{
		"alpha": {Repo: "/tmp/alpha"}, "beta": {Repo: "/tmp/beta"},
	}}
	be := &fakeBackend{cfg: *cfg}
	m := New(cfg, be, testAgentOptions, make(chan sessionview.Snapshot), func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList

	m.openNewSessionForm()
	if got := m.agentNames()[m.newFormAgentIdx]; got != "codex" {
		t.Fatalf("agent = %q, want the last-used codex", got)
	}
	if m.newFormModelIdx != 0 || m.newFormThinkingIdx != 0 || m.newFormModelInput.Value() != "" {
		t.Fatalf("model/thinking = %d/%d/%q, want default", m.newFormModelIdx, m.newFormThinkingIdx, m.newFormModelInput.Value())
	}

	m.newFormFocus = newFormProjFocus
	press(m, tea.KeyRight)
	if got := m.agentNames()[m.newFormAgentIdx]; got != "codex" {
		t.Fatalf("after project change agent = %q, want codex", got)
	}

	be.cfg.LastAgent = "opencode"
	m.Update(SessionCreatedMsg{Session: session.Session{ID: "alpha:x", Project: "alpha", Name: "x"}, Cfg: m.cfgSnapshotOnSuccess(nil)})
	m.openNewSessionForm()
	if got := m.agentNames()[m.newFormAgentIdx]; got != "opencode" {
		t.Fatalf("after create agent = %q, want opencode", got)
	}
}
