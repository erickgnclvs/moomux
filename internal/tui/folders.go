package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/sessionview"
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
				return SessionFolderSetMsg{Session: s, Cfg: m.cfgSnapshotOnSuccess(err)}
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
				return FolderRenamedMsg{OldName: old, NewName: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
			}
		case "create":
			if name == "" || len(m.projects) == 0 {
				return m, nil
			}
			proj := m.projects[m.activeProj]
			m.mode = ModeFolders
			return m, func() tea.Msg {
				err := m.backend.CreateFolder(proj, name)
				return FolderCreatedMsg{Project: proj, Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
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
		// Existing names, so filing into a folder that already exists is a
		// matter of copying one rather than remembering it exactly: any
		// typo here silently creates a second, near-identical folder.
		if names := m.currentProjectFolders(); len(names) > 0 {
			b.WriteString("\n")
			b.WriteString(muteStyle.Render(truncate("existing: "+strings.Join(names, ", "), m.overlayWidth(formHintWidth))))
		}
	}
	return b.String()
}

// folderCounts is each folder's member count in the view the list is
// currently showing, read off the same rows the list renders so the overlay
// and the list headers can't disagree.
func (m *Model) folderCounts(proj string) map[string]int {
	counts := map[string]int{}
	for _, r := range sessionview.BuildRows(m.allSessions(), m.cfg.Projects[proj].Folders, proj) {
		if r.IsFolder() {
			counts[r.Folder] = m.memberCount(r)
		}
	}
	return counts
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
				return FolderCollapsedSetMsg{Project: proj, Name: name, Collapsed: collapsed, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
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
			m.folderDeleteName = names[m.folderCursor]
			m.mode = ModeConfirmDeleteFolder
			m.resetOverlayViewport()
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
	counts := m.folderCounts(proj)
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

// updateConfirmDeleteFolder handles the y/n confirmation before a folder is
// deleted. Nothing is lost — members are put back at top-level, not
// removed — but re-filing them is one keystroke each, so it asks first, the
// same as removing a session or a project does.
func (m *Model) updateConfirmDeleteFolder(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		name := m.folderDeleteName
		m.mode = ModeFolders
		if name == "" || len(m.projects) == 0 {
			return m, nil
		}
		proj := m.projects[m.activeProj]
		return m, func() tea.Msg {
			err := m.backend.DeleteFolder(proj, name)
			return FolderDeletedMsg{Project: proj, Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	case "n", "esc":
		m.mode = ModeFolders
	}
	return m, nil
}

func (m *Model) renderConfirmDeleteFolder() string {
	name := m.folderDeleteName
	count := 0
	if len(m.projects) > 0 {
		count = m.folderCounts(m.projects[m.activeProj])[name]
	}
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Delete folder?"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("name: %s\n", name))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render(fmt.Sprintf("%d session(s) move back to top level.", count)))
	b.WriteString("\n")
	b.WriteString(muteStyle.Render("No session or worktree is deleted."))
	b.WriteString("\n\n")
	b.WriteString("y to confirm   n/esc to cancel")
	return b.String()
}
