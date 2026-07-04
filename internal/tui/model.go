package tui

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/prompt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// Backend is everything the TUI calls into. main wires the real impl;
// tests can supply fakes.
type Backend interface {
	CreateSession(project, name, agent string) (session.Session, error)
	OpenSession(id string) error
	DeleteSession(id string) error
	KillTmux(id string) error
	SetSessionTags(id, ticket, pr string) (session.Session, error)
	// TmuxAliveAll returns id→alive for every stored session using a single
	// tmux list-sessions call instead of N has-session calls.
	TmuxAliveAll() map[string]bool
	Sessions() []session.Session
	Projects() []string
	AddProject(name string, p config.Project) error
	InitProjectAndAdd(name string, p config.Project) error
	AddPlainProject(name string, p config.Project) error
	RemoveProject(name string) error
}

type Mode int

const (
	ModeList Mode = iota
	ModeNewForm
	ModeConfirmDelete
	ModeNewProject
	ModeConfirmDeleteProject
	ModeProjectInitChoice
	ModeTagForm
)

var agentChoices = []string{"claude", "codex", "opencode"}

const projFormInputCount = 4 // text inputs; focus==4 is the agent selector

type projectForm struct {
	inputs   []textinput.Model
	focus    int
	agentIdx int // index into agentChoices
	err      string
}

type pendingProject struct {
	name string
	p    config.Project
}

type tagForm struct {
	inputs []textinput.Model // [0]=ticket, [1]=PR
	focus  int
}

func newTagForm(ticket, pr string) tagForm {
	mk := func(placeholder, value string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.Width = 48
		ti.CharLimit = 256
		ti.SetValue(value)
		return ti
	}
	tf := tagForm{
		inputs: []textinput.Model{
			mk("ticket url", ticket),
			mk("pr url", pr),
		},
	}
	tf.inputs[0].Focus()
	return tf
}

type Model struct {
	cfg     *config.Config
	backend Backend
	keys    KeyMap

	projects   []string
	activeProj int
	sessions   []session.Session
	cursor     int
	states     map[string]watcher.State
	tmuxAlive  map[string]bool
	prompts    map[string]string
	statusCh   <-chan watcher.Snapshot
	cancelPoll context.CancelFunc

	mode            Mode
	nameInput       textinput.Model
	newFormAgentIdx int // agent selector in the new-session form
	projForm        projectForm
	tagForm         tagForm
	pending         pendingProject
	flash           string
	flashTime       time.Time

	width, height int
}

func New(cfg *config.Config, backend Backend, statusCh <-chan watcher.Snapshot, cancel context.CancelFunc) *Model {
	ti := textinput.New()
	ti.Placeholder = "session name (e.g. hash-password)"
	ti.CharLimit = 64
	ti.Width = 40

	m := &Model{
		cfg:        cfg,
		backend:    backend,
		keys:       DefaultKeyMap(),
		states:     map[string]watcher.State{},
		tmuxAlive:  map[string]bool{},
		prompts:    map[string]string{},
		statusCh:   statusCh,
		cancelPoll: cancel,
		nameInput:  ti,
	}
	for name := range cfg.Projects {
		m.projects = append(m.projects, name)
	}
	sort.Strings(m.projects)
	m.refreshSessions()
	m.refreshTmuxAlive()
	m.refreshPrompts()
	if len(m.projects) == 0 {
		m.mode = ModeNewProject
		m.projForm = newProjectForm()
	}
	return m
}

func (m *Model) refreshPrompts() {
	home, _ := os.UserHomeDir()
	for _, s := range m.backend.Sessions() {
		if p := m.prompts[s.ID]; p != "" {
			continue
		}
		m.prompts[s.ID] = prompt.ForAgent(home, s.AgentName(), s.WorktreePath)
	}
}

func (m *Model) refreshTmuxAlive() {
	m.tmuxAlive = m.backend.TmuxAliveAll()
}

// effectiveState returns the state to display: if tmux is dead the
// Claude-session JSON is stale and the session is effectively parked.
func (m *Model) effectiveState(s session.Session) watcher.State {
	if !m.tmuxAlive[s.ID] {
		return watcher.Parked
	}
	return m.states[s.WorktreePath]
}

func (m *Model) refreshProjects() {
	m.projects = m.projects[:0]
	for name := range m.cfg.Projects {
		m.projects = append(m.projects, name)
	}
	sort.Strings(m.projects)
	if m.activeProj >= len(m.projects) {
		m.activeProj = 0
	}
}

func newProjectForm() projectForm {
	mk := func(placeholder string, width int) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.Width = width
		ti.CharLimit = 256
		return ti
	}
	pf := projectForm{
		inputs: []textinput.Model{
			mk("name (e.g. eg_system)", 32),
			mk("repo path (e.g. ~/Development/eg_system)", 48),
			mk("base branch (default: main)", 24),
			mk("branch prefix (optional)", 24),
		},
	}
	pf.inputs[0].Focus()
	return pf
}

func (m *Model) refreshSessions() {
	if len(m.projects) == 0 {
		m.sessions = nil
		return
	}
	proj := m.projects[m.activeProj]
	all := m.backend.Sessions()
	out := make([]session.Session, 0, len(all))
	for _, s := range all {
		if s.Project == proj {
			out = append(out, s)
		}
	}
	m.sessions = out
	if m.cursor >= len(m.sessions) {
		if len(m.sessions) == 0 {
			m.cursor = 0
		} else {
			m.cursor = len(m.sessions) - 1
		}
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(listenStatus(m.statusCh), tickFlash())
}

func listenStatus(ch <-chan watcher.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return nil
		}
		return StatusTickMsg{Snap: snap}
	}
}

func tickFlash() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return InfoMsg{When: t} })
}
