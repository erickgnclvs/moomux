package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// formHintWidth and formHintLines keep every form's hint row a fixed size —
// wrapped to this width and padded to this many lines — so switching fields
// (and thus hint text) never resizes the overlay box. formHintWidth is a cap;
// on narrow terminals it shrinks to fit so the overlay never exceeds the
// screen width.
const (
	formHintWidth = 72
	formHintLines = 2
)

// overlayWidth returns the content width available to overlay forms: capped
// at max, but shrunk to fit narrow terminals (accounting for overlayBox's
// border + padding).
func (m *Model) overlayWidth(max int) int {
	w := m.width - overlayBox.GetHorizontalFrameSize()
	if w > max {
		w = max
	}
	if w < 1 {
		w = 1
	}
	return w
}

// clampInputWidth shrinks desired to fit avail, never grows it — text inputs
// keep their designed width on normal terminals and only narrow on small ones.
func clampInputWidth(desired, avail int) int {
	if avail < 1 {
		avail = 1
	}
	if desired > avail {
		return avail
	}
	return desired
}

func textInputWidth(ti *textinput.Model, desired, avail int) int {
	// textinput.Width does not include its prompt ("> " by default), so
	// reserve those cells explicitly. Its View also renders a cursor cell
	// after short values, which must fit without clipping the dialog border.
	return clampInputWidth(desired, avail-lipgloss.Width(ti.Prompt)-1)
}

// setInputWidth sets a text input's Width and re-runs its overflow
// calculation — textinput only recomputes its scroll offset on cursor/value
// changes, not when Width is set directly, so a stale offset would otherwise
// leave the old (wider) content un-truncated.
func setInputWidth(ti *textinput.Model, w int) {
	pos := ti.Position()
	ti.Width = w
	ti.SetCursor(pos)
}

// resizeFormInputs re-clamps every form's text-input widths to the current
// terminal size. Called on resize and whenever a form is (re)opened, since
// the forms are built with fixed default widths that only fit on typical
// terminals.
func (m *Model) resizeFormInputs() {
	avail := m.overlayWidth(72)

	setInputWidth(&m.nameInput, textInputWidth(&m.nameInput, 40, avail))
	setInputWidth(&m.sessionForm.nameInput, textInputWidth(&m.sessionForm.nameInput, 40, avail))
	setInputWidth(&m.branchInput, textInputWidth(&m.branchInput, 40, avail))
	setInputWidth(&m.baseBranchInput, textInputWidth(&m.baseBranchInput, 40, avail))
	setInputWidth(&m.ticketInput, textInputWidth(&m.ticketInput, 40, avail))
	setInputWidth(&m.prInput, textInputWidth(&m.prInput, 40, avail))
	setInputWidth(&m.newFormModelInput, textInputWidth(&m.newFormModelInput, 40, avail))
	// Unlike the single-line fields above, the textarea soft-wraps its
	// content at its width instead of just scrolling — capping it to the
	// same fixed 40 they use would wrap lines well before the box's actual
	// edge on wide terminals, so let it fill the available width instead.
	m.promptInput.SetWidth(avail)

	if len(m.tagForm.inputs) == 2 {
		labels := []string{"ticket url", "pr url"}
		for i := range m.tagForm.inputs {
			w := textInputWidth(&m.tagForm.inputs[i], 48, avail-m.formLabelWidth(labels[i], 12))
			setInputWidth(&m.tagForm.inputs[i], w)
		}
	}

	labels := []string{"name", "repo", "base branch", "branch prefix"}
	projWidths := [4]int{32, 48, 24, 24}
	for i := range m.projForm.inputs {
		if i < len(projWidths) {
			labelWidth := m.formLabelWidth(labels[i], 15)
			w := textInputWidth(&m.projForm.inputs[i], projWidths[i], avail-labelWidth)
			setInputWidth(&m.projForm.inputs[i], w)
		}
	}
}

func (m *Model) formLabelWidth(label string, desired int) int {
	available := m.overlayWidth(formHintWidth)
	if available < 40 {
		// Drop alignment-only padding on narrow screens and give the value or
		// input the reclaimed cells.
		desired = lipgloss.Width(label + ":")
	}
	if desired >= available {
		desired = available - 1
	}
	if desired < 1 {
		desired = 1
	}
	return desired
}

func (m *Model) renderFormLabel(label string, desired int) string {
	width := m.formLabelWidth(label, desired)
	return muteStyle.Width(width).Render(truncateToWidth(label+":", width))
}

func (m *Model) renderFormHint(text string) string {
	// MaxHeight caps a hint long enough to wrap past formHintLines — without
	// it, that one field's hint would grow the overlay box taller than
	// every other field's, breaking the "fixed size hint row" the constants
	// above promise.
	rendered := hintStyle.Width(m.overlayWidth(formHintWidth)).MaxHeight(formHintLines).Render(text)
	for lipgloss.Height(rendered) < formHintLines {
		rendered += "\n"
	}
	return rendered
}

// newFormFieldHints gives a one-line explanation for whichever field of the
// new-session form is currently focused, so the jargon (worktree, base
// branch) doesn't have to be memorized up front.
// newFormFieldCount is the focus cycle length: project selector, name,
// branch, base branch, prompt, ticket, PR, agent selector, model selector,
// thinking selector, dangerous toggle, open-terminal toggle, auto-submit
// toggle — matching the rendered order. The named constants below are the
// newFormFocus values for the non-text rows in that order; the text inputs
// are referred to by their bare index.
const (
	newFormFieldCount        = 13
	newFormAgentFocus        = 7
	newFormModelFocus        = 8
	newFormThinkingFocus     = 9
	newFormDangerousFocus    = 10
	newFormOpenTerminalFocus = 11
	newFormAutoSubmitFocus   = 12
)

var newFormFieldHints = []string{
	0:  "which project this session belongs to — ←→ to choose",
	1:  "shown in the list and worktree folder — blank uses branch",
	2:  "resume an existing branch; blank = new branch off base",
	3:  "only used for a new branch — blank uses the project's base branch",
	4:  "optional — the agent's first task; enter for a newline, tab away to submit",
	5:  "optional — shown as a clickable ticket icon next to the session",
	6:  "optional — shown as a clickable PR icon next to the session",
	7:  "which agent CLI runs in the session's pane — ←→ to choose",
	8:  "optional — passed as --model to the agent; \"default\" omits the flag",
	9:  "optional — prepended to the first prompt (e.g. \"ultrathink: ...\"); no effect without a prompt",
	10: "on: skips permission prompts (--dangerously-skip-permissions / --yolo); no effect for opencode",
	11: "on: starts the session in the background, no terminal window",
	12: "on: presses enter after typing the first prompt so the agent starts right away; off: leaves it typed for you to review first",
}

func (m *Model) renderNewForm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New session"))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("project:  "))
	b.WriteString(m.renderNewFormProjectSelector())
	b.WriteString("\n\n")
	b.WriteString(m.nameInput.View())
	b.WriteString("\n\n")
	b.WriteString(m.branchInput.View())
	b.WriteString("\n\n")
	b.WriteString(m.baseBranchInput.View())
	b.WriteString("\n\n")
	b.WriteString(m.promptInput.View())
	b.WriteString("\n\n")
	b.WriteString(m.ticketInput.View())
	b.WriteString("\n\n")
	b.WriteString(m.prInput.View())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("agent:  "))
	b.WriteString(m.renderNewFormAgentSelector())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("model:  "))
	b.WriteString(m.renderNewFormModelSelector())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("thinking:  "))
	b.WriteString(m.renderNewFormThinkingSelector())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("dangerous:  "))
	b.WriteString(m.renderNewFormDangerousToggle())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("open in background:  "))
	b.WriteString(m.renderNewFormOpenTerminalToggle())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("auto-submit:  "))
	b.WriteString(m.renderNewFormAutoSubmitToggle())
	return b.String()
}

// renderToggle renders a form's on/off toggle value, in the highlighted style
// when it's the focused control.
func renderToggle(on, focused bool) string {
	label := "[off]"
	if on {
		label = "[on]"
	}
	if focused {
		return titleStyle.Render(label)
	}
	return lipgloss.NewStyle().Bold(true).Render(label)
}

// renderSelector renders a horizontal "[selected]  other  other" choice row,
// degrading to renderCompactSelector when the full row won't fit available.
func renderSelector(choices []string, selected int, focused bool, available int) string {
	if selected < 0 || selected >= len(choices) {
		return ""
	}
	var b strings.Builder
	for i, c := range choices {
		if i > 0 {
			b.WriteString("  ")
		}
		switch {
		case i != selected:
			b.WriteString(muteStyle.Render(c))
		case focused:
			b.WriteString(titleStyle.Render("[" + c + "]"))
		default:
			b.WriteString(lipgloss.NewStyle().Bold(true).Render("[" + c + "]"))
		}
	}
	rendered := b.String()
	if lipgloss.Width(rendered) > available {
		return renderCompactSelector(choices[selected], focused, available)
	}
	return rendered
}

func (m *Model) renderNewFormDangerousToggle() string {
	return renderToggle(m.newFormDangerous, m.newFormFocus == newFormDangerousFocus)
}

func (m *Model) renderNewFormOpenTerminalToggle() string {
	return renderToggle(m.newFormOpenInBackground, m.newFormFocus == newFormOpenTerminalFocus)
}

func (m *Model) renderNewFormAutoSubmitToggle() string {
	return renderToggle(m.newFormAutoSubmit, m.newFormFocus == newFormAutoSubmitFocus)
}

// newFormProjFocus is the newFormFocus value for the project selector row.
const newFormProjFocus = 0

func (m *Model) renderNewFormProjectSelector() string {
	if len(m.projects) == 0 {
		return ""
	}
	if m.newFormProjIdx < 0 {
		return warnStyle.Render("choose a project (←→)")
	}
	return renderSelector(
		m.projects, m.newFormProjIdx,
		m.newFormFocus == newFormProjFocus,
		m.overlayWidth(formHintWidth)-lipgloss.Width("project:  "),
	)
}

func (m *Model) renderNewFormAgentSelector() string {
	if m.newFormAgentIdx < 0 {
		return warnStyle.Render("choose an agent (←→)")
	}
	return renderSelector(
		m.agentNames(), m.newFormAgentIdx, m.newFormFocus == newFormAgentFocus,
		m.overlayWidth(formHintWidth)-lipgloss.Width("agent:  "),
	)
}

func (m *Model) renderNewFormModelSelector() string {
	agent := ""
	if m.newFormAgentIdx >= 0 {
		agent = m.agentNames()[m.newFormAgentIdx]
	}
	if agent == "opencode" {
		return m.newFormModelInput.View()
	}
	return renderSelector(
		m.modelNamesFor(agent), m.newFormModelIdx, m.newFormFocus == newFormModelFocus,
		m.overlayWidth(formHintWidth)-lipgloss.Width("model:  "),
	)
}

// newFormFieldHint returns the footer hint for the currently focused
// new-form field. It's newFormFieldHints[m.newFormFocus] for every row
// except the thinking selector, which reads differently for codex (a real
// -c model_reasoning_effort flag) than for claude/opencode (a phrase
// prepended to the first prompt).
func (m *Model) newFormFieldHint() string {
	if m.newFormFocus == newFormThinkingFocus && m.newFormAgentIdx >= 0 && m.agentNames()[m.newFormAgentIdx] == "codex" {
		return "optional — passed to codex as -c model_reasoning_effort; \"default\" omits it"
	}
	return newFormFieldHints[m.newFormFocus]
}

func (m *Model) renderNewFormThinkingSelector() string {
	agent := ""
	if m.newFormAgentIdx >= 0 {
		agent = m.agentNames()[m.newFormAgentIdx]
	}
	return renderSelector(
		m.thinkingNamesFor(agent), m.newFormThinkingIdx, m.newFormFocus == newFormThinkingFocus,
		m.overlayWidth(formHintWidth)-lipgloss.Width("thinking:  "),
	)
}

func renderCompactSelector(agent string, focused bool, available int) string {
	selected := lipgloss.NewStyle().Bold(true)
	if focused {
		selected = titleStyle
	}
	label := "[" + agent + "]"
	if available < lipgloss.Width(label) {
		return selected.Render(truncateToWidth(label, available))
	}
	if available >= lipgloss.Width(label)+4 {
		return muteStyle.Render("‹ ") + selected.Render(label) + muteStyle.Render(" ›")
	}
	return selected.Render(label)
}

func (m *Model) renderEditSession() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Edit session"))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("project: "))
	b.WriteString(m.sessionForm.project)
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("name:    "))
	b.WriteString(m.sessionForm.nameInput.View())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("agent:  "))
	b.WriteString(m.renderSessionAgentSelector())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("dangerous:  "))
	b.WriteString(m.renderSessionDangerousToggle())
	return b.String()
}

func (m *Model) renderSessionAgentSelector() string {
	return renderSelector(
		m.agentNames(), m.sessionForm.agentIdx,
		m.sessionForm.focus == sessionFormAgentFocus,
		m.overlayWidth(formHintWidth)-lipgloss.Width("agent:  "),
	)
}

func (m *Model) renderSessionDangerousToggle() string {
	return renderToggle(m.sessionForm.dangerous, m.sessionForm.focus == sessionFormDangerousFocus)
}

// editSessionFieldHints gives a one-line explanation for whichever control of
// the edit-session form is currently focused.
var editSessionFieldHints = []string{
	sessionFormNameFocus:      "display name; also renames the live tmux session so it stays aligned",
	sessionFormAgentFocus:     "agent used the next time this session's tmux process is created",
	sessionFormDangerousFocus: "on: skips permission prompts (--dangerously-skip-permissions / --yolo); no effect for opencode",
}

// projFormFieldHints gives a one-line explanation for whichever field of the
// add-project form is currently focused (index projFormInputCount is the
// agent selector), so terms like "base branch" or "branch prefix" don't need
// to be looked up elsewhere.
var projFormFieldHints = []string{
	0: "internal label for this project — shown in the tabs at the top",
	1: "path to the project's git repo — edit or point elsewhere",
	2: "the branch new session worktrees branch off of (usually main or master)",
	3: "prepended to new branch names, e.g. alice/feature-x — blank to skip",
	4: "shown instead of the project name in all-sessions — ←→ to choose",
	5: "default agent for new sessions — \"ask each time\" prompts every time",
	6: "on: new sessions run with the agent's permission-skipping flag; no effect for opencode or \"ask each time\"",
	7: "off: sessions run directly in the repo, no worktree/branch",
}

var editProjectFieldHints = []string{
	0: "project names cannot be changed",
	1: "repo path used by new sessions — existing worktrees stay put",
	2: "base branch used when creating new session worktrees",
	3: "prepended to branches created for new sessions — leave blank to skip",
	4: "shown instead of the project name in all-sessions — ←→ to choose",
	5: "default agent for new sessions — \"ask each time\" prompts every time",
	6: "on: new sessions run with the agent's permission-skipping flag; no effect for opencode or \"ask each time\"",
	7: "changes worktree behavior for new sessions only",
}

func (m *Model) renderNewProject() string {
	labels := []string{"name", "repo", "base branch", "branch prefix"}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Add project"))
	b.WriteString("\n\n")
	for i, ti := range m.projForm.inputs {
		b.WriteString(m.renderFormLabel(labels[i], 15))
		b.WriteString(ti.View())
		b.WriteString("\n")
	}
	b.WriteString(m.renderFormLabel("emoji", 15))
	b.WriteString(m.renderProjectEmojiSelector())
	b.WriteString("\n")
	b.WriteString(m.renderFormLabel("agent", 15))
	b.WriteString(m.renderAgentSelector())
	b.WriteString("\n")
	b.WriteString(m.renderFormLabel("dangerous", 15))
	b.WriteString(m.renderProjectDangerousToggle())
	b.WriteString("\n")
	b.WriteString(m.renderFormLabel("worktrees", 15))
	b.WriteString(m.renderWorktreeToggle())
	b.WriteString("\n\n")
	return b.String()
}

func (m *Model) renderEditProject() string {
	project := m.cfg.Projects[m.editProjectName]
	var b strings.Builder
	b.WriteString(titleStyle.Render("Edit project"))
	b.WriteString("\n\n")
	b.WriteString(m.renderFormLabel("name", 15))
	b.WriteString(m.editProjectName)
	b.WriteString(muteStyle.Render(" (fixed)"))
	b.WriteString("\n")
	b.WriteString(m.renderFormLabel("repo", 15))
	b.WriteString(m.projForm.inputs[1].View())
	b.WriteString("\n")
	if !project.IsPlain() {
		b.WriteString(m.renderFormLabel("base branch", 15))
		b.WriteString(m.projForm.inputs[2].View())
		b.WriteString("\n")
		b.WriteString(m.renderFormLabel("branch prefix", 15))
		b.WriteString(m.projForm.inputs[3].View())
		b.WriteString("\n")
	}
	b.WriteString(m.renderFormLabel("emoji", 15))
	b.WriteString(m.renderProjectEmojiSelector())
	b.WriteString("\n")
	b.WriteString(m.renderFormLabel("agent", 15))
	b.WriteString(m.renderAgentSelector())
	b.WriteString("\n")
	b.WriteString(m.renderFormLabel("dangerous", 15))
	b.WriteString(m.renderProjectDangerousToggle())
	b.WriteString("\n")
	if !project.IsPlain() {
		b.WriteString(m.renderFormLabel("worktrees", 15))
		b.WriteString(m.renderWorktreeToggle())
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) renderProjectEmojiSelector() string {
	return renderSelector(
		m.projForm.emojiChoices, m.projForm.emojiIdx,
		m.projForm.focus == projFormInputCount,
		m.overlayWidth(formHintWidth)-m.formLabelWidth("emoji", 15),
	)
}

func (m *Model) renderWorktreeToggle() string {
	return renderToggle(!m.projForm.noWorktree, m.projForm.focus == projFormInputCount+3)
}

func (m *Model) renderProjectDangerousToggle() string {
	return renderToggle(m.projForm.dangerous, m.projForm.focus == projFormInputCount+2)
}

// projectAgentChoices is the project form's agent selector: the real agents,
// plus a trailing "ask each time" entry (index askAgentIdx maps to it) that
// defers the choice to each new-session form instead of fixing one here.
func (m *Model) projectAgentChoices() []string {
	return append(append([]string{}, m.agentNames()...), "ask each time")
}

func (m *Model) renderAgentSelector() string {
	selectedIdx := m.projForm.agentIdx
	if selectedIdx == askAgentIdx {
		selectedIdx = len(m.agentNames())
	}
	return renderSelector(
		m.projectAgentChoices(), selectedIdx,
		m.projForm.focus == projFormInputCount+1,
		m.overlayWidth(formHintWidth)-m.formLabelWidth("agent", 15),
	)
}

// tagFormFieldHints gives a one-line explanation for whichever field of the
// tag form is currently focused, matching the other forms' contextual hints.
var tagFormFieldHints = []string{
	0: "shown as a clickable ticket icon next to the session",
	1: "shown as a clickable PR icon next to the session",
}

func (m *Model) renderTagForm() string {
	labels := []string{"ticket url", "pr url"}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Tag session"))
	b.WriteString("\n\n")
	for i, ti := range m.tagForm.inputs {
		b.WriteString(m.renderFormLabel(labels[i], 12))
		b.WriteString(ti.View())
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m *Model) renderProjectInitChoice() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Path is not a git repository"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("path: %s\n", m.pending.p.Repo))
	if w := tccWarning(m.pending.p.Repo); w != "" {
		b.WriteString(warnStyle.Width(64).Render(w))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("How should moomux set this up?\n\n")
	b.WriteString("  i  ")
	b.WriteString(muteStyle.Render("init a new git repo here (mkdir + git init + empty commit)"))
	b.WriteString("\n")
	b.WriteString("  s  ")
	b.WriteString(muteStyle.Render("skip git — manage as a plain folder, no worktrees/branches"))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("b/esc to go back to the form"))
	return b.String()
}

func (m *Model) renderConfirmDeleteProject() string {
	if len(m.projects) == 0 {
		return ""
	}
	name := m.projects[m.activeProj]
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Remove project?"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("name: %s\n", name))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("This only removes the entry from config."))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("Worktrees and the repo itself are untouched."))
	b.WriteString("\n\n")
	b.WriteString("y to confirm   n/esc to cancel")
	return b.String()
}
