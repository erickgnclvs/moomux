package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/sessionview"
)

// Hiding a model on the hidden-models screen saves it through the backend
// and drops it from the new-session picker; "default" can't be hidden.
func TestModelFilterHidesModelFromPicker(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	m.openSettings()
	m.openModelFilter()
	rows := m.modelFilterRows()
	if len(rows) == 0 || rows[0].id != "sonnet" || rows[0].label != "Sonnet" {
		t.Fatalf("rows = %+v, want claude's models first, with display names", rows)
	}
	if slices.ContainsFunc(rows, func(r modelFilterRow) bool { return r.id == "default" }) {
		t.Fatal("default is listed; it can't be hidden")
	}
	press(m, tea.KeyEnter) // hide sonnet
	if len(be.setModelExcludedCalls) != 1 || be.setModelExcludedCalls[0] != (setModelExcludedCall{"claude", "sonnet", true}) {
		t.Fatalf("SetModelExcluded calls = %+v", be.setModelExcludedCalls)
	}
	if got := m.modelNamesFor("claude"); !slices.Equal(got, []string{"default", "opus", "fable"}) {
		t.Fatalf("picker = %v, want sonnet gone", got)
	}
	if !strings.Contains(m.renderSettings(), "1 hidden") {
		t.Fatal("settings row doesn't count the hidden model")
	}
	press(m, tea.KeyEnter) // show it again
	if got := m.modelNamesFor("claude"); !slices.Contains(got, "sonnet") {
		t.Fatalf("picker = %v, want sonnet back", got)
	}
	press(m, tea.KeyEsc)
	if m.mode != ModeSettings {
		t.Fatalf("esc: mode = %v, want settings", m.mode)
	}
}

// A newer agent table from the stream replaces the startup one, and an open
// form keeps its model and thinking choices by value, not by index into
// lists that just changed.
func TestStreamedAgentsReplaceTableKeepingFormChoice(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	m.Update(keyRune("n"))
	m.newFormAgentIdx = slices.Index(m.agentNames(), "claude")
	m.newFormModelIdx = slices.Index(m.modelNamesFor("claude"), "opus")
	m.newFormThinkingIdx = slices.Index(m.newFormThinkingNames(), "xhigh")

	opts := slices.Clone(testAgentOptions)
	opts[0].Models = []string{"default", "haiku", "sonnet", "opus"} // a model arrived ahead of opus
	m.Update(StatusTickMsg{Snap: sessionview.Snapshot{Agents: opts}})

	if agent, model := m.newFormAgentModel(); agent != "claude" || model != "opus" {
		t.Fatalf("form = %s/%s, want claude/opus kept", agent, model)
	}
	if got := m.newFormThinkingNames()[m.newFormThinkingIdx]; got != "xhigh" {
		t.Fatalf("thinking = %q, want xhigh kept", got)
	}
	if !slices.Contains(m.modelNamesFor("claude"), "haiku") {
		t.Fatal("new model not offered")
	}
	// A snapshot without Agents (an older core) keeps the last table.
	m.Update(StatusTickMsg{Snap: sessionview.Snapshot{}})
	if !slices.Contains(m.modelNamesFor("claude"), "haiku") {
		t.Fatal("a snapshot with no Agents dropped the table")
	}
}

// Showing a model from another front end re-filters the list under an open
// form; the form must keep the model and level it had, not whatever now sits
// at its old index.
func TestStreamedExclusionChangeKeepsFormChoice(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be)
	m.cfg.SetModelExcluded("claude", "sonnet", true)
	m.Update(keyRune("n"))
	m.newFormAgentIdx = slices.Index(m.agentNames(), "claude")
	m.newFormModelIdx = slices.Index(m.newFormModelChoices("claude"), "opus")
	m.newFormThinkingIdx = slices.Index(m.newFormThinkingNames(), "max")

	cfg := *m.cfg
	cfg.SetModelExcluded("claude", "sonnet", false) // shown again, ahead of opus
	m.Update(cfgTick(cfg, time.Now()))

	if _, model := m.newFormAgentModel(); model != "opus" {
		t.Fatalf("form model = %q, want opus kept", model)
	}
	if got := m.newFormThinkingNames()[m.newFormThinkingIdx]; got != "max" {
		t.Fatalf("thinking = %q, want max kept", got)
	}
}
