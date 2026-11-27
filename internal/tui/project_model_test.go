package tui

import (
	"errors"
	"slices"
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
	if m.newFormModelIdx != 0 || m.newFormThinkingIdx != 0 {
		t.Fatalf("model/thinking = %d/%d, want default", m.newFormModelIdx, m.newFormThinkingIdx)
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

func TestOpenCodeModelSelectorUsesProjectCatalog(t *testing.T) {
	cfg := &config.Config{LastAgent: "opencode", Projects: map[string]config.Project{"alpha": {}, "beta": {}}}
	m := New(cfg, &fakeBackend{}, testAgentOptions, make(chan sessionview.Snapshot), func() {})
	loadAlpha := m.openNewSessionForm()
	if loadAlpha == nil {
		t.Fatal("focusing the model row should discover models")
	}
	m.newFormFocus = newFormProjFocus
	_, loadBeta := press(m, tea.KeyRight)
	m.newFormFocus = newFormOpenCodeAgentFocus
	if loadBeta == nil {
		t.Fatal("second project should load its own models")
	}
	m.Update(loadBeta())
	m.Update(loadAlpha())
	press(m, tea.KeyTab)
	typeText(m, "unlisted/model")
	if _, got := m.newFormAgentModel(); got != "default" {
		t.Fatalf("typing changed model to %q", got)
	}
	press(m, tea.KeyRight)
	if _, got := m.newFormAgentModel(); got != "beta/test" {
		t.Fatalf("selected %q, want beta/test", got)
	}
}

func TestOpenCodeDiscoveryFailureLeavesDefaultAndAllowsRetry(t *testing.T) {
	cfg := &config.Config{LastAgent: "opencode", Projects: map[string]config.Project{"demo": {}}}
	m := New(cfg, &fakeBackend{}, testAgentOptions, make(chan sessionview.Snapshot), func() {})
	load := m.openNewSessionForm()
	m.newFormFocus = newFormOpenCodeAgentFocus
	if load == nil {
		t.Fatal("missing discovery command")
	}
	reply := load().(openCodeOptionsMsg)
	reply.options, reply.err = config.OpenCodeOptions{}, errors.New("opencode unavailable")
	m.Update(reply)
	if _, got := m.newFormAgentModel(); got != "default" {
		t.Fatalf("model = %q after failure", got)
	}
	_, retry := press(m, tea.KeyRight)
	if retry == nil {
		t.Fatal("missing retry command")
	}
	m.Update(retry())
	press(m, tea.KeyTab)
	press(m, tea.KeyRight)
	if _, got := m.newFormAgentModel(); got != "demo/test" {
		t.Fatalf("model = %q after retry", got)
	}
}

func TestOpenCodeShowsNativeEffort(t *testing.T) {
	cfg := &config.Config{LastAgent: "opencode", Projects: map[string]config.Project{"demo": {}}}
	be := &fakeBackend{}
	m := New(cfg, be, testAgentOptions, make(chan sessionview.Snapshot), func() {})
	load := m.openNewSessionForm()
	reply := load().(openCodeOptionsMsg)
	reply.options.Agents[1].Model = "custom/reviewer"
	reply.options.Models = append(reply.options.Models, "custom/reviewer", "custom/plain")
	reply.options.Variants["custom/reviewer"] = []string{"adaptive"}
	m.Update(reply)
	if got := m.newFormThinkingNames(); !slices.Equal(got, []string{"default", "low", "high"}) {
		t.Fatalf("default efforts = %v", got)
	}
	m.newFormFocus = newFormThinkingFocus
	press(m, tea.KeyRight)
	m.newFormFocus = newFormOpenCodeAgentFocus
	press(m, tea.KeyRight)
	if got := m.newFormThinkingNames(); !slices.Equal(got, []string{"default", "adaptive"}) || m.newFormThinkingIdx != 0 {
		t.Fatalf("reviewer efforts = %v, index = %d", got, m.newFormThinkingIdx)
	}
	m.newFormFocus = newFormModelFocus
	press(m, tea.KeyLeft)
	if got := m.newFormThinkingNames(); !slices.Equal(got, []string{"default"}) {
		t.Fatalf("plain model efforts = %v", got)
	}
	press(m, tea.KeyRight) // Back to the reviewer's default model.
	m.newFormFocus = newFormThinkingFocus
	press(m, tea.KeyRight)
	m.promptInput.SetValue("check this")
	_, submit := press(m, tea.KeyEnter)
	submit()
	req := be.createCalls[0]
	if req.OpenCodeAgent != "review" || req.Model != "custom/reviewer#adaptive" || req.Thinking != "" || req.Prompt != "check this" {
		t.Fatalf("request = %+v", req)
	}
}
