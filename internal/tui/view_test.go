package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func layoutTestModel(sessionCount int) *Model {
	sessions := make([]session.Session, sessionCount)
	for i := range sessions {
		name := fmt.Sprintf("session-%02d", i)
		sessions[i] = session.Session{
			ID:           "demo:" + name,
			Project:      "demo",
			Name:         name,
			WorktreePath: "/tmp/" + name,
			TmuxSession:  name,
		}
	}
	return newTestModel(&fakeBackend{sessions: sessions})
}

func TestNarrowShortLayoutShowsOnlySessionList(t *testing.T) {
	m := layoutTestModel(3)
	m.width, m.height = 50, 24

	view := m.View()

	// Narrow terminals hide the "SESSIONS" title itself (to reclaim rows for
	// content), so check for a session's own name as the signal the list
	// pane rendered instead.
	if !strings.Contains(view, m.sessions[0].Name) {
		t.Fatalf("short narrow view does not contain session list:\n%s", view)
	}
	if strings.Contains(view, "DETAIL") {
		t.Fatalf("short narrow view unexpectedly contains detail pane:\n%s", view)
	}
	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("rendered height = %d, terminal height = %d", got, m.height)
	}
}

func TestHeaderNeverShowsWordmark(t *testing.T) {
	m := layoutTestModel(1)

	m.width = narrowWidthBreak - 1
	if header := m.renderHeader(); strings.Contains(header, "moomux") {
		t.Fatalf("narrow header unexpectedly contains wordmark:\n%s", header)
	}

	m.width = narrowWidthBreak
	if header := m.renderHeader(); strings.Contains(header, "moomux") {
		t.Fatalf("wide header unexpectedly contains wordmark:\n%s", header)
	}
}

func TestHeaderEyesShowNeedsInputForSelectedSession(t *testing.T) {
	m := layoutTestModel(1)
	m.cursor = 0
	s := m.sessions[0]
	m.tmuxAlive = map[string]bool{s.ID: true}
	m.states = map[string]watcher.State{s.WorktreePath: watcher.NeedsInput}

	header := m.renderHeader()

	if !strings.Contains(header, "!!") {
		t.Fatalf("header eyes did not show needs-input state:\n%s", header)
	}
}

// TestHeaderNeverShowsProjectName guards the header's project-tabs slot
// being retired entirely: the picker (/) is the way to see and jump to any
// project, and ModeMultiView's own panel titles already name each one, so
// naming the active project a second time up top was just noise. Covers
// both the narrow and wide layouts, since the slot used to differ between
// them (a single active tab either way, prior to this test).
func TestHeaderNeverShowsProjectName(t *testing.T) {
	for _, width := range []int{50, 100} {
		m := layoutTestModel(1)
		m.width = width
		m.projects = []string{
			"a-very-long-project-name-one",
			"a-very-long-project-name-two",
			"a-very-long-project-name-three",
		}
		m.activeProj = 1

		header := m.renderHeader()

		for _, name := range m.projects {
			if strings.Contains(header, name) {
				t.Fatalf("width=%d: header unexpectedly contains project name %q:\n%s", width, name, header)
			}
		}
		if got := lipgloss.Width(header); got > width {
			t.Fatalf("width=%d: header width = %d:\n%s", width, got, header)
		}
	}
}

func TestNarrowDetailKeepsEndOfWorktreePath(t *testing.T) {
	m := layoutTestModel(1)
	m.sessions[0].WorktreePath = "/Users/example/Development/moomux/feature-right-end"

	detail, _ := m.renderDetail(30, 20)

	if !strings.Contains(detail, "right-end") {
		t.Fatalf("detail does not show the useful end of the worktree path:\n%s", detail)
	}
	if !strings.Contains(detail, "…") {
		t.Fatalf("truncated worktree path has no leading ellipsis:\n%s", detail)
	}
}

func TestNarrowProjectEditKeepsEndOfRepoInput(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 24, 16
	project := m.cfg.Projects["demo"]
	project.Repo = "/Users/example/Development/moomux/repo-XYZ"
	m.cfg.Projects["demo"] = project
	m.editProjectName = "demo"
	m.projForm = m.editProjectForm("demo", project)
	m.mode = ModeEditProject
	m.resizeFormInputs()

	view := m.View()

	if !strings.Contains(view, "XYZ") {
		t.Fatalf("project editor does not show the end of the repo input:\n%s", view)
	}
	if !strings.Contains(view, "repo:") {
		t.Fatalf("project editor scrolled past the focused repo row:\n%s", view)
	}
	if !strings.Contains(view, "╮") || !strings.Contains(view, "╯") {
		t.Fatalf("narrow project editor clipped its right border:\n%s", view)
	}
}

func TestNarrowProjectEditShortRepoPreservesFrame(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 24, 12
	project := m.cfg.Projects["demo"]
	project.Repo = "/tmp/demo"
	m.editProjectName = "demo"
	m.projForm = m.editProjectForm("demo", project)
	m.mode = ModeEditProject
	m.resizeFormInputs()

	view := m.View()

	if !strings.Contains(view, "╮") || !strings.Contains(view, "╯") {
		t.Fatalf("short repo input clipped the narrow dialog frame:\n%s", view)
	}
}

func TestNarrowEditSessionShowsCompactSelectedAgent(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 24, 12
	m.mode = ModeEditSession
	m.sessionForm = newSessionForm("id", "demo", "session", 2, false) // opencode

	view := m.View()

	if !strings.Contains(view, "[opencode]") {
		t.Fatalf("narrow session editor does not show selected agent:\n%s", view)
	}
	if !strings.Contains(view, "╮") || !strings.Contains(view, "╯") {
		t.Fatalf("narrow session editor clipped its right border:\n%s", view)
	}
}

func TestNarrowTallLayoutShowsStackedDetail(t *testing.T) {
	m := layoutTestModel(3)
	m.width, m.height = 50, 40

	view := m.View()

	// Still narrow, so the "SESSIONS"/"DETAIL" titles stay hidden even in
	// the tall (stacked) layout — check for a session's own name and a
	// detail-pane field instead.
	if !strings.Contains(view, m.sessions[0].Name) || !strings.Contains(view, "status:") {
		t.Fatalf("tall narrow view should contain both panes:\n%s", view)
	}
	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("rendered height = %d, terminal height = %d", got, m.height)
	}
}

func TestNarrowLayoutRestoresDetailAfterResize(t *testing.T) {
	m := layoutTestModel(3)
	// "DETAIL" itself is hidden at this narrow width regardless of the
	// stacked/list-only split, so check for a detail-pane field instead.
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	if view := m.View(); strings.Contains(view, "status:") {
		t.Fatalf("detail visible before resize:\n%s", view)
	}

	m.Update(tea.WindowSizeMsg{Width: 50, Height: 40})
	if view := m.View(); !strings.Contains(view, "status:") {
		t.Fatalf("detail not restored after resize:\n%s", view)
	}
}

// TestNarrowStackedDetailGrowsBeforeListClips is the regression test for the
// stacked layout's size priority: detail is sized around what its own
// content actually needs (fields plus the closing cowsay art), and the list
// gets whatever's left, scrolling to keep the cursor visible if that isn't
// enough room to show every session — not the other way around, where a long
// session list used to claim priority and squeeze detail's cow off the
// bottom of the screen (see AGENTS.md's mobile-width guidance).
func TestNarrowStackedDetailGrowsBeforeListClips(t *testing.T) {
	few := layoutTestModel(2)
	few.width, few.height = 60, 40
	fewView := few.View()
	if !strings.Contains(fewView, "||     ||") {
		t.Fatalf("few sessions: detail's cow got clipped when it shouldn't have:\n%s", fewView)
	}

	many := layoutTestModel(25)
	many.width, many.height = 60, 40
	manyView := many.View()
	if !strings.Contains(manyView, "||     ||") {
		t.Fatalf("many sessions: detail's cow should never be clipped, regardless of list length:\n%s", manyView)
	}
	if !strings.Contains(manyView, "status:") {
		t.Fatalf("many sessions: detail should be fully visible:\n%s", manyView)
	}
	if strings.Contains(manyView, many.sessions[len(many.sessions)-1].Name) {
		t.Fatalf("many sessions: list should scroll rather than shrink detail to fit every session:\n%s", manyView)
	}
}

func TestNarrowShortLayoutKeepsSelectedSessionVisible(t *testing.T) {
	m := layoutTestModel(20)
	m.width, m.height = 50, 16
	m.cursor = len(m.sessions) - 1

	view := m.View()

	if !strings.Contains(view, m.sessions[m.cursor].Name) {
		t.Fatalf("selected session %q not visible:\n%s", m.sessions[m.cursor].Name, view)
	}
}

func TestViewNeverExceedsTerminalHeight(t *testing.T) {
	for _, tc := range []struct {
		width  int
		height int
	}{
		{width: 50, height: 8},
		{width: 50, height: 16},
		{width: 50, height: 24},
		{width: 50, height: 40},
		{width: 80, height: 12},
		{width: 80, height: 24},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			m := layoutTestModel(20)
			m.width, m.height = tc.width, tc.height

			if got := lipgloss.Height(m.View()); got > tc.height {
				t.Fatalf("rendered height = %d, terminal height = %d", got, tc.height)
			}
		})
	}
}

func TestWideLayoutKeepsRightBorder(t *testing.T) {
	m := layoutTestModel(10)
	m.width, m.height = 100, 24

	for _, raw := range strings.Split(m.View(), "\n") {
		line := ansi.Strip(raw)
		if !strings.ContainsRune(line, '│') && !strings.ContainsRune(line, '┌') && !strings.ContainsRune(line, '└') {
			continue
		}
		runes := []rune(line)
		last := runes[len(runes)-1]
		if last != '│' && last != '┐' && last != '┘' {
			t.Fatalf("line missing right border, want trailing │/┐/┘, got %q: %q", last, line)
		}
	}
}

func TestOverlaysStayWithinKeyboardSizedViewport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Model)
	}{
		{
			name: "new session",
			setup: func(m *Model) {
				m.mode = ModeNewForm
			},
		},
		{
			name: "new project",
			setup: func(m *Model) {
				m.mode = ModeNewProject
				m.projForm = newProjectForm()
			},
		},
		{
			name: "tag session",
			setup: func(m *Model) {
				m.mode = ModeTagForm
				m.tagForm = newTagForm("", "")
			},
		},
		{
			name: "help",
			setup: func(m *Model) {
				m.mode = ModeHelp
			},
		},
		{
			name: "edit session",
			setup: func(m *Model) {
				m.mode = ModeEditSession
				m.sessionForm = newSessionForm("id", "demo", "session", 1, false)
			},
		},
		{
			name: "edit project",
			setup: func(m *Model) {
				m.mode = ModeEditProject
				m.editProjectName = "demo"
				m.projForm = m.editProjectForm("demo", m.cfg.Projects["demo"])
			},
		},
	} {
		for _, size := range []struct {
			width  int
			height int
		}{
			{width: 24, height: 12},
			{width: 50, height: 12},
			{width: 50, height: 16},
		} {
			t.Run(fmt.Sprintf("%s/%dx%d", tc.name, size.width, size.height), func(t *testing.T) {
				m := layoutTestModel(1)
				m.width, m.height = size.width, size.height
				tc.setup(m)
				m.resizeFormInputs()

				view := m.View()
				if got := lipgloss.Width(view); got > m.width {
					t.Fatalf("rendered width = %d, terminal width = %d:\n%s", got, m.width, view)
				}
				if got := lipgloss.Height(view); got > m.height {
					t.Fatalf("rendered height = %d, terminal height = %d:\n%s", got, m.height, view)
				}
				if !strings.Contains(view, "esc") {
					t.Fatalf("essential escape control is not visible:\n%s", view)
				}
			})
		}
	}
}

func TestShortFormViewportKeepsFocusedInputVisible(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 50, 12
	m.mode = ModeNewForm
	m.nameInput.SetValue("unique-name")
	m.branchInput.SetValue("unique-branch")
	m.baseBranchInput.SetValue("unique-basebranch")
	m.promptInput.SetValue("unique-prompt")
	m.ticketInput.SetValue("unique-ticket")
	m.prInput.SetValue("unique-pr")
	m.resizeFormInputs()

	values := []string{"[demo]", "unique-name", "unique-branch", "unique-basebranch", "unique-prompt", "unique-ticket", "unique-pr", "[claude]", "[default]", "[default]", "[off]", "[off]", "[off]"}
	hints := []string{"which project", "worktree folder", "existing branch", "project's base branch", "agent's first task", "clickable ticket", "clickable PR", "←→ to choose", "omits the flag", "prepended to the first prompt", "permission prompts", "background", "starts right away"}
	for focus, value := range values {
		m.newFormBlurAll()
		m.newFormFocus = focus
		m.newFormFocusInput()

		view := m.View()
		if !strings.Contains(view, value) {
			t.Fatalf("focused input %d value %q is not visible:\n%s", focus, value, view)
		}
		if !strings.Contains(view, "esc cancel") {
			t.Fatalf("sticky form actions are not visible:\n%s", view)
		}
		if !strings.Contains(view, hints[focus]) {
			t.Fatalf("contextual hint %q is not visible:\n%s", hints[focus], view)
		}
	}
}

// TestFocusedOverlayLineCoversEveryNewFormField guards against
// focusedOverlayLine silently falling through to nameInput's line for a
// focus value it doesn't recognize — which happened for the prompt/PR/
// open-terminal fields after the new-session form was reordered, and made
// the overlay viewport scroll to the wrong spot (visible as the dialog
// jumping/"resizing") whenever one of those fields was focused.
func TestFocusedOverlayLineCoversEveryNewFormField(t *testing.T) {
	m := layoutTestModel(1)
	m.mode = ModeNewForm
	m.nameInput.SetValue("tok-name")
	m.branchInput.SetValue("tok-branch")
	m.baseBranchInput.SetValue("tok-basebranch")
	m.promptInput.SetValue("tok-firstprompt")
	m.ticketInput.SetValue("tok-ticket")
	m.prInput.SetValue("tok-prurl")
	m.newFormAgentIdx = 0
	content := m.compactOverlayContent(m.renderNewForm())

	cases := []struct {
		focus  int
		marker string
	}{
		{newFormProjFocus, "project:"},
		{1, "tok-name"},
		{2, "tok-branch"},
		{3, "tok-basebranch"},
		{4, "tok-firstprompt"},
		{5, "tok-ticket"},
		{6, "tok-prurl"},
		{newFormAgentFocus, "agent:"},
		{newFormModelFocus, "model:"},
		{newFormThinkingFocus, "thinking:"},
		{newFormDangerousFocus, "dangerous:"},
		{newFormOpenTerminalFocus, "open in background:"},
		{newFormAutoSubmitFocus, "auto-submit:"},
	}
	for _, tc := range cases {
		m.newFormFocus = tc.focus
		want := lineContaining(content, tc.marker)
		if got := m.focusedOverlayLine(content); got != want {
			t.Fatalf("focus %d: focusedOverlayLine = %d, want %d (line containing %q)", tc.focus, got, want, tc.marker)
		}
	}
}

func TestShortProjectFormKeepsBottomControlVisible(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 50, 12
	m.mode = ModeNewProject
	m.projForm = newProjectForm()
	m.projForm.focus = projFormInputCount + 1
	m.resizeFormInputs()

	view := m.View()

	if !strings.Contains(view, "[on]") {
		t.Fatalf("focused bottom control is not visible:\n%s", view)
	}
	if !strings.Contains(view, "esc cancel") {
		t.Fatalf("sticky project actions are not visible:\n%s", view)
	}
}

func TestManualFormScrollPersistsUntilFocusChanges(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 50, 12
	m.mode = ModeNewForm

	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	scrolled := m.overlayViewport.YOffset
	m.View()

	if scrolled == 0 {
		t.Fatal("form viewport did not scroll")
	}
	if got := m.overlayViewport.YOffset; got != scrolled {
		t.Fatalf("manual scroll was reset: before render=%d after=%d", scrolled, got)
	}
}

func TestConfirmDeleteOpensUnscrolled(t *testing.T) {
	// Scroll inside the help overlay, close it, then open the delete
	// confirmation: it must start at the top, showing what's being
	// deleted — not inherit the help overlay's scroll offset.
	m := layoutTestModel(1)
	m.width, m.height = 50, 12
	m.mode = ModeHelp

	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.overlayViewport.YOffset == 0 {
		t.Fatal("help viewport did not scroll; scenario not set up")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if m.mode != ModeConfirmDelete {
		t.Fatalf("mode = %v, want ModeConfirmDelete", m.mode)
	}
	if got := m.overlayViewport.YOffset; got != 0 {
		t.Fatalf("confirm dialog opened pre-scrolled: YOffset = %d", got)
	}
}

func TestHelpOverlayScrollsWhileControlsRemainVisible(t *testing.T) {
	m := layoutTestModel(1)
	m.width, m.height = 50, 12
	m.mode = ModeHelp

	m.View() // populate the viewport content before sending scroll input
	before := m.overlayViewport.YOffset
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	view := m.View()

	if m.overlayViewport.YOffset <= before {
		t.Fatalf("help viewport did not scroll: before=%d after=%d", before, m.overlayViewport.YOffset)
	}
	if !strings.Contains(view, "?/esc close") {
		t.Fatalf("sticky help controls are not visible after scrolling:\n%s", view)
	}
}

func TestFooterUpdateNoticeFallsBackAsWidthShrinks(t *testing.T) {
	m := layoutTestModel(1)
	m.Version = "0.5.3"
	m.UpdateVersion = "0.5.4"

	full := "v0.5.3 → v0.5.4 (u to update)"
	short := "v0.5.3 → v0.5.4"
	plain := "v0.5.3"

	// Wide enough for the full instruction.
	if got := m.hintRowWithVersion("? help", lipgloss.Width("? help")+lipgloss.Width(full)+1); !strings.Contains(got, full) {
		t.Fatalf("wide footer = %q, want it to contain %q", got, full)
	}
	// Too narrow for the instruction, wide enough for the arrow form.
	if got := m.hintRowWithVersion("? help", lipgloss.Width("? help")+lipgloss.Width(short)+1); !strings.Contains(got, short) || strings.Contains(got, "u to update") {
		t.Fatalf("medium footer = %q, want it to contain %q but not the update hint", got, short)
	}
	// Too narrow for the arrow form, wide enough for the plain version.
	if got := m.hintRowWithVersion("? help", lipgloss.Width("? help")+lipgloss.Width(plain)+1); !strings.Contains(got, plain) || strings.Contains(got, "→") {
		t.Fatalf("narrow footer = %q, want it to contain %q but not an update arrow", got, plain)
	}
	// Too narrow for even the plain version: dropped entirely, same as today.
	if got := m.hintRowWithVersion("? help", lipgloss.Width("? help")); strings.Contains(got, "v0.5.3") {
		t.Fatalf("too-narrow footer unexpectedly contains version: %q", got)
	}
}

// A flash long enough to overflow the terminal wraps onto extra footer rows
// instead of being cut off — backend errors put the useful part (what git
// actually said) at the end.
func TestFlashWrapsInsteadOfTruncating(t *testing.T) {
	m := newTestModel(&fakeBackend{})
	m.width, m.height = 60, 24
	m.setFlash("error", "no branch \"merchant-physcal\" in /tmp/demo (checked local and origin) — fix the name, or clear the branch field")

	line := m.flashLine(m.width - 2)
	if lipgloss.Height(line) < 2 {
		t.Fatalf("flash not wrapped: %q", line)
	}
	if !strings.Contains(line, "field") {
		t.Fatalf("tail of the message was lost: %q", line)
	}
	if lipgloss.Height(line) > flashMaxLines {
		t.Fatalf("flash grew past the cap: %d lines", lipgloss.Height(line))
	}
}
