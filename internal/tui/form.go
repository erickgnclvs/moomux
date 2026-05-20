package tui

import (
	"fmt"
	"strings"
)

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
	b.WriteString(muteStyle.Render("enter to create   esc to cancel"))
	return b.String()
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
	b.WriteString("\n")
	if m.projForm.err != "" {
		b.WriteString(dangerStyle.Render(m.projForm.err))
		b.WriteString("\n\n")
	}
	b.WriteString(muteStyle.Render("tab/↑↓ to move   enter to save   esc to cancel"))
	return b.String()
}

func (m *Model) renderProjectInitChoice() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Path is not a git repository"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("path: %s\n\n", m.pending.p.Repo))
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
