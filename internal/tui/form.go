package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// newFormFieldHints gives a one-line explanation for whichever field of the
// new-session form is currently focused, so the jargon (worktree, base
// branch) doesn't have to be memorized up front.
var newFormFieldHints = []string{
	0: "shown in the session list and used for the worktree folder name — leave blank to derive one from the branch",
	1: "an existing branch to resume, or a new one to branch off the project's base branch",
	2: "optional — shown as a clickable ticket icon next to the session",
}

func (m *Model) renderNewForm() string {
	var b strings.Builder
	proj := ""
	if len(m.projects) > 0 {
		proj = m.projects[m.activeProj]
	}
	b.WriteString(titleStyle.Render("New session"))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render(fmt.Sprintf("project: %s", proj)))
	b.WriteString("\n\n")
	b.WriteString(m.nameInput.View())
	b.WriteString("\n\n")
	b.WriteString(m.branchInput.View())
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("agent:  "))
	b.WriteString(m.renderNewFormAgentSelector())
	b.WriteString("\n\n")
	b.WriteString(m.ticketInput.View())
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render(newFormFieldHints[m.newFormFocus]))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("tab to switch field   ←→ to pick agent   enter to create   esc to cancel"))
	return b.String()
}

func (m *Model) renderNewFormAgentSelector() string {
	var b strings.Builder
	for i, a := range agentChoices {
		if i > 0 {
			b.WriteString("  ")
		}
		if i == m.newFormAgentIdx {
			b.WriteString(titleStyle.Render("[" + a + "]"))
		} else {
			b.WriteString(muteStyle.Render(a))
		}
	}
	return b.String()
}

// projFormFieldHints gives a one-line explanation for whichever field of the
// add-project form is currently focused (index projFormInputCount is the
// agent selector), so terms like "base branch" or "branch prefix" don't need
// to be looked up elsewhere.
var projFormFieldHints = []string{
	0: "internal label for this project — shown in the tabs at the top",
	1: "path to the project's git repo — prefilled from the current directory; edit it, or point elsewhere",
	2: "the branch new session worktrees branch off of (usually main or master)",
	3: "prepended to every new session's branch name, e.g. \"alice/\" → alice/feature-x — leave blank to skip",
	4: "coding agent launched by default for new sessions in this project",
	5: "off: every session runs directly in the repo folder instead of its own worktree/branch",
}

func (m *Model) renderNewProject() string {
	labels := []string{"name", "repo", "base branch", "branch prefix"}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Add project"))
	b.WriteString("\n\n")
	for i, ti := range m.projForm.inputs {
		b.WriteString(muteStyle.Render(fmt.Sprintf("%-15s", labels[i]+":")))
		b.WriteString(ti.View())
		b.WriteString("\n")
	}
	b.WriteString(muteStyle.Render(fmt.Sprintf("%-15s", "agent:")))
	b.WriteString(m.renderAgentSelector())
	b.WriteString("\n")
	b.WriteString(muteStyle.Render(fmt.Sprintf("%-15s", "worktrees:")))
	b.WriteString(m.renderWorktreeToggle())
	b.WriteString("\n\n")
	if m.projForm.err != "" {
		b.WriteString(dangerStyle.Render(m.projForm.err))
		b.WriteString("\n\n")
	}
	b.WriteString(hintStyle.Render(projFormFieldHints[m.projForm.focus]))
	b.WriteString("\n\n")
	b.WriteString(muteStyle.Render("tab/↑↓ to move   ←→ to pick agent/toggle   enter to save   esc to cancel"))
	return b.String()
}

func (m *Model) renderWorktreeToggle() string {
	focused := m.projForm.focus == projFormInputCount+1
	choice := "on"
	if m.projForm.noWorktree {
		choice = "off"
	}
	label := "[" + choice + "]"
	if focused {
		return titleStyle.Render(label)
	}
	return lipgloss.NewStyle().Bold(true).Render(label)
}

func (m *Model) renderAgentSelector() string {
	focused := m.projForm.focus == projFormInputCount
	var b strings.Builder
	for i, a := range agentChoices {
		if i > 0 {
			b.WriteString("  ")
		}
		if i == m.projForm.agentIdx {
			if focused {
				b.WriteString(titleStyle.Render("[" + a + "]"))
			} else {
				b.WriteString(lipgloss.NewStyle().Bold(true).Render("[" + a + "]"))
			}
		} else {
			b.WriteString(muteStyle.Render(a))
		}
	}
	return b.String()
}

func (m *Model) renderTagForm() string {
	labels := []string{"ticket url", "pr url"}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Tag session"))
	b.WriteString("\n\n")
	for i, ti := range m.tagForm.inputs {
		b.WriteString(muteStyle.Render(fmt.Sprintf("%-12s", labels[i]+":")))
		b.WriteString(ti.View())
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("tab to move   enter to save   esc to cancel"))
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
