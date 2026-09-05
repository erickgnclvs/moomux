package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func slashKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}
}

func TestProjectPickerJumpsToSelectedProject(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // projects: alpha, beta
	m.activeProj = 0

	m.Update(slashKey())
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected ModeProjectPicker, got %v", m.mode)
	}
	if m.pickerCursor != 0 {
		t.Fatalf("expected pickerCursor to start on the active project (0), got %d", m.pickerCursor)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.pickerCursor != 1 {
		t.Fatalf("expected pickerCursor 1 after down, got %d", m.pickerCursor)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != ModeList {
		t.Fatalf("expected enter to return to ModeList, got %v", m.mode)
	}
	if m.activeProj != 1 {
		t.Fatalf("expected activeProj 1 (beta) after selecting, got %d", m.activeProj)
	}
}

func TestProjectPickerCancelLeavesActiveProjectUnchanged(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 0

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.mode != ModeList {
		t.Fatalf("expected esc to return to ModeList, got %v", m.mode)
	}
	if m.activeProj != 0 {
		t.Fatalf("expected activeProj unchanged at 0, got %d", m.activeProj)
	}
}

// TestProjectPickerReorderMovesNonActiveProjectAndKeepsActiveInPlace covers
// the picker's own reorder support: shift+up/K on a highlighted project that
// is NOT the active one must move only that project, follow it with
// pickerCursor, and leave the active project (and its tab) unchanged.
func TestProjectPickerReorderMovesNonActiveProjectAndKeepsActiveInPlace(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // projects: alpha, beta
	m.activeProj = 0                  // active = alpha

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // pickerCursor -> 1 (beta)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")}) // move beta up
	if cmd == nil {
		t.Fatalf("expected a command to dispatch MoveProject")
	}
	resultMsg := cmd() // runs the closure, which calls backend.MoveProject
	if len(be.moveProjectCalls) != 1 {
		t.Fatalf("expected 1 MoveProject call, got %d", len(be.moveProjectCalls))
	}
	if got := be.moveProjectCalls[0]; got.name != "beta" || got.delta != -1 {
		t.Fatalf("MoveProject called with %+v, want {beta -1}", got)
	}

	// Backend reorders "beta" to the front; simulate the persisted order.
	m.cfg.Order = []string{"beta", "alpha"}
	m.Update(resultMsg)

	if m.projects[0] != "beta" || m.projects[1] != "alpha" {
		t.Fatalf("expected projects [beta alpha] after reorder, got %v", m.projects)
	}
	if m.pickerCursor != 0 {
		t.Fatalf("expected pickerCursor to follow beta to index 0, got %d", m.pickerCursor)
	}
	if m.activeProj != 1 {
		t.Fatalf("expected active project to stay on alpha (now index 1), got %d", m.activeProj)
	}
}

// TestProjectPickerDeleteHighlightedProject covers triggering project
// removal from the picker: d on a highlighted, session-free project (not
// necessarily the active one) jumps to it and opens the same
// ModeConfirmDeleteProject dialog D uses from the main list — and, since the
// delete was triggered from the picker, lands back on the picker rather than
// the main session list once it completes.
func TestProjectPickerDeleteHighlightedProject(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // projects: alpha, beta
	m.activeProj = 0                  // active = alpha

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // pickerCursor -> 1 (beta)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})

	if m.mode != ModeConfirmDeleteProject {
		t.Fatalf("expected ModeConfirmDeleteProject, got %v", m.mode)
	}
	if m.activeProj != 1 {
		t.Fatalf("expected d to land on the highlighted project (1, beta) before confirming, got %d", m.activeProj)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	drainCmd(m, cmd)
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected mode back to ModeProjectPicker after a picker-initiated delete, got %v", m.mode)
	}
	if len(be.removeProjectCalls) != 1 || be.removeProjectCalls[0] != "beta" {
		t.Fatalf("removeProjectCalls = %v, want [beta]", be.removeProjectCalls)
	}
}

// TestProjectPickerRendersEmptyStateAfterDeletingLastProject covers deleting
// the only remaining project from inside the picker: the picker itself has
// no "no projects left" special case in updateProjectPicker (there's nothing
// left to highlight or act on), so it must fall through to the picker's own
// empty-state render rather than panicking on an out-of-range pickerCursor.
func TestProjectPickerRendersEmptyStateAfterDeletingLastProject(t *testing.T) {
	be := &fakeBackend{}
	m := newTestModel(be) // single project: demo

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if m.mode != ModeConfirmDeleteProject {
		t.Fatalf("expected ModeConfirmDeleteProject, got %v", m.mode)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatalf("expected a command to dispatch RemoveProject")
	}
	resultMsg := cmd()
	if len(be.removeProjectCalls) != 1 || be.removeProjectCalls[0] != "demo" {
		t.Fatalf("removeProjectCalls = %v, want [demo]", be.removeProjectCalls)
	}
	// Simulate what the real backend persists: "demo" gone from cfg.
	delete(m.cfg.Projects, "demo")
	m.Update(resultMsg)

	if m.mode != ModeProjectPicker {
		t.Fatalf("expected mode back to ModeProjectPicker, got %v", m.mode)
	}
	if len(m.projects) != 0 {
		t.Fatalf("expected no projects left, got %v", m.projects)
	}
	if view := m.View(); !strings.Contains(view, "no projects yet") {
		t.Fatalf("expected the picker's empty-state hint, got:\n%s", view)
	}
}

// TestProjectPickerDeleteCancelReturnsToPicker mirrors the completed-delete
// case above for the "n"/esc cancel path out of the confirm dialog.
func TestProjectPickerDeleteCancelReturnsToPicker(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 0

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.mode != ModeProjectPicker {
		t.Fatalf("expected cancel to return to ModeProjectPicker, got %v", m.mode)
	}
	if len(be.removeProjectCalls) != 0 {
		t.Fatalf("expected no RemoveProject call on cancel, got %v", be.removeProjectCalls)
	}
}

// TestDelProjectFromMainListStillReturnsToList is the main-list counterpart:
// D pressed there (not from the picker) must still land back on ModeList,
// not the picker, after the delete completes.
func TestDelProjectFromMainListStillReturnsToList(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 0

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if m.mode != ModeConfirmDeleteProject {
		t.Fatalf("expected ModeConfirmDeleteProject, got %v", m.mode)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	drainCmd(m, cmd)
	if m.mode != ModeList {
		t.Fatalf("expected mode back to ModeList after a main-list-initiated delete, got %v", m.mode)
	}
}

// TestProjectPickerDeleteBlockedByExistingSessions ensures the picker
// enforces the same "no sessions" guard as the main list's D, checked
// against the highlighted project rather than the active one.
func TestProjectPickerDeleteBlockedByExistingSessions(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 0 // active = alpha, which has no sessions
	be.sessions = append(be.sessions, session.Session{ID: "beta:x", Project: "beta", Name: "x"})

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // pickerCursor -> 1 (beta)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})

	if m.mode != ModeProjectPicker {
		t.Fatalf("expected mode to stay ModeProjectPicker when blocked, got %v", m.mode)
	}
	if m.flashKind != "error" {
		t.Fatalf("expected an error flash, got kind %q text %q", m.flashKind, m.flash)
	}
	if len(be.removeProjectCalls) != 0 {
		t.Fatalf("expected no RemoveProject call, got %v", be.removeProjectCalls)
	}
	// Regression: the blocked-delete flash used to be set but never rendered
	// — ModeProjectPicker renders via renderOverlay, not the base view's
	// footer, so the global flash row was invisible while the picker stayed
	// open. It must now surface inline in the picker's own footer.
	if view := m.View(); !strings.Contains(view, "delete them first") {
		t.Fatalf("expected the blocked-delete error visible in the picker view, got:\n%s", view)
	}
}

// TestProjectPickerEditHighlightedProject covers triggering project edit
// from the picker: e on a highlighted project (not necessarily the active
// one) jumps to it and opens the same ModeEditProject form E uses from the
// main list — and, since edit was triggered from the picker, lands back on
// the picker rather than the main session list once the save completes.
func TestProjectPickerEditHighlightedProject(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // projects: alpha, beta
	m.activeProj = 0                  // active = alpha

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // pickerCursor -> 1 (beta)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})

	if m.mode != ModeEditProject {
		t.Fatalf("expected ModeEditProject, got %v", m.mode)
	}
	if m.editProjectName != "beta" {
		t.Fatalf("expected e to land on the highlighted project (beta), got %q", m.editProjectName)
	}
	if m.activeProj != 1 {
		t.Fatalf("expected activeProj to follow the highlighted project, got %d", m.activeProj)
	}

	run(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(be.updateProjectCalls) != 1 || be.updateProjectCalls[0].name != "beta" {
		t.Fatalf("updateProjectCalls = %v, want one call for beta", be.updateProjectCalls)
	}
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected mode back to ModeProjectPicker after a picker-initiated edit, got %v", m.mode)
	}
}

// TestProjectPickerEditEntryBlockedWhileSaveInFlight covers a race
// CodeRabbit flagged: canceling out of an edit whose save is still pending
// leaves m.busy true (updateEditProject's busy-guard changes the mode but
// doesn't reset it — that's intentional, matching updateEditSession, since
// the real ProjectUpdatedMsg still needs to land and reset it). Without a
// busy check on the picker's own edit-entry, pressing e again right then
// would open a second edit form whose state the first (still in-flight)
// ProjectUpdatedMsg could then clobber.
func TestProjectPickerEditEntryBlockedWhileSaveInFlight(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // projects: alpha, beta
	m.activeProj = 0

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // pickerCursor -> 1 (beta)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.editProjectName != "beta" {
		t.Fatalf("expected editProjectName beta, got %q", m.editProjectName)
	}

	// Submit, but don't run the returned command yet — simulates the save
	// still being in flight.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy {
		t.Fatalf("expected a pending UpdateProject command and m.busy, got cmd=%v busy=%v", cmd, m.busy)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancels out while still busy
	if m.mode != ModeProjectPicker || !m.busy {
		t.Fatalf("expected ModeProjectPicker with busy still true, got mode=%v busy=%v", m.mode, m.busy)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // pickerCursor -> 0 (alpha)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected e to no-op while a save is still in flight, got mode=%v", m.mode)
	}
	if m.editProjectName != "beta" {
		t.Fatalf("expected editProjectName to stay beta (untouched), got %q", m.editProjectName)
	}
}

// TestProjectPickerEditCancelReturnsToPicker mirrors the completed-edit case
// above for the esc cancel path out of the edit form.
func TestProjectPickerEditCancelReturnsToPicker(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.activeProj = 0

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.mode != ModeProjectPicker {
		t.Fatalf("expected cancel to return to ModeProjectPicker, got %v", m.mode)
	}
	if len(be.updateProjectCalls) != 0 {
		t.Fatalf("expected no UpdateProject call on cancel, got %v", be.updateProjectCalls)
	}
}

// TestProjectPickerFooterFitsNarrowWidths guards against the footer
// overflowing the overlay box and getting hard-clipped mid-word on
// mobile-width terminals (narrowWidthBreak and below) — projectPickerFooter
// must always pick a preset that fits the available width.
func TestProjectPickerFooterFitsNarrowWidths(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	for _, width := range []int{200, 100, 72, 60, 50, 40} {
		m.width = width
		footer := m.projectPickerFooter()
		avail := m.overlayWidth(formHintWidth)
		if w := lipgloss.Width(footer); w > avail {
			t.Errorf("width=%d: footer %q is %d cells wide, want <= %d", width, footer, w, avail)
		}
	}
}

// TestProjectPickerFooterReachesFullTierOnWideTerminals guards against the
// full preset silently becoming unreachable (it happened once: adding "E
// edit" pushed it past formHintWidth's cap, so every width fell through to
// the medium preset and the reorder/select hints never showed at all).
func TestProjectPickerFooterReachesFullTierOnWideTerminals(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)
	m.width = 200 // overlayWidth caps at formHintWidth regardless, so this is as generous as it gets
	footer := m.projectPickerFooter()
	for _, want := range []string{"add", "reorder", "edit", "delete", "open", "cancel"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer %q missing %q — full preset not reached even at generous width", footer, want)
		}
	}
}

// TestProjectPickerShowsActiveAndArchivedCounts covers the detail column:
// every project with any sessions shows both counts (active/archived), even
// when one side is zero — a bare "1" without the archived side reads as an
// ambiguous single number, not clearly "1 active".
// TestProjectPickerLegendAlignsWithCounts guards against the legend and the
// row counts drifting apart: an earlier version built the legend with
// Width(rowWidth) and Padding(0,1) in the same lipgloss style, which sets
// the *total* rendered width (padding eats into the budget) rather than
// adding to a rowWidth-sized content box the way listRow/listRowSelected do
// for actual rows — so the legend came out 2 columns narrower than a row and
// its right edge landed short of the counts below it.
func TestProjectPickerLegendAlignsWithCounts(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "alpha:a", Project: "alpha", Name: "a"},
		{ID: "alpha:b", Project: "alpha", Name: "b", Archived: true},
	}}
	m := newMultiProjectTestModel(be) // projects: alpha, beta

	lines := strings.Split(m.renderProjectPicker(), "\n")
	var legendLine, countLine string
	for _, line := range lines {
		if strings.Contains(line, "active/archived") {
			legendLine = line
		}
		if strings.Contains(line, "1/1") {
			countLine = line
		}
	}
	if legendLine == "" || countLine == "" {
		t.Fatalf("expected both a legend line and a count line, got:\n%s", strings.Join(lines, "\n"))
	}
	if lipgloss.Width(legendLine) != lipgloss.Width(countLine) {
		t.Fatalf("legend width %d != row width %d — right edges don't align:\nlegend: %q\nrow:    %q",
			lipgloss.Width(legendLine), lipgloss.Width(countLine), legendLine, countLine)
	}
}

func TestProjectPickerShowsActiveAndArchivedCounts(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "alpha:a", Project: "alpha", Name: "a"},
		{ID: "alpha:b", Project: "alpha", Name: "b"},
		{ID: "alpha:c", Project: "alpha", Name: "c", Archived: true},
		{ID: "beta:a", Project: "beta", Name: "a"},
	}}
	m := newMultiProjectTestModel(be)
	m.Update(slashKey())

	view := m.renderProjectPicker()
	if !strings.Contains(view, "2/1") {
		t.Fatalf("expected alpha's row to show \"2/1\" (2 active, 1 archived), got:\n%s", view)
	}
	if !strings.Contains(view, "1/0") {
		t.Fatalf("expected beta's row to show \"1/0\" (1 active, 0 archived), got:\n%s", view)
	}
}

// TestProjectPickerAddProject covers triggering "add project" from the
// picker: n opens the add-project form (there's no main-list equivalent
// anymore — P/E were removed, so this is now the only way to add or edit a
// project past the very first, zero-projects startup flow). Submitting it
// returns to the picker, with the cursor following the newly added project.
func TestProjectPickerAddProject(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be) // projects: alpha, beta

	m.Update(slashKey())
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected ModeProjectPicker, got %v", m.mode)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.mode != ModeNewProject {
		t.Fatalf("expected ModeNewProject, got %v", m.mode)
	}

	m.projForm.inputs[0].SetValue("newproj")
	m.projForm.inputs[1].SetValue("/tmp/newproj")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a command to dispatch AddProject")
	}
	resultMsg := cmd() // runs the closure, which calls backend.AddProject
	if len(be.addProjectCalls) != 1 || be.addProjectCalls[0].name != "newproj" {
		t.Fatalf("addProjectCalls = %v, want one call for newproj", be.addProjectCalls)
	}
	// The real backend persists the new project into cfg as part of
	// AddProject; the fake only records the call, so simulate that here
	// before feeding the result back in (same pattern as the reorder tests).
	m.cfg.Projects["newproj"] = config.Project{Repo: "/tmp/newproj", BaseBranch: "main"}
	m.cfg.Order = append(m.cfg.Order, "newproj")
	m.Update(resultMsg)
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected mode back to ModeProjectPicker after a picker-initiated add, got %v", m.mode)
	}
	if m.pickerCursor >= len(m.projects) || m.projects[m.pickerCursor] != "newproj" {
		t.Fatalf("expected pickerCursor on newproj, got index %d of %v", m.pickerCursor, m.projects)
	}
}

// TestProjectPickerAddProjectCancelReturnsToPicker mirrors the edit/delete
// cancel-returns-to-origin behavior for the add-project form's esc path.
func TestProjectPickerAddProjectCancelReturnsToPicker(t *testing.T) {
	be := &fakeBackend{}
	m := newMultiProjectTestModel(be)

	m.Update(slashKey())
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.mode != ModeProjectPicker {
		t.Fatalf("expected cancel to return to ModeProjectPicker, got %v", m.mode)
	}
	if len(be.addProjectCalls) != 0 {
		t.Fatalf("expected no AddProject call on cancel, got %v", be.addProjectCalls)
	}
}

// TestProjectPickerOpensWithZeroProjects covers the picker's new role as the
// only way to add a project past the very first, zero-projects startup flow
// (P/E were removed from the main list): opening it with none yet must
// still work — landing on its own empty-state render — rather than being
// blocked the way it used to be when P was still there as a fallback.
func TestProjectPickerOpensWithZeroProjects(t *testing.T) {
	be := &fakeBackend{}
	cfg := &config.Config{Projects: map[string]config.Project{}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList // New() auto-opens ModeNewProject with zero projects

	m.Update(slashKey())
	if m.mode != ModeProjectPicker {
		t.Fatalf("expected ModeProjectPicker even with no projects, got %v", m.mode)
	}
	if view := m.View(); !strings.Contains(view, "no projects yet") {
		t.Fatalf("expected the picker's empty-state hint, got:\n%s", view)
	}

	// n still works to add the first one from right here.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.mode != ModeNewProject {
		t.Fatalf("expected n to open ModeNewProject even from an empty picker, got %v", m.mode)
	}
}
