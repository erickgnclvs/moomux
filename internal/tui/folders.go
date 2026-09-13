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

// currentFolders returns every folder name, alphabetically — the list
// ModeFolders navigates and m.folderCursor indexes.
//
// Folders are one flat global namespace, so this enumerates all of them
// rather than the project in view. The overlay is the only place a folder is
// created, renamed or deleted, and filtering it to the active project would
// make a folder with no members here unreachable — including the one the
// user just created, which by definition has none anywhere yet.
func (m *Model) currentFolders() []string {
	names := make([]string, 0, len(m.cfg.Folders))
	for name := range m.cfg.Folders {
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
			if name == "" {
				return m, nil
			}
			old := m.folderFormOldName
			m.mode = ModeFolders
			return m, func() tea.Msg {
				err := m.backend.RenameFolder(old, name)
				return FolderRenamedMsg{OldName: old, NewName: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
			}
		case "create":
			if name == "" {
				return m, nil
			}
			m.mode = ModeFolders
			return m, func() tea.Msg {
				err := m.backend.CreateFolder(name)
				return FolderCreatedMsg{Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
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
		if names := m.currentFolders(); len(names) > 0 {
			b.WriteString("\n")
			b.WriteString(muteStyle.Render(truncate("existing: "+strings.Join(names, ", "), m.overlayWidth(formHintWidth))))
		}
	}
	return b.String()
}

// folderCounts is each folder's member count in the view the list is
// currently showing, read off the same rows the list renders so the overlay
// and the list headers can't disagree.
//
// The folder table is global now but this stays project-filtered: a global
// count would print "auth (7)" in the overlay above a list showing two rows
// under that header. A folder with no members in this project isn't in the
// map at all, so the overlay renders it as "auth (0)".
func (m *Model) folderCounts(proj string) map[string]int {
	counts := map[string]int{}
	for _, r := range sessionview.BuildRows(m.allSessions(), m.cfg.Folders, proj) {
		if r.IsFolder() {
			counts[r.Folder] = m.memberCount(r)
		}
	}
	return counts
}

// globalMemberCount is how many sessions are filed under name, and how many
// distinct projects they span — across every project and across both views,
// archived included.
//
// This is the one place both of folderCounts' filters are the wrong answer.
// App.DeleteFolder un-parents through refileSessions, which walks the whole
// store with no project filter and no archived filter, so a dialog that
// counted the way a folder header counts would promise less than y does on
// both axes: it would quote the active project's share, and it would quote
// only the view the list happens to be filtered to.
func (m *Model) globalMemberCount(name string) (sessions, projects int) {
	seen := map[string]bool{}
	for _, s := range m.allSessions() {
		if s.Folder == name {
			sessions++
			seen[s.Project] = true
		}
	}
	return sessions, len(seen)
}

// folderPickerRowMarker mirrors projectPickerRowMarker — see its doc.
const folderPickerRowMarker = "▸ "

// updateFolders handles keys while ModeFolders is open: ↑↓/jk move the
// cursor, enter/o toggles the highlighted folder's collapsed state, e
// renames it, d deletes it (un-parenting members back to top-level), n
// creates a new empty one.
func (m *Model) updateFolders(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	names := m.currentFolders()
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = m.sessionDialogReturn
		return m, nil
	case key.Matches(msg, m.keys.New):
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
		if m.folderCursor < len(names) {
			name := names[m.folderCursor]
			collapsed := !m.cfg.Folders[name].Collapsed
			return m, m.setFolderCollapsedCmd(name, collapsed, "")
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
		if m.folderCursor < len(names) {
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
	names := m.currentFolders()
	if len(names) == 0 {
		b.WriteString(muteStyle.Render("no folders yet — press n to create one, or g on a session to file it into one"))
		return b.String()
	}
	// Folders outlive the projects they were filled from, so the overlay
	// opens with none configured; there are simply no per-project counts
	// to show then.
	counts := map[string]int{}
	if len(m.projects) > 0 {
		counts = m.folderCounts(m.projects[m.activeProj])
	}
	rowWidth := m.overlayWidth(formHintWidth) - 2
	for i, name := range names {
		selected := i == m.folderCursor
		prefix := "  "
		if selected {
			prefix = folderPickerRowMarker
		}
		glyph := "▾"
		if m.cfg.Folders[name].Collapsed {
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
		if name == "" {
			return m, nil
		}
		return m, func() tea.Msg {
			err := m.backend.DeleteFolder(name)
			return FolderDeletedMsg{Name: name, Err: err, Cfg: m.cfgSnapshotOnSuccess(err)}
		}
	case "n", "esc":
		m.mode = ModeFolders
	}
	return m, nil
}

func (m *Model) renderConfirmDeleteFolder() string {
	name := m.folderDeleteName
	count, projects := m.globalMemberCount(name)
	// wrapLines, not truncate and not lipgloss's Width: these two lines are
	// the whole warning, so a narrow overlay has to wrap them rather than
	// clip one mid-word (at 40 columns "move back to top level." lost its
	// last word, which is the half that says nothing is destroyed). Width
	// would do the wrapping but pads every line out to the cap, widening
	// the box to 72 columns on a full-size terminal.
	body := func(text string) string {
		return muteStyle.Render(strings.Join(wrapLines(text, m.overlayWidth(formHintWidth)), "\n"))
	}
	// The Folders overlay one keystroke ago counted only the project being
	// viewed, so whenever this global count differs from that one, say
	// where the extra members came from rather than letting the number
	// jump unexplained. The gate is that difference and not "spans more
	// than one project": the starkest mismatch is a folder whose members
	// all sit in some project the user is not looking at, where the
	// overlay says (0) and this dialog would otherwise say 3.
	local := 0
	if len(m.projects) > 0 {
		local = m.folderCounts(m.projects[m.activeProj])[name]
	}
	blast := fmt.Sprintf("%d session(s) move back to top level.", count)
	if count != local {
		noun := "projects"
		if projects == 1 {
			noun = "project"
		}
		blast = fmt.Sprintf("%d session(s) across %d %s move back to top level.", count, projects, noun)
	}
	var b strings.Builder
	b.WriteString(dangerStyle.Render("Delete folder?"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("name: %s\n", name))
	b.WriteString("\n")
	b.WriteString(body(blast))
	b.WriteString("\n")
	b.WriteString(body("No session or worktree is deleted."))
	b.WriteString("\n\n")
	b.WriteString("y to confirm   n/esc to cancel")
	return b.String()
}
