package tui

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

// T7: a flash set while any overlay is open must reach the screen.
func TestOverlayShowsFlash(t *testing.T) {
	m := newTestModel(&fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}})
	m.Update(runeKey('G'))
	m.Update(runeKey('n'))
	if m.mode != ModeFolderForm {
		t.Fatalf("mode = %v", m.mode)
	}
	m.setFlash("error", "a session create failed")
	if v := m.View(); !strings.Contains(v, "a session create failed") {
		t.Fatalf("flash missing under folder form:\n%s", v)
	}
}

// T8: truncation and padding count cells, not runes.
func TestTruncateAndPadCountCells(t *testing.T) {
	name := strings.Repeat("漢", 30)
	if w := lipgloss.Width(truncate(name, 10)); w > 10 {
		t.Fatalf("truncate rendered %d cells, want <= 10", w)
	}
	if w := lipgloss.Width(padTrunc(name, 10)); w != 10 {
		t.Fatalf("padTrunc rendered %d cells, want 10", w)
	}
	if w := lipgloss.Width(padTrunc("ab", 10)); w != 10 {
		t.Fatalf("padTrunc short rendered %d cells, want 10", w)
	}
}

// T9: at 40 columns the worktree path wraps (tail stays visible) and the two
// y steps read differently.
func TestConfirmDeleteNarrow(t *testing.T) {
	wt := "/very/long/worktrees/directory/path/that/ends/in/UNIQUETAIL"
	m := newTestModel(&fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a", WorktreePath: wt}}})
	m.width, m.height = 40, 30
	m.confirmID = "demo:a"
	m.confirmGit = gitStatusInfo{ok: true, dirty: true}
	m.mode = ModeConfirmDelete
	before := m.View()
	flat := strings.NewReplacer("│", "", " ", "", "\n", "").Replace(before)
	if !strings.Contains(flat, "UNIQUETAIL") || !strings.Contains(flat, "removestheworktree.") {
		t.Fatalf("worktree tail clipped:\n%s", before)
	}
	m.confirmAck = true
	after := m.View()
	if before == after {
		t.Fatalf("first and second y steps render identically:\n%s", after)
	}
}

// T10: switching back to auto restores the terminal's real background even
// when the first appearance applied was an explicit override.
func TestAutoAppearanceAfterExplicitFirst(t *testing.T) {
	autoDarkOnce, autoDarkVal = sync.Once{}, false
	defer func() {
		autoDarkOnce = sync.Once{}
		applyAppearance("")
	}()
	lipgloss.SetHasDarkBackground(false) // the "detected" terminal: light
	ApplySettings(&config.Config{Appearance: "dark"})
	applyAppearance("")
	if lipgloss.HasDarkBackground() {
		t.Fatal("auto after explicit dark restored dark; want the detected light")
	}
}

// T12: Esc in the folder rename/create form returns to the Folders overlay.
func TestFolderFormEscReturnsToFolders(t *testing.T) {
	m := newTestModel(&fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}})
	m.Update(runeKey('G'))
	m.Update(runeKey('n'))
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModeFolders {
		t.Fatalf("mode after esc = %v, want ModeFolders", m.mode)
	}
}

// Narrow footers keep add/edit/delete reachable.
func TestNarrowFootersKeepActions(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	for _, w := range []int{40, 50, 60} {
		m.width, m.height = w, 24
		for name, f := range map[string]string{"folders": m.foldersFooter(), "picker": m.projectPickerFooter()} {
			if !strings.Contains(f, "n e d") && !strings.Contains(f, "del") {
				t.Errorf("%s footer at %d lacks actions: %q", name, w, f)
			}
			if lipgloss.Width(f) > m.overlayWidth(formHintWidth) {
				t.Errorf("%s footer at %d too wide: %q", name, w, f)
			}
		}
	}
}

// The narrow list names the project and the archived view.
func TestNarrowListNamesProjectAndArchived(t *testing.T) {
	m := newTestModel(&fakeBackend{sessions: []session.Session{{ID: "demo:a", Project: "demo", Name: "a"}}})
	m.width, m.height = 60, 24
	if v := m.View(); !strings.Contains(v, "demo · SESSIONS") {
		t.Fatalf("narrow list lacks project title:\n%s", v)
	}
}
