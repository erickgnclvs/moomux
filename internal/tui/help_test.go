package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/session"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestHelpToggle(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:a", Project: "demo", Name: "a"},
	}}
	m := newTestModel(be)

	if m.mode != ModeList {
		t.Fatalf("expected ModeList initially, got %v", m.mode)
	}

	// ? opens the help overlay.
	m.Update(runeKey('?'))
	if m.mode != ModeHelp {
		t.Fatalf("expected ModeHelp after '?', got %v", m.mode)
	}

	// The overlay lists commands and does not attempt to read the (empty)
	// clickable list behind it.
	view := m.View()
	if !strings.Contains(view, "moomux commands") {
		t.Fatalf("help view missing title, got:\n%s", view)
	}
	if m.linkHits != nil {
		t.Fatalf("expected link hits cleared behind overlay, got %v", m.linkHits)
	}

	// ? closes it again.
	m.Update(runeKey('?'))
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after second '?', got %v", m.mode)
	}
}

func TestHelpEscCloses(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}}
	m := newTestModel(be)

	m.Update(runeKey('?'))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got %v", m.mode)
	}
}
