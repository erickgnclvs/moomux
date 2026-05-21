package tui

import (
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case StatusTickMsg:
		for path, st := range msg.Snap.States {
			m.states[path] = st
		}
		m.refreshTmuxAlive()
		m.refreshPrompts()
		return m, listenStatus(m.statusCh)

	case TmuxKilledMsg:
		m.flash = "killed tmux"
		m.flashTime = time.Now()
		m.refreshTmuxAlive()
		m.refreshPrompts()
		return m, nil

	case InfoMsg:
		if !m.flashTime.IsZero() && time.Since(m.flashTime) > 3*time.Second {
			m.flash = ""
		}
		return m, tickFlash()

	case ErrorMsg:
		m.flash = "error: " + msg.Err.Error()
		m.flashTime = time.Now()
		return m, nil

	case SessionCreatedMsg:
		m.flash = "created " + msg.Session.Name
		m.flashTime = time.Now()
		m.refreshSessions()
		m.refreshTmuxAlive()
		m.refreshPrompts()
		return m, nil

	case SessionDeletedMsg:
		m.flash = "deleted"
		m.flashTime = time.Now()
		m.refreshSessions()
		m.refreshTmuxAlive()
		m.refreshPrompts()
		return m, nil

	case SessionOpenedMsg:
		m.flash = "opened " + msg.ID
		m.flashTime = time.Now()
		return m, nil

	case MosaicOpenedMsg:
		m.flash = "mosaic open — Ctrl+b p to return"
		m.flashTime = time.Now()
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case ModeNewForm:
			return m.updateNewForm(msg)
		case ModeConfirmDelete:
			return m.updateConfirm(msg)
		case ModeNewProject:
			return m.updateNewProject(msg)
		case ModeConfirmDeleteProject:
			return m.updateConfirmDeleteProject(msg)
		case ModeProjectInitChoice:
			return m.updateProjectInitChoice(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.cancelPoll()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
		}
	case key.Matches(msg, m.keys.Tab):
		if len(m.projects) > 0 {
			m.activeProj = (m.activeProj + 1) % len(m.projects)
			m.cursor = 0
			m.refreshSessions()
		}
	case key.Matches(msg, m.keys.Refresh):
		m.refreshSessions()
		m.refreshTmuxAlive()
		m.refreshPrompts()
	case key.Matches(msg, m.keys.Kill):
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			return m, func() tea.Msg {
				if err := m.backend.KillTmux(id); err != nil {
					return ErrorMsg{Err: err}
				}
				return TmuxKilledMsg{ID: id}
			}
		}
	case key.Matches(msg, m.keys.New):
		if len(m.projects) == 0 {
			return m.flashError(fmt.Errorf("no projects configured — edit ~/.config/moomux/config.toml"))
		}
		m.mode = ModeNewForm
		m.nameInput.SetValue("")
		m.nameInput.Focus()
	case key.Matches(msg, m.keys.Delete):
		if len(m.sessions) > 0 {
			m.mode = ModeConfirmDelete
		}
	case key.Matches(msg, m.keys.NewProject):
		m.mode = ModeNewProject
		m.projForm = newProjectForm()
		return m, nil
	case key.Matches(msg, m.keys.DelProject):
		if len(m.projects) == 0 {
			return m.flashError(fmt.Errorf("no projects to remove"))
		}
		m.mode = ModeConfirmDeleteProject
		return m, nil
	case key.Matches(msg, m.keys.Open):
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			return m, func() tea.Msg {
				if err := m.backend.OpenSession(id); err != nil {
					return ErrorMsg{Err: err}
				}
				return SessionOpenedMsg{ID: id}
			}
		}
	case key.Matches(msg, m.keys.Mosaic):
		return m, func() tea.Msg {
			if err := m.backend.OpenMosaic(); err != nil {
				return ErrorMsg{Err: err}
			}
			return MosaicOpenedMsg{}
		}
	}
	return m, nil
}

func (m *Model) updateNewForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeList
		return m, nil
	case "enter":
		name := m.nameInput.Value()
		if name == "" {
			return m, nil
		}
		proj := m.projects[m.activeProj]
		m.mode = ModeList
		m.flash = "creating " + name + "…"
		m.flashTime = time.Now()
		return m, func() tea.Msg {
			s, err := m.backend.CreateSession(proj, name)
			if err != nil {
				return ErrorMsg{Err: err}
			}
			return SessionCreatedMsg{Session: s}
		}
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if len(m.sessions) == 0 {
			m.mode = ModeList
			return m, nil
		}
		id := m.sessions[m.cursor].ID
		m.mode = ModeList
		return m, func() tea.Msg {
			if err := m.backend.DeleteSession(id); err != nil {
				return ErrorMsg{Err: err}
			}
			return SessionDeletedMsg{ID: id}
		}
	case "n", "esc":
		m.mode = ModeList
	}
	return m, nil
}

func (m *Model) updateNewProject(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeList
		return m, nil
	case "tab", "down":
		m.projForm.inputs[m.projForm.focus].Blur()
		m.projForm.focus = (m.projForm.focus + 1) % len(m.projForm.inputs)
		m.projForm.inputs[m.projForm.focus].Focus()
		return m, nil
	case "shift+tab", "up":
		m.projForm.inputs[m.projForm.focus].Blur()
		m.projForm.focus = (m.projForm.focus - 1 + len(m.projForm.inputs)) % len(m.projForm.inputs)
		m.projForm.inputs[m.projForm.focus].Focus()
		return m, nil
	case "enter":
		name := m.projForm.inputs[0].Value()
		repo := m.projForm.inputs[1].Value()
		base := m.projForm.inputs[2].Value()
		prefix := m.projForm.inputs[3].Value()
		if base == "" {
			base = "main"
		}
		p := config.Project{Repo: repo, BaseBranch: base, BranchPrefix: prefix}
		err := m.backend.AddProject(name, p)
		if err == nil {
			m.activateProject(name)
			m.mode = ModeList
			m.flash = "added project " + name
			m.flashTime = time.Now()
			return m, nil
		}
		if errors.Is(err, gitwt.ErrNotGitRepo) {
			m.pending = pendingProject{name: name, p: p}
			m.mode = ModeProjectInitChoice
			return m, nil
		}
		m.projForm.err = err.Error()
		return m, nil
	}
	var cmd tea.Cmd
	m.projForm.inputs[m.projForm.focus], cmd = m.projForm.inputs[m.projForm.focus].Update(msg)
	return m, cmd
}

func (m *Model) activateProject(name string) {
	m.refreshProjects()
	for i, n := range m.projects {
		if n == name {
			m.activeProj = i
			break
		}
	}
	m.cursor = 0
	m.refreshSessions()
}

func (m *Model) updateProjectInitChoice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "i":
		if err := m.backend.InitProjectAndAdd(m.pending.name, m.pending.p); err != nil {
			m.mode = ModeNewProject
			m.projForm.err = err.Error()
			return m, nil
		}
		name := m.pending.name
		m.activateProject(name)
		m.mode = ModeList
		m.flash = "initialized git repo + added " + name
		m.flashTime = time.Now()
		return m, nil
	case "s":
		if err := m.backend.AddPlainProject(m.pending.name, m.pending.p); err != nil {
			m.mode = ModeNewProject
			m.projForm.err = err.Error()
			return m, nil
		}
		name := m.pending.name
		m.activateProject(name)
		m.mode = ModeList
		m.flash = "added plain (non-git) project " + name
		m.flashTime = time.Now()
		return m, nil
	case "esc", "b":
		m.mode = ModeNewProject
		return m, nil
	}
	return m, nil
}

func (m *Model) updateConfirmDeleteProject(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if len(m.projects) == 0 {
			m.mode = ModeList
			return m, nil
		}
		name := m.projects[m.activeProj]
		if err := m.backend.RemoveProject(name); err != nil {
			m.mode = ModeList
			return m.flashError(err)
		}
		m.refreshProjects()
		m.cursor = 0
		m.refreshSessions()
		m.mode = ModeList
		m.flash = "removed project " + name
		m.flashTime = time.Now()
		return m, nil
	case "n", "esc":
		m.mode = ModeList
	}
	return m, nil
}

func (m *Model) flashError(err error) (tea.Model, tea.Cmd) {
	m.flash = "error: " + err.Error()
	m.flashTime = time.Now()
	return m, nil
}
