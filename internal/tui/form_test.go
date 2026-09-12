package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestRenderFormHintClampsLongHintToFixedHeight guards the "every form's
// hint row is a fixed size" invariant formHintLines documents: a hint long
// enough to wrap past formHintLines must be clamped, not allowed to grow
// the overlay box taller than every other field's hint.
func TestRenderFormHintClampsLongHintToFixedHeight(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	long := strings.Repeat("word ", 200)
	rendered := m.renderFormHint(long)
	if got := lipgloss.Height(rendered); got != formHintLines {
		t.Fatalf("height = %d, want %d (long hint):\n%s", got, formHintLines, rendered)
	}
}

func TestRenderFormHintPadsShortHintToFixedHeight(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	rendered := m.renderFormHint("short hint")
	if got := lipgloss.Height(rendered); got != formHintLines {
		t.Fatalf("height = %d, want %d (short hint):\n%s", got, formHintLines, rendered)
	}
}

// TestRenderNewFormAgentSelectorFollowsFocus guards against the agent
// selector always rendering as focused regardless of m.newFormFocus, which
// made it highlight blue even while another field had focus.
func TestRenderNewFormAgentSelectorFollowsFocus(t *testing.T) {
	origProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(origProfile)

	m := newTestModel(&fakeBackend{})
	m.newFormAgentIdx = 0

	m.newFormFocus = newFormProjFocus
	unfocused := m.renderNewFormAgentSelector()
	want := renderSelector(m.agentNames(), 0, false, m.overlayWidth(formHintWidth)-lipgloss.Width("agent:  "))
	if unfocused != want {
		t.Fatalf("unfocused render = %q, want %q", unfocused, want)
	}

	m.newFormFocus = newFormAgentFocus
	focused := m.renderNewFormAgentSelector()
	if focused == unfocused {
		t.Fatalf("agent selector rendered identically whether focused or not: %q", focused)
	}
}

// TestRenderSelectorOutOfRangeSelection guards against the compact-fallback
// path indexing choices[selected] unguarded: on very narrow terminals
// available goes negative, so the fallback fires even for an empty slice.
func TestRenderSelectorOutOfRangeSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		choices  []string
		selected int
	}{
		{"empty", nil, 0},
		{"negative", []string{"a"}, -1},
		{"past end", []string{"a"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderSelector(tc.choices, tc.selected, true, -5); got != "" {
				t.Fatalf("renderSelector = %q, want empty", got)
			}
		})
	}
}

// TestNewFormHintWarnsWhenAgyModelEatsThinking pins the second half of the
// agy effort rule: the thinking row's own hint only reaches someone standing
// on it, so picking the level first and a named model second would drop the
// level with nothing on screen saying so (buildAgentCmd logs a warning the
// user never sees).
func TestNewFormHintWarnsWhenAgyModelEatsThinking(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	agy := slices.Index(m.agentNames(), "antigravity")
	if agy < 0 {
		t.Fatal("antigravity missing from testAgentOptions")
	}
	m.newFormAgentIdx = agy
	m.newFormFocus = newFormModelFocus

	// default model: nothing is dropped, so the row keeps its normal hint.
	m.newFormModelIdx, m.newFormThinkingIdx = 0, 3
	if got := m.newFormFieldHint(); got != newFormFieldHints[newFormModelFocus] {
		t.Errorf("default model hint = %q, want the plain model hint", got)
	}
	// A named model with a level picked: warn.
	m.newFormModelIdx = 1
	if got := m.newFormFieldHint(); !strings.Contains(got, "ignored") {
		t.Errorf("named-model hint = %q, want it to say the thinking level is ignored", got)
	}
	// A named model with no level: nothing to warn about.
	m.newFormThinkingIdx = 0
	if got := m.newFormFieldHint(); got != newFormFieldHints[newFormModelFocus] {
		t.Errorf("no-thinking hint = %q, want the plain model hint", got)
	}
	// Another agent never gets the agy warning.
	m.newFormAgentIdx = slices.Index(m.agentNames(), "codex")
	m.newFormModelIdx, m.newFormThinkingIdx = 1, 3
	if got := m.newFormFieldHint(); got != newFormFieldHints[newFormModelFocus] {
		t.Errorf("codex hint = %q, want the plain model hint", got)
	}
}
