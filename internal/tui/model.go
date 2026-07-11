package tui

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/prompt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// Backend is everything the TUI calls into. main wires the real impl;
// tests can supply fakes.
type Backend interface {
	// CreateSession's hint, when non-empty, is a user-facing instruction
	// (e.g. "run: tmux attach -t ...") to show alongside success — it is
	// not an error.
	CreateSession(project, name, agent, existingBranch, ticket string) (s session.Session, hint string, err error)
	OpenSession(id string) (hint string, err error)
	DeleteSession(id string) error
	KillTmux(id string) error
	SetSessionTags(id, ticket, pr string) (session.Session, error)
	MoveSession(id string, delta int) error
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
	branchInput     textinput.Model
	ticketInput     textinput.Model
	newFormFocus    int // 0=nameInput, 1=branchInput, 2=ticketInput
	newFormAgentIdx int // agent selector in the new-session form
	projForm        projectForm
	tagForm         tagForm
	pending         pendingProject
	flash           string
	flashKind       string // "info" or "error"
	flashTime       time.Time
	busy            bool // true while a background op (e.g. session create) is in flight; suppresses flash expiry

	width, height int

	linkHits []resolvedLinkHit
}

// resolvedLinkHit is a linkHit translated into absolute terminal
// coordinates, computed fresh on every View() call and consulted by the
// mouse handler in Update() to resolve a click to a session's ticket/PR URL.
type resolvedLinkHit struct {
	sessionID string
	url       string
	y         int
	x0, x1    int // half-open column range
}

// updateLinkHits recomputes m.linkHits in absolute terminal coordinates
// from the list-local hits produced by renderList. It's a no-op (clearing
// hits) outside ModeList, since the list isn't clickable behind an overlay.
func (m *Model) updateLinkHits(header string, hits []linkHit) {
	if m.mode != ModeList {
		m.linkHits = nil
		return
	}
	originX := panelBorder.GetBorderLeftSize() + panelBorder.GetPaddingLeft()
	originY := lipgloss.Height(header) + panelBorder.GetBorderTopSize()
	m.linkHits = m.linkHits[:0]
	for _, h := range hits {
		m.linkHits = append(m.linkHits, resolvedLinkHit{
			sessionID: h.sessionID,
			url:       h.url,
			y:         originY + h.line,
			x0:        originX + h.col0,
			x1:        originX + h.col1,
		})
	}
}

// linkAt returns the URL of the ticket/PR icon at absolute terminal
// coordinates (x, y), if any.
func (m *Model) linkAt(x, y int) string {
	for _, h := range m.linkHits {
		if y == h.y && x >= h.x0 && x < h.x1 {
			return h.url
		}
	}
	return ""
}

func New(cfg *config.Config, backend Backend, statusCh <-chan watcher.Snapshot, cancel context.CancelFunc) *Model {
	ti := textinput.New()
	ti.Placeholder = "session name (optional if branch set)"
	ti.CharLimit = 64
	ti.Width = 40

	bi := textinput.New()
	bi.Placeholder = "existing branch (optional)"
	bi.CharLimit = 128
	bi.Width = 40

	tki := textinput.New()
	tki.Placeholder = "ticket url (optional)"
	tki.CharLimit = 256
	tki.Width = 40

	m := &Model{
		cfg:         cfg,
		backend:     backend,
		keys:        DefaultKeyMap(),
		states:      map[string]watcher.State{},
		tmuxAlive:   map[string]bool{},
		prompts:     map[string]string{},
		statusCh:    statusCh,
		cancelPoll:  cancel,
		nameInput:   ti,
		branchInput: bi,
		ticketInput: tki,
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

// refreshStatusCmd returns a tea.Cmd that computes the tmux-alive map and
// missing prompts off the Bubble Tea event-loop goroutine. It must not
// mutate m — only Update may mutate model state. m.prompts is also written
// concurrently by Update, so we snapshot the keys we already know about here
// (on the caller's goroutine) rather than reading m.prompts from the
// returned closure, which would race.
func refreshStatusCmd(m *Model) tea.Cmd {
	backend := m.backend
	known := make(map[string]string, len(m.prompts))
	for id, p := range m.prompts {
		known[id] = p
	}

	return func() tea.Msg {
		tmuxAlive := backend.TmuxAliveAll()

		home, _ := os.UserHomeDir()
		prompts := make(map[string]string)
		for _, s := range backend.Sessions() {
			if p := known[s.ID]; p != "" {
				continue
			}
			prompts[s.ID] = prompt.ForAgent(home, s.AgentName(), s.WorktreePath)
		}

		return StatusRefreshedMsg{TmuxAlive: tmuxAlive, Prompts: prompts}
	}
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
			return StatusChannelClosedMsg{}
		}
		return StatusTickMsg{Snap: snap}
	}
}

func tickFlash() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return InfoMsg{When: t} })
}

const (
	infoFlashDuration  = 3 * time.Second
	errorFlashDuration = 8 * time.Second
)

func (m *Model) setFlash(kind, text string) {
	m.flash = text
	m.flashKind = kind
	m.flashTime = time.Now()
}

func (m *Model) setError(err error) {
	m.setFlash("error", err.Error())
}
