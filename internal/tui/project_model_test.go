package tui

import (
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/sessionview"
)

func projectModelTestModel(p config.Project) *Model {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": p}}
	be := &fakeBackend{cfg: *cfg}
	m := New(cfg, be, testAgentOptions, make(chan sessionview.Snapshot), func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList
	return m
}

// TestNewFormPreselectsProjectModel: a project's default model must land in
// the new-session form's model selector (free-text field for opencode, which
// has no fixed list), and an unset default must leave it on "default".
func TestNewFormPreselectsProjectModel(t *testing.T) {
	t.Run("selector", func(t *testing.T) {
		m := projectModelTestModel(config.Project{Repo: "/tmp/demo", Agent: "claude", Model: "opus"})
		m.openNewSessionForm()
		if got := m.modelNamesFor("claude")[m.newFormModelIdx]; got != "opus" {
			t.Fatalf("model = %q, want opus", got)
		}
	})
	t.Run("opencode free text", func(t *testing.T) {
		m := projectModelTestModel(config.Project{Repo: "/tmp/demo", Agent: "opencode", Model: "anthropic/claude-opus-4"})
		m.openNewSessionForm()
		if got := m.newFormModelInput.Value(); got != "anthropic/claude-opus-4" {
			t.Fatalf("model input = %q", got)
		}
	})
	t.Run("unset", func(t *testing.T) {
		m := projectModelTestModel(config.Project{Repo: "/tmp/demo", Agent: "claude"})
		m.openNewSessionForm()
		if m.newFormModelIdx != 0 {
			t.Fatalf("modelIdx = %d, want 0 (default)", m.newFormModelIdx)
		}
	})
}

// TestEditProjectFormKeepsUnlistedModel: a model that isn't one of the
// agent's listed choices — opencode's free-text model, a hand-edited config
// value, or one stored while the agent is "ask each time" — must survive an
// edit that touches nothing else, instead of being silently reset.
func TestEditProjectFormKeepsUnlistedModel(t *testing.T) {
	for _, p := range []config.Project{
		{Repo: "/tmp/demo", Agent: "claude", Model: "opusplan"},
		{Repo: "/tmp/demo", Agent: "opencode", Model: "anthropic/claude-opus-4"},
		{Repo: "/tmp/demo", PromptAgent: true, Model: "opus"},
	} {
		m := projectModelTestModel(p)
		m.projForm = m.editProjectForm("demo", p)
		if got := m.projFormModel(); got != p.Model {
			t.Errorf("agent %q: projFormModel() = %q, want %q", p.AgentName(), got, p.Model)
		}
	}
}

// TestEditProjectFormRoundTripsModel: the edit-project form must show the
// stored model and hand it back unchanged, and switching agent must drop it
// rather than carry an index into the other agent's list.
func TestEditProjectFormRoundTripsModel(t *testing.T) {
	p := config.Project{Repo: "/tmp/demo", Agent: "claude", Model: "opus"}
	m := projectModelTestModel(p)
	m.projForm = m.editProjectForm("demo", p)
	if got := m.projFormModel(); got != "opus" {
		t.Fatalf("projFormModel() = %q, want opus", got)
	}
	m.projForm.focus = projFormInputCount + 1
	m.adjustProjFormField(1)
	if got := m.projFormModel(); got != "" {
		t.Fatalf("after agent change projFormModel() = %q, want empty", got)
	}
	m.projForm.focus = projFormInputCount + 2
	m.adjustProjFormField(1)
	agent := m.agentNames()[m.projForm.agentIdx]
	if got, want := m.projFormModel(), m.projectModelChoices(m.projForm.agentIdx)[1]; got != want {
		t.Fatalf("after model cycle projFormModel() = %q, want %q (agent %s)", got, want, agent)
	}
}
