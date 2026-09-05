package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// currentProjectFolders returns the active project's folder names,
// alphabetically — the list ModeFolders navigates and m.folderCursor indexes.
func (m *Model) currentProjectFolders() []string {
	if len(m.projects) == 0 {
		return nil
	}
	proj := m.projects[m.activeProj]
	folders := m.cfg.Projects[proj].Folders
	names := make([]string, 0, len(folders))
	for name := range folders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// updateFolderForm handles keys while ModeFolderForm is open — a single text
// input that either files the session at folderFormSessionID into the typed
// folder (folderFormKind == "assign"; blank removes it from one), renames
// the folder at folderFormOldName (folderFormKind == "rename"), or adds a
// new, empty folder by the typed name (folderFormKind == "create").
func (m *Model) updateFolderForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		name := strings.TrimSpace(m.folderForm.input.Value())
		switch m.folderFormKind {
		case "assign":
			id := m.folderFormSessionID
			m.mode = m.sessionDialogReturn
			return m, func() tea.Msg {
				s, err := m.backend.SetSessionFolder(id, name)
				if err != nil {
					return ErrorMsg{Err: err}
				}
				return SessionFolderSetMsg{Session: s}
			}
		case "rename":
			if name == "" || len(m.projects) == 0 {
				return m, nil
			}
			proj := m.projects[m.activeProj]
			old := m.folderFormOldName
			m.mode = ModeFolders
			return m, func() tea.Msg {
				err := m.backend.RenameFolder(proj, old, name)
				return FolderRenamedMsg{OldName: old, NewName: name, Err: err}
			}
		case "create":
			if name == "" || len(m.projects) == 0 {
				return m, nil
			}
			proj := m.projects[m.activeProj]
			m.mode = ModeFolders
			return m, func() tea.Msg {
				err := m.backend.CreateFolder(proj, name)
				return FolderCreatedMsg{Project: proj, Name: name, Err: err}
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.folderForm.input, cmd = m.folderForm.input.Update(msg)
	return m, cmd
}

func (m *Model) renderFolderForm() string {
	title := "Assign to folder"
	switch m.folderFormKind {
	case "rename":
		title = "Rename folder"
	case "create":
		title = "New folder"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")
	b.WriteString(m.renderFormLabel("folder", 12))
	b.WriteString(m.folderForm.input.View())
	b.WriteString("\n\n")
	if m.folderFormKind == "assign" {
		b.WriteString(muteStyle.Render("blank removes it from any folder"))
	}
	return b.String()
}

// folderPickerRowMarker mirrors projectPickerRowMarker — see its doc.
const folderPickerRowMarker = "▸ "

// updateFolders handles keys while ModeFolders is open: ↑↓/jk move the
// cursor, enter/o toggles the highlighted folder's collapsed state, e
// renames it, d deletes it (un-parenting members back to top-level), n
// creates a new empty one.
func (m *Model) updateFolders(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	names := m.currentProjectFolders()
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.New):
		if len(m.projects) == 0 {
			return m, nil
		}
		m.mode = ModeFolderForm
		m.folderFormKind = "create"
		m.folderFormOldName = ""
		m.folderForm = newFolderForm("folder name", "")
		m.resetOverlayViewport()
		m.resizeFormInputs()
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if len(names) > 0 {
			m.folderCursor = (m.folderCursor - 1 + len(names)) % len(names)
		}
	case key.Matches(msg, m.keys.Down):
		if len(names) > 0 {
			m.folderCursor = (m.folderCursor + 1) % len(names)
		}
	case key.Matches(msg, m.keys.Enter), key.Matches(msg, m.keys.Open):
		if m.folderCursor < len(names) && len(m.projects) > 0 {
			proj := m.projects[m.activeProj]
			name := names[m.folderCursor]
			collapsed := !m.cfg.Projects[proj].Folders[name].Collapsed
			return m, func() tea.Msg {
				err := m.backend.SetFolderCollapsed(proj, name, collapsed)
				return FolderCollapsedSetMsg{Project: proj, Name: name, Collapsed: collapsed, Err: err}
			}
		}
	case key.Matches(msg, m.keys.EditSession):
		if m.folderCursor < len(names) {
			name := names[m.folderCursor]
			m.mode = ModeFolderForm
			m.folderFormKind = "rename"
			m.folderFormOldName = name
			m.folderForm = newFolderForm("new folder name", name)
			m.resetOverlayViewport()
			m.resizeFormInputs()
		}
		return m, nil
	case key.Matches(msg, m.keys.Delete):
		if m.folderCursor < len(names) && len(m.projects) > 0 {
			proj := m.projects[m.activeProj]
			name := names[m.folderCursor]
			return m, func() tea.Msg {
				err := m.backend.DeleteFolder(proj, name)
				return FolderDeletedMsg{Project: proj, Name: name, Err: err}
			}
		}
	}
	return m, nil
}

// renderFolders renders the cursor-navigable folder list opened by Folders
// (G) — collapse state, name, and current member count per row.
func (m *Model) renderFolders() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("FOLDERS"))
	b.WriteString("\n\n")
	names := m.currentProjectFolders()
	if len(names) == 0 {
		b.WriteString(muteStyle.Render("no folders yet — press n to create one, or g on a session to file it into one"))
		return b.String()
	}
	proj := m.projects[m.activeProj]
	counts := map[string]int{}
	for _, s := range m.backend.Sessions() {
		if s.Project == proj && s.Folder != "" && s.Archived == m.showArchived {
			counts[s.Folder]++
		}
	}
	rowWidth := m.overlayWidth(formHintWidth) - 2
	for i, name := range names {
		selected := i == m.folderCursor
		prefix := "  "
		if selected {
			prefix = folderPickerRowMarker
		}
		glyph := "▾"
		if m.cfg.Projects[proj].Folders[name].Collapsed {
			glyph = "▸"
		}
		detail := fmt.Sprintf("%s (%d)", glyph, counts[name])
		avail := rowWidth - lipgloss.Width(prefix) - lipgloss.Width(detail) - 1
		if avail < 4 {
			avail = 4
		}
		row := prefix + fmt.Sprintf("%-*s", avail, truncate(name, avail)) + " " + detail
		if selected {
			row = listRowSelected.Render(row)
		} else {
			row = listRow.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return b.String()
}

// foldersFooter mirrors projectPickerFooter's width-tiered approach.
func (m *Model) foldersFooter() string {
	full := "↑↓ select  enter collapse/expand  n new  e rename  d delete  esc close"
	short := "esc close  enter toggle"
	controls := full
	if lipgloss.Width(controls) > m.overlayWidth(formHintWidth) {
		controls = short
	}
	return muteStyle.Render(controls)
}
