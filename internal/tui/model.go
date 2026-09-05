package tui

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/browser"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/prompt"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/updatecheck"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// Backend is everything the TUI calls into. main wires the real impl;
// tests can supply fakes.
type Backend interface {
	// CreateSession's hint, when non-empty, is a user-facing instruction
	// (e.g. "run: tmux attach -t ...") to show alongside success — it is
	// not an error.
	// dangerous is a pointer so the TUI can leave it unset to mean "use the
	// project's own default"; in practice it always computes and passes an
	// explicit value (see updateNewForm's Enter-key handling in update.go),
	// same as before this was a pointer.
	CreateSession(project, name, agent, existingBranch, ticket string, openTerminal bool, dangerous *bool, baseBranch, model, thinking string) (s session.Session, hint string, err error)
	// StartFirstPrompt waits for a freshly created session's agent pane to
	// be ready, then types prompt into it, and — if autoSubmit is true —
	// presses Enter to start the agent working on it. No-op if prompt is
	// empty.
	StartFirstPrompt(tmuxSession, prompt string, autoSubmit bool) error
	OpenSession(id string) (hint string, err error)
	DeleteSession(id string) (hint string, err error)
	// WorktreeStatus reports id's worktree as dirty/unpushed; ok is false if
	// status can't be determined (unknown session, or not a git repo).
	WorktreeStatus(id string) (dirty, unpushed, ok bool)
	// ChangeSummary reports id's worktree change counts (files with
	// uncommitted changes, commits unpushed) for the delete dialog's detail
	// line; ok is false if it can't be determined.
	ChangeSummary(id string) (filesChanged, unpushedCommits int, ok bool)
	// PRStatus reports the merge/CI status of id's attached PR; ok is false
	// if the session has no PR attached or the lookup fails.
	PRStatus(id string) (info prstatus.Info, ok bool)
	KillTmux(id string) error
	// SetSessionStatusTitle renames id's tmux window to reflect st, so
	// terminals tracking the window name as their tab title show it live.
	SetSessionStatusTitle(id string, st watcher.State) error
	SetSessionTags(id, ticket, pr string) (session.Session, error)
	SetSessionPrompt(id, prompt string) (session.Session, error)
	SetSessionAgent(id, agent string, dangerous bool) (session.Session, error)
	// RenameSession changes id's display name and its live tmux session name.
	RenameSession(id, newName string) (session.Session, error)
	// SetSessionArchived hides (or restores) a session from the default
	// list without touching its tmux session or worktree.
	SetSessionArchived(id string, archived bool) (session.Session, error)
	MoveSession(id string, delta int) error
	MoveProject(name string, delta int) error
	// TmuxAliveAll returns id→alive for every stored session using a single
	// tmux list-sessions call instead of N has-session calls.
	TmuxAliveAll() map[string]bool
	Sessions() []session.Session
	Projects() []string
	AddProject(name string, p config.Project) error
	InitProjectAndAdd(name string, p config.Project) error
	AddPlainProject(name string, p config.Project) error
	UpdateProject(name string, p config.Project) error
	RemoveProject(name string) error
	// SetTheme persists the chosen theme name and appearance override
	// ("light"/"dark"/"" for auto) to config.
	SetTheme(theme, appearance string) error
	// SetAutoSubmitDefault persists the remembered default for the
	// new-session form's auto-submit toggle.
	SetAutoSubmitDefault(autoSubmit bool) error
	// SetSortRecentFirst persists the session list's sort mode.
	SetSortRecentFirst(recentFirst bool) error
	// SetAutoTmux persists whether moomux always relaunches itself inside a
	// dedicated tmux session on startup.
	SetAutoTmux(autoTmux bool) error
	// SetCompactDetail persists whether the detail panel trims itself to the
	// fields most useful at a glance.
	SetCompactDetail(compact bool) error
	// ConfigSnapshot returns the backend's current config. Only ever call
	// this from inside a tea.Cmd closure, after the mutation it's reporting
	// on — never from Update()/View() directly (it may do I/O, e.g. an IPC
	// round trip). The result is handed back via a Msg's Cfg field for
	// Update() to apply with *m.cfg = *msg.Cfg — see ProjectAddedMsg and
	// friends in messages.go for why the model never touches the backend's
	// live config directly.
	ConfigSnapshot() config.Config
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
	ModeHelp
	ModeEditSession
	ModeEditProject
	ModeProjectPicker
	ModeThemePicker
	ModeMultiView
	ModeSearch
	ModeSettings
)

// agentNames lists the agent CLIs a session/project can run, in the core's
// order. Every form pairs a selector over this list with its own independent
// "dangerous" toggle row, rather than a single combined "codex (dangerous)"
// -style list — opencode has no permission-skipping flag (see dangerousFlag
// in internal/app), so its toggle is simply a no-op.
//
// Sourced from m.agentOptions, fetched from the backend once at startup
// (App.AgentOptions, served remotely by internal/ipc) — this used to be a
// hardcoded copy here that could silently drift from what the core actually
// launches; now the TUI and any other front end read the same table.
func (m *Model) agentNames() []string {
	if len(m.agentOptions) == 0 {
		// A Backend (e.g. an IPC front end) that returns no options at all
		// would otherwise leave every index/modulo against this list
		// (newFormAgentIdx and friends) dividing by zero.
		return []string{"claude"}
	}
	names := make([]string, len(m.agentOptions))
	for i, o := range m.agentOptions {
		names[i] = o.Name
	}
	return names
}

// modelNamesFor returns agent's model choices, defaulting to claude's list
// for an unrecognized agent (or one with none of its own) so the selector
// always has at least "default". Never called for "opencode" — its form row
// is a free-text input (newFormModelInput) instead of a selector, since it
// has no small fixed model list worth hardcoding.
func (m *Model) modelNamesFor(agent string) []string {
	var fallback []string
	for _, o := range m.agentOptions {
		if o.Name == agent && len(o.Models) > 0 {
			return o.Models
		}
		if o.Name == "claude" {
			fallback = o.Models
		}
	}
	return fallback
}

// thinkingNamesFor returns agent's thinking/reasoning-level choices. Claude
// and opencode share a list of prompt phrases prepended to the first prompt
// (see thinkingPromptPrefix in update.go) — neither has a launch-time
// reasoning-effort flag; codex's are real values for its
// -c model_reasoning_effort flag (see reasoningEffortFlag in internal/app).
// "default" always means "pass/prepend nothing".
func (m *Model) thinkingNamesFor(agent string) []string {
	var fallback []string
	for _, o := range m.agentOptions {
		if o.Name == agent {
			return o.Thinking
		}
		if o.Name == "claude" {
			fallback = o.Thinking
		}
	}
	return fallback
}

// agentNameIndex finds agent's position in agentNames, defaulting to 0
// (claude) if it's unrecognized.
func (m *Model) agentNameIndex(agent string) int {
	for i, a := range m.agentNames() {
		if a == agent {
			return i
		}
	}
	return 0
}

// askAgentIdx is a sentinel projectForm.agentIdx value, one past the real
// agentNames, selected as an extra "ask each time" entry in the project
// agent selector — it maps to config.Project.PromptAgent instead of Agent.
const askAgentIdx = -1

// projFormInputCount is the number of plain text inputs in the project form
// (name, repo, base branch, branch prefix). Non-text controls follow at
// fixed offsets from it: focus==projFormInputCount is the emoji selector,
// +1 the agent selector, +2 the dangerous toggle, +3 the worktree toggle.
const projFormInputCount = 4

type projectForm struct {
	inputs   []textinput.Model
	focus    int
	emojiIdx int // index into emojiChoices; 0 is the "auto" (deterministic pick) sentinel
	// emojiChoices is normally projectEmojiChoices, but editProjectForm
	// inserts the project's existing Emoji as its own entry when it isn't
	// one of projectEmojiPalette's glyphs (e.g. hand-edited into the TOML
	// config) — otherwise it would be indistinguishable from "auto" and
	// saving any unrelated field would silently discard it.
	emojiChoices []string
	agentIdx     int  // index into agentNames, or askAgentIdx for "ask each time"
	dangerous    bool // whether the chosen agent runs with its permission-skipping flag; meaningless when agentIdx is askAgentIdx
	noWorktree   bool
	err          string
}

type pendingProject struct {
	name string
	p    config.Project
}

type tagForm struct {
	inputs []textinput.Model // [0]=ticket, [1]=PR
	focus  int
}

// sessionFormNameFocus, sessionFormAgentFocus, and sessionFormDangerousFocus
// are the sessionForm.focus values for the edit-session form's controls.
const (
	sessionFormNameFocus      = 0
	sessionFormAgentFocus     = 1
	sessionFormDangerousFocus = 2
)

type sessionForm struct {
	id        string
	project   string
	nameInput textinput.Model
	agentIdx  int
	dangerous bool
	focus     int // sessionFormNameFocus, sessionFormAgentFocus, or sessionFormDangerousFocus
	err       string
}

func newSessionForm(id, project, name string, agentIdx int, dangerous bool) sessionForm {
	ni := textinput.New()
	ni.CharLimit = 256
	ni.SetValue(name)
	ni.Focus()
	return sessionForm{
		id:        id,
		project:   project,
		nameInput: ni,
		agentIdx:  agentIdx,
		dangerous: dangerous,
	}
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
	// agentOptions is the core's agent/model/thinking-level tables, fetched
	// once at startup (see New) — the single source every form's agent,
	// model and thinking selectors read from instead of a copy of their own.
	agentOptions []config.AgentOption
	keys         KeyMap
	// Version is shown in the bottom-right corner of the footer; empty hides it.
	Version string
	// UpdateVersion is the latest GitHub release, set by checkUpdateCmd once
	// it resolves; empty unless it's newer than Version.
	UpdateVersion string
	// updating is true while runUpdateCmd's `brew upgrade` is in flight, so
	// a repeat press of Update doesn't shell out twice concurrently.
	updating bool
	// Relaunch is set right before quitting once an update installs
	// successfully. main() checks it after p.Run() returns and, if set,
	// execs the freshly-installed binary in place of this process.
	Relaunch bool

	projects     []string
	activeProj   int
	sessions     []session.Session
	showArchived bool // when true, the list shows archived sessions instead of active ones
	cursor       int
	states       map[string]watcher.State
	titleState   map[string]watcher.State // last status pushed as each session's tmux window title, by session id
	tmuxAlive    map[string]bool
	// tmuxCheckedOnce becomes true once the first (async, startup) tmux-alive
	// check resolves. Gates the startup fetchStaleGitStatusCmd call so it
	// runs exactly once with real data — tmuxAlive is empty/unknown until
	// then, and every subsequent StatusRefreshedMsg is just the routine 2s
	// refresh (which doesn't need its own git-status check; the regular
	// StatusTickMsg handler already does one every tick).
	tmuxCheckedOnce bool
	gitStatus       map[string]gitStatusInfo // by session id; see gitStatusStaleAfter for the refresh policy
	// gitStatusPending marks session ids with a fetchGitStatusCmd currently
	// in flight, so staleGitStatusIDs doesn't pile up duplicate concurrent
	// fetches for a session whose `git status` call is just running long.
	gitStatusPending map[string]bool
	prStatus         map[string]prStatusInfo // by session id, only populated for sessions with a PR attached; see prStatusStaleAfter
	// prStatusPending mirrors gitStatusPending for fetchPRStatusCmd.
	prStatusPending map[string]bool
	prompts         map[string]string
	// promptCheckedAt records when each session was last scanned for its
	// first prompt, so a session whose prompt can't be found isn't
	// re-scanned on every single status tick — see promptRetryAfter.
	promptCheckedAt map[string]time.Time
	statusCh        <-chan watcher.Snapshot
	cancelPoll      context.CancelFunc

	// sessCache memoizes backend.Sessions() for the duration of one Update
	// or one View pass — see allSessions.
	sessCache      []session.Session
	sessCacheValid bool

	mode                    Mode
	nameInput               textinput.Model
	branchInput             textinput.Model
	baseBranchInput         textinput.Model
	ticketInput             textinput.Model
	prInput                 textinput.Model
	promptInput             textarea.Model
	newFormModelInput       textinput.Model // opencode's free-text model field; unused for claude/codex
	newFormFocus            int             // 0=project selector, 1=nameInput, 2=branchInput, 3=baseBranchInput, 4=promptInput, 5=ticketInput, 6=prInput, 7=agent selector, 8=model selector, 9=thinking selector, 10=dangerous toggle, 11=open-terminal toggle, 12=auto-submit toggle
	newFormErr              string
	newFormAgentIdx         int  // index into agentNames; -1 means "not chosen yet"
	newFormModelIdx         int  // index into modelNamesFor(chosen agent); 0 ("default") initially, reset on agent change
	newFormThinkingIdx      int  // index into thinkingNamesFor(chosen agent); 0 ("default") initially, reset on agent change
	newFormDangerous        bool // whether to run the chosen agent with its permission-skipping flag; off by default
	newFormProjIdx          int  // project selector in the new-session form; index into m.projects
	newFormOpenInBackground bool // whether to skip opening a terminal window for the new session; off by default
	newFormAutoSubmit       bool // whether to press Enter after typing the first prompt into the pane; off by default
	projForm                projectForm
	sessionForm             sessionForm
	editProjectName         string
	tagForm                 tagForm
	pickerCursor            int // index into m.projects while ModeProjectPicker is open
	searchInput             textinput.Model
	// searchResults is the flattened, filtered session list (every project,
	// active + archived) while ModeSearch is open, recomputed on every
	// keystroke by refreshSearchResults. searchCursor indexes into it.
	searchResults []session.Session
	searchCursor  int
	// confirmGit starts out as whatever's cached in m.gitStatus (possibly
	// stale or empty) the instant ModeConfirmDelete opens, so the dialog
	// never pauses. confirmChecking is true while a fresh fetchGitStatusCmd
	// for the session is in flight; the dialog shows a small loading note
	// until it resolves and confirmGit is updated for real (see GitStatusMsg
	// in update.go). confirmAck becomes true once the user has pressed y
	// through a dirty/unpushed warning once; a second y then actually deletes.
	confirmGit      gitStatusInfo
	confirmChecking bool
	confirmAck      bool
	// confirmSummary is the file/commit-count detail for the warning line in
	// the delete dialog, fetched alongside confirmGit by the same key press
	// (see fetchChangeSummaryCmd). It has no "checking" gate of its own —
	// confirmGit alone decides whether the dialog warns and whether a second
	// y is required; this only enriches that warning's text once it arrives.
	confirmSummary changeSummary
	// themeCursor is an index into themeNames while ModeThemePicker is open.
	// previewAppearance holds the appearance being live-previewed there
	// ("", "light", or "dark") until Enter persists it or Esc reverts to
	// m.cfg.Appearance — cfg itself isn't touched until the user confirms.
	themeCursor       int
	previewAppearance string
	// settingsCursor is an index into settingsRows while ModeSettings is open.
	settingsCursor int
	// projectDialogReturn is where ModeConfirmDeleteProject/ModeEditProject/
	// ModeNewProject send the user back to (cancel, error, or success) —
	// ModeList when P/D/E was pressed there, ModeProjectPicker when
	// p/d/e was pressed in the picker. Also read by finishProjectAdded to
	// decide whether a successful add returns to the picker or opens the
	// new-session form.
	projectDialogReturn Mode
	// sessionDialogReturn is where a session-level dialog (new/delete/tag/
	// edit-session, help) sends the user back to once it closes — ModeList
	// normally, or ModeMultiView when it was opened from there (see
	// updateMultiView's delegation to updateList). Mirrors
	// projectDialogReturn's role for the project dialogs.
	sessionDialogReturn Mode
	pending             pendingProject
	flash               string
	flashKind           string // "info" or "error"
	flashTime           time.Time
	busy                bool // true while a background op (e.g. session create) is in flight; suppresses flash expiry
	// forceCopyLinks overrides browser.Remote()'s auto-detection, forcing
	// ticket/PR clicks to copy instead of open. Auto-detection has no
	// signal at all for transports like mosh that don't set SSH_TTY/
	// SSH_CONNECTION/SSH_CLIENT, so this lets a user force the behavior
	// from inside the running session instead of needing shell/env access
	// on the host. Toggled with R; there's no "force open" counterpart —
	// if auto-detection isn't already saying remote, links just open.
	forceCopyLinks bool

	// multiFocus is a global index into m.projects — which project Tab/
	// Shift-Tab and Up/Down/Open act on while ModeMultiView is active. It can
	// point outside the currently visible window (see multiOffset); that's
	// exactly what tells ensureMultiFocusVisible to slide the window.
	multiFocus int
	// multiOffset is the index of the first project shown in ModeMultiView's
	// side-by-side panels — panning this is how focus moving past either
	// edge of the visible window "slides" the view instead of the focused
	// project just going off-screen.
	multiOffset int
	// multiCursors is each project's selected row within its own panel in
	// ModeMultiView, keyed by project name so it survives switching focus
	// away and back. Unlike m.cursor (a single index into the one active
	// project's m.sessions), multi-view shows several projects' lists at
	// once and each needs its own independent selection.
	multiCursors map[string]int
	// multiPinned is a project name multiViewEligibleProjects() shows even
	// though it wouldn't otherwise qualify — set when the project picker
	// jumps to a project with nothing to show yet (see updateProjectPicker),
	// so multi-view actually lands on it instead of leaving it invisible.
	// Cleared as soon as focus moves elsewhere (Tab/Shift-Tab); it's a
	// one-shot "show me this one," not a standing exception.
	multiPinned string

	width, height int

	linkHits        []resolvedLinkHit
	rowHits         []resolvedRowHit
	panelHits       []resolvedPanelHit
	overlayViewport viewport.Model
	overlayMode     Mode
	overlayFocus    int
}

// resolvedLinkHit is a linkHit translated into absolute terminal
// coordinates, computed fresh on every View() call and consulted by the
// mouse handler in Update() to resolve a click to a session's ticket/PR URL.
type resolvedLinkHit struct {
	sessionID string
	url       string
	copyOnly  bool
	y         int
	x0, x1    int // half-open column range
}

// resolvedRowHit is a rowHit translated into absolute terminal coordinates,
// computed fresh on every View() call and consulted by the mouse handler in
// Update() to resolve a click anywhere on a session's row (not just its
// ticket/PR icons) to that session.
type resolvedRowHit struct {
	sessionID string
	y         int
	x0, x1    int // half-open column range
}

// resolvedPanelHit records one ModeMultiView panel's full rendered
// rectangle (border included) in absolute terminal coordinates, computed
// fresh by renderMultiView on every View() call. It's the fallback the mouse
// handler in Update() consults when a click doesn't land on a link or a
// session row — e.g. the detail pane, the panel's title line, or empty
// space below a short list — so clicking anywhere in a project's panel
// picks that project, not just its session rows.
type resolvedPanelHit struct {
	project string
	x0, x1  int // half-open column range
	y0, y1  int // half-open row range
}

// panelAt returns the project whose panel rectangle contains absolute
// terminal coordinates (x, y), if any.
func (m *Model) panelAt(x, y int) (string, bool) {
	for _, h := range m.panelHits {
		if x >= h.x0 && x < h.x1 && y >= h.y0 && y < h.y1 {
			return h.project, true
		}
	}
	return "", false
}

// updateLinkHits recomputes m.linkHits and m.rowHits in absolute terminal
// coordinates from the list- and detail-local hits produced during
// rendering. It's a no-op (clearing hits) outside ModeList and ModeMultiView,
// since panels aren't clickable behind an overlay. ModeMultiView only ever
// reaches here via renderListView's own single-project fallback (see
// renderMultiView) — its actual multi-panel layout computes and appends its
// own hits directly (one origin per panel) and never calls this. Either way
// m.panelHits (the real multi-panel layout's own project-rectangle hits) is
// cleared here too — it's stale outside that layout, e.g. right after a
// resize drops down to a single panel.
func (m *Model) updateLinkHits(header string, listHits, detailHits []linkHit, detailX, detailY int, listRows []rowHit, listWidth int) {
	m.panelHits = nil
	if m.mode != ModeList && m.mode != ModeMultiView {
		m.linkHits = nil
		m.rowHits = nil
		return
	}
	m.linkHits = m.linkHits[:0]
	appendHits := func(hits []linkHit, originX, originY int) {
		for _, h := range hits {
			m.linkHits = append(m.linkHits, resolvedLinkHit{
				sessionID: h.sessionID,
				url:       h.url,
				copyOnly:  h.copyOnly,
				y:         originY + h.line,
				x0:        originX + h.col0,
				x1:        originX + h.col1,
			})
		}
	}
	listX := panelBorder.GetBorderLeftSize() + panelBorder.GetPaddingLeft()
	listY := lipgloss.Height(header) + panelBorder.GetBorderTopSize()
	appendHits(listHits, listX, listY)
	appendHits(detailHits, detailX, detailY)

	m.rowHits = m.rowHits[:0]
	for _, r := range listRows {
		m.rowHits = append(m.rowHits, resolvedRowHit{
			sessionID: r.sessionID,
			y:         listY + r.line,
			x0:        listX,
			x1:        listX + listWidth,
		})
	}
}

// sessionRowAt returns the session ID whose row contains absolute terminal
// coordinates (x, y), for turning a mouse click anywhere on a row (mobile
// clients can't easily land a tap on the exact cursor column) into a
// selection.
func (m *Model) sessionRowAt(x, y int) (string, bool) {
	for _, h := range m.rowHits {
		if y == h.y && x >= h.x0 && x < h.x1 {
			return h.sessionID, true
		}
	}
	return "", false
}

// isRemote decides whether a ticket/PR icon click should copy the URL
// (true) or open it in a browser (false), honoring the user's R toggle
// before falling back to browser.Remote()'s SSH auto-detection.
func (m *Model) isRemote() bool {
	return m.forceCopyLinks || browser.Remote()
}

// linkAt returns the URL of the ticket/PR/tmux icon at absolute terminal
// coordinates (x, y), if any, and whether it must always be copied rather
// than opened in a browser (true for non-URL text like a tmux command).
func (m *Model) linkAt(x, y int) (string, bool) {
	for _, h := range m.linkHits {
		if y == h.y && x >= h.x0 && x < h.x1 {
			return h.url, h.copyOnly
		}
	}
	return "", false
}

func New(cfg *config.Config, backend Backend, agentOptions []config.AgentOption, statusCh <-chan watcher.Snapshot, cancel context.CancelFunc) *Model {
	ti := textinput.New()
	ti.Placeholder = "session name (optional if branch set)"
	ti.CharLimit = 64
	ti.Width = 40

	bi := textinput.New()
	bi.Placeholder = "existing branch (optional)"
	bi.CharLimit = 128
	bi.Width = 40

	bbi := textinput.New()
	bbi.Placeholder = "base branch (optional, defaults to project's)"
	bbi.CharLimit = 128
	bbi.Width = 40

	tki := textinput.New()
	tki.Placeholder = "ticket url (optional)"
	tki.CharLimit = 256
	tki.Width = 40

	pri := textinput.New()
	pri.Placeholder = "PR url (optional)"
	pri.CharLimit = 256
	pri.Width = 40

	// opencode has no fixed model list of its own worth hardcoding — it's a
	// free-text field instead of a selector like modelNamesByAgent's other
	// entries.
	mi := textinput.New()
	mi.Placeholder = "model (optional, e.g. anthropic/claude-sonnet-4-5)"
	mi.CharLimit = 128
	mi.Width = 40

	si := textinput.New()
	si.Placeholder = "session name"
	si.CharLimit = 64
	si.Width = 40

	pi := textarea.New()
	pi.Placeholder = "first prompt (optional)"
	pi.CharLimit = 4096
	pi.ShowLineNumbers = false
	pi.Prompt = "> "
	pi.SetHeight(4)
	pi.SetWidth(40)
	// "shift+enter" can't be told apart from plain "enter" over a raw
	// terminal (no kitty keyboard protocol here), so binding it as a
	// separate newline key silently submitted the form instead — see
	// updateNewForm's Enter case, which only treats Enter as a global
	// submit outside this field.
	pi.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("enter", "ctrl+j", "ctrl+m"), key.WithHelp("enter", "newline"))

	// own decouples m.cfg from cfg: locally cfg is the same *config.Config
	// App itself mutates from tea.Cmd goroutines (see App.Cfg's doc
	// comment), and over the socket ipc.Client used to keep it refreshed in
	// place from a Cmd goroutine too — either way, Update()/View() read
	// m.cfg's fields unlocked from the event-loop goroutine, so the model
	// must own memory nothing else ever writes. Every config-mutating Cmd
	// instead hands Update() a fresh snapshot via a Msg's Cfg field (see
	// ProjectAddedMsg and friends), applied with *m.cfg = *msg.Cfg.
	own := cfg.Clone()
	m := &Model{
		cfg:               &own,
		backend:           backend,
		agentOptions:      agentOptions,
		keys:              DefaultKeyMap(),
		states:            map[string]watcher.State{},
		titleState:        map[string]watcher.State{},
		tmuxAlive:         map[string]bool{},
		gitStatus:         map[string]gitStatusInfo{},
		gitStatusPending:  map[string]bool{},
		prStatus:          map[string]prStatusInfo{},
		prStatusPending:   map[string]bool{},
		prompts:           map[string]string{},
		promptCheckedAt:   map[string]time.Time{},
		statusCh:          statusCh,
		cancelPoll:        cancel,
		nameInput:         ti,
		branchInput:       bi,
		baseBranchInput:   bbi,
		ticketInput:       tki,
		prInput:           pri,
		promptInput:       pi,
		newFormModelInput: mi,
		searchInput:       si,
		overlayViewport:   viewport.New(1, 1),
		overlayMode:       ModeList,
		overlayFocus:      -1,
		multiCursors:      map[string]int{},
	}
	m.projects = cfg.OrderedProjectNames()
	// Land on the first project with active sessions rather than always
	// project 0 — a config-order project with nothing going on isn't a
	// useful thing to open on by default when another one has active work.
	for i, name := range m.projects {
		if m.projectHasSessions(name) {
			m.activeProj = i
			break
		}
	}
	m.refreshSessions()
	// tmuxAlive is deliberately left empty here — populated asynchronously by
	// Init()'s refreshStatusCmd instead of a synchronous TmuxAliveAll() call,
	// so a slow or wedged tmux server can't block the first render. Until
	// that resolves, effectiveState's "no entry = not alive" default reads
	// every session as Parked, which self-corrects within one render once
	// the real check lands.
	m.refreshPrompts()
	if len(m.projects) == 0 {
		m.mode = ModeNewProject
		m.projForm = newProjectForm()
	} else {
		// Multi-view is the primary view now — it already collapses to the
		// classic single-project layout whenever only one project would show
		// (see renderMultiView), so this costs nothing when there's just one
		// project and surfaces the rest at a glance when there's more.
		m.mode = ModeMultiView
		if idx := indexOfProject(m.multiViewEligibleProjects(), m.projects[m.activeProj]); idx >= 0 {
			m.multiFocus = idx
		}
	}
	return m
}

func (m *Model) refreshPrompts() {
	home, _ := os.UserHomeDir()
	for _, s := range m.allSessions() {
		if p := m.prompts[s.ID]; p != "" {
			continue
		}
		m.prompts[s.ID] = prompt.ForAgent(home, s.AgentName(), s.WorktreePath)
	}
}

// pruneDeadSessions drops per-session bookkeeping for sessions that no
// longer exist. m.states is keyed by worktree path and the rest by session
// id, but they rot the same way: without this, every map here grows for the
// life of the process, keeping entries (including whole prompt strings) for
// every session ever deleted.
func (m *Model) pruneDeadSessions() {
	all := m.allSessions()
	livePaths := make(map[string]bool, len(all))
	liveIDs := make(map[string]bool, len(all))
	for _, s := range all {
		livePaths[s.WorktreePath] = true
		liveIDs[s.ID] = true
	}
	for path := range m.states {
		if !livePaths[path] {
			delete(m.states, path)
		}
	}
	for _, byID := range []map[string]bool{m.gitStatusPending, m.prStatusPending} {
		for id := range byID {
			if !liveIDs[id] {
				delete(byID, id)
			}
		}
	}
	for id := range m.titleState {
		if !liveIDs[id] {
			delete(m.titleState, id)
		}
	}
	for id := range m.gitStatus {
		if !liveIDs[id] {
			delete(m.gitStatus, id)
		}
	}
	for id := range m.prStatus {
		if !liveIDs[id] {
			delete(m.prStatus, id)
		}
	}
	for id := range m.prompts {
		if !liveIDs[id] {
			delete(m.prompts, id)
		}
	}
	for id := range m.promptCheckedAt {
		if !liveIDs[id] {
			delete(m.promptCheckedAt, id)
		}
	}
}

// allSessions returns backend.Sessions(), memoized for the rest of the
// current Update or View pass.
//
// Both passes ask for the session list many times over — measured at 15
// calls for one View of an eight-project list and 6 more for one
// StatusTickMsg, since every panel-count, eligible-project and per-project
// filter helper fetches it again — and each call is a full sessions.json
// read, unmarshal and sort (57us and 45 KB with 29 sessions), or, on the
// socket-backed backend, its own unix connect and round trip. Nothing
// mutates the backend from the Update goroutine — every mutator runs inside
// a tea.Cmd and reports back as a message — so one snapshot per pass is
// exactly as fresh as re-reading it 21 times.
func (m *Model) allSessions() []session.Session {
	if !m.sessCacheValid {
		m.sessCache = m.backend.Sessions()
		m.sessCacheValid = true
	}
	return m.sessCache
}

// invalidateSessions drops the memoized snapshot. Called at the top of both
// Update and View, so each pass reads the backend at most once but never
// carries a snapshot across passes.
func (m *Model) invalidateSessions() { m.sessCacheValid = false }

// promptRetryAfter bounds how often a session with no discoverable first
// prompt is re-scanned. Without it, every such session re-ran
// prompt.ForAgent on every status tick forever — a line-by-line JSON scan
// of every .jsonl under ~/.claude/projects/<cwd>/, or a sqlite3 subprocess
// per codex/opencode database — because a scan that comes back empty leaves
// m.prompts[id] empty, which is the very condition that selects it for
// scanning. Prompts do appear late (the agent writes its log a moment after
// the session exists), so this backs the retry off rather than giving up.
const promptRetryAfter = 30 * time.Second

// gitStatusStaleAfter bounds how long a cached gitStatusInfo is trusted
// before it's worth a fresh `git status`/`rev-list` call — see
// staleGitStatusIDs. Every session is tracked this way regardless of its
// agent state (working, done, parked, whatever) — long enough that no
// session is re-checked every single 2s tick (those calls can run well past
// 2s), short enough that a session sitting untouched for a while still
// eventually reflects changes made outside moomux (another terminal, an
// editor, a push from elsewhere).
const gitStatusStaleAfter = time.Minute

// gitStatusStaleJitter varies gitStatusStaleThreshold by up to this fraction
// of gitStatusStaleAfter, per session. Without it, every session first
// fetched around the same moment (notably: all of them, at startup) would
// keep coming due for refresh in the same tick forever after — a thundering
// herd of `git status` calls every minute, on the minute, instead of spread
// out.
const gitStatusStaleJitter = 0.2

// gitStatusInfo is a session's git worktree status. ok is false when it
// couldn't be determined (unknown session, not a git repo, or simply not
// fetched yet), in which case dirty/unpushed are meaningless. checkedAt is
// when this was fetched — zero if never — and is what staleGitStatusIDs
// compares against gitStatusStaleThreshold.
type gitStatusInfo struct {
	dirty, unpushed, ok bool
	checkedAt           time.Time
}

// gitStatusStaleThreshold returns id's jittered staleness threshold: a
// deterministic value in [gitStatusStaleAfter*(1-gitStatusStaleJitter),
// gitStatusStaleAfter*(1+gitStatusStaleJitter)], derived from the session id
// so it's stable across repeated calls (never flaps between "stale" and
// "fresh" from call to call) without needing to store anything extra per
// session.
func gitStatusStaleThreshold(id string) time.Duration {
	return jitteredStaleThreshold(id, gitStatusStaleAfter, gitStatusStaleJitter)
}

// jitteredStaleThreshold is the shared math behind gitStatusStaleThreshold
// and prStatusStaleThreshold: a deterministic value in
// [base*(1-jitter), base*(1+jitter)], derived from id so it's stable across
// repeated calls for the same id.
func jitteredStaleThreshold(id string, base time.Duration, jitter float64) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	frac := float64(h.Sum32()) / float64(math.MaxUint32) // deterministic, in [0,1)
	mult := 1 + jitter*(2*frac-1)                        // in [1-J, 1+J]
	return time.Duration(float64(base) * mult)
}

// prStatusStaleAfter bounds how long a cached prStatusInfo is trusted before
// it's worth another `gh pr view` call. Longer than gitStatusStaleAfter since
// a PR's merge/CI status changes less often than a worktree's dirty state,
// and gh hits the network (slower, rate-limited) rather than a local git
// call.
const prStatusStaleAfter = 2 * time.Minute

const prStatusStaleJitter = 0.2

// prStatusInfo is a session's PR status. ok is false when it couldn't be
// determined (no PR attached, gh unavailable, or the PR couldn't be
// resolved), in which case Info is meaningless. checkedAt is when this was
// fetched — zero if never — and is what stalePRStatusIDs compares against
// prStatusStaleThreshold.
type prStatusInfo struct {
	info      prstatus.Info
	ok        bool
	checkedAt time.Time
}

func prStatusStaleThreshold(id string) time.Duration {
	return jitteredStaleThreshold(id, prStatusStaleAfter, prStatusStaleJitter)
}

// refreshStatusCmd returns a tea.Cmd that computes the tmux-alive map and
// missing prompts off the Bubble Tea event-loop goroutine. The returned
// closure must not touch m — only Update may — so the set of sessions to
// scan for a prompt is chosen here, on the caller's goroutine, and passed in
// by value. Selecting them here is also what marks them checked, so a slow
// scan isn't re-issued for the same session on the next tick.
func refreshStatusCmd(m *Model) tea.Cmd {
	backend := m.backend

	type scan struct{ id, agent, path string }
	var toScan []scan
	now := time.Now()
	for _, s := range m.allSessions() {
		if m.prompts[s.ID] != "" {
			continue
		}
		if last, ok := m.promptCheckedAt[s.ID]; ok && now.Sub(last) < promptRetryAfter {
			continue
		}
		m.promptCheckedAt[s.ID] = now
		toScan = append(toScan, scan{id: s.ID, agent: s.AgentName(), path: s.WorktreePath})
	}

	return func() tea.Msg {
		tmuxAlive := backend.TmuxAliveAll()

		home, _ := os.UserHomeDir()
		prompts := make(map[string]string, len(toScan))
		for _, s := range toScan {
			prompts[s.id] = prompt.ForAgent(home, s.agent, s.path)
		}

		return StatusRefreshedMsg{TmuxAlive: tmuxAlive, Prompts: prompts}
	}
}

// fetchGitStatusCmd computes git status for ids off the event-loop goroutine.
// `git status`/`rev-list` can run well past the 2s status-tick interval, so
// callers only pass ids that are actually worth checking right now — see
// staleGitStatusIDs for the routine case, and the Delete key handler for the
// delete-dialog's on-demand single-session check. The returned msg always has
// an entry for every id passed in, even when WorktreeStatus reports ok=false
// — callers (e.g. the delete dialog's "checking..." loader) use that
// presence to tell "resolved, nothing to show" apart from "hasn't resolved
// yet".
// fetchStatusMaxConcurrency caps how many of a status fan-out's per-id
// fetches run at once, so a sweep over many long-lived sessions doesn't
// burst that many concurrent git/gh subprocesses — or, over the IPC
// backend, socket dials — all at the same instant.
const fetchStatusMaxConcurrency = 8

// fetchStatusFanOut runs fetch for each id, at most fetchStatusMaxConcurrency
// at a time, and returns the id->result map once every fetch has completed.
// Shared by fetchGitStatusCmd and fetchPRStatusCmd, which differ only in
// which per-id backend call they make and what they wrap the result in.
func fetchStatusFanOut[T any](ids []string, fetch func(id string) T) map[string]T {
	result := make(map[string]T, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetchStatusMaxConcurrency)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			v := fetch(id)
			mu.Lock()
			result[id] = v
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return result
}

func fetchGitStatusCmd(backend Backend, ids []string) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		status := fetchStatusFanOut(ids, func(id string) gitStatusInfo {
			dirty, unpushed, ok := backend.WorktreeStatus(id)
			return gitStatusInfo{dirty: dirty, unpushed: unpushed, ok: ok, checkedAt: now}
		})
		return GitStatusMsg{Status: status}
	}
}

// changeSummary is the file/commit-count detail for one session's delete
// dialog warning line. ok is false when it couldn't be determined, in which
// case the counts are meaningless and the dialog falls back to its plain
// dirty/unpushed wording.
type changeSummary struct {
	filesChanged, unpushedCommits int
	ok                            bool
}

// fetchChangeSummaryCmd computes id's change summary off the event-loop
// goroutine, for the delete dialog's on-demand detail line. Unlike
// fetchGitStatusCmd's routine per-session polling, this only ever runs once
// per delete-key-press for the one session being deleted.
func fetchChangeSummaryCmd(backend Backend, id string) tea.Cmd {
	return func() tea.Msg {
		files, commits, ok := backend.ChangeSummary(id)
		return ChangeSummaryMsg{ID: id, Summary: changeSummary{filesChanged: files, unpushedCommits: commits, ok: ok}}
	}
}

// staleGitStatusIDs returns every session whose cached git status is missing
// or older than its jittered gitStatusStaleThreshold — regardless of agent
// state, since the point is just to keep the info reasonably fresh for
// whenever it's looked at (the list icons, the delete dialog). Sessions with
// a fetch already in flight (gitStatusPending) are skipped so a slow
// `git status` call doesn't get re-issued for the same session every tick
// until it finally returns.
//
// A Parked session (tmux dead — see effectiveState) is fetched once, the
// same as any other session with no cached status yet, but is then excluded
// from every routine re-check regardless of how stale that cached status
// gets: its worktree has no agent running in it, so dirty/unpushed can't
// change on their own. It starts being re-checked again the moment it stops
// being parked — checkedAt hasn't advanced while parked, so it's already
// past gitStatusStaleThreshold as soon as tmux comes back.
func (m *Model) staleGitStatusIDs() []string {
	var ids []string
	for _, s := range m.allSessions() {
		if m.gitStatusPending[s.ID] {
			continue
		}
		st, ok := m.gitStatus[s.ID]
		if ok && m.effectiveState(s) == watcher.Parked {
			continue
		}
		if !ok || time.Since(st.checkedAt) > gitStatusStaleThreshold(s.ID) {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// fetchStaleGitStatusCmd wraps staleGitStatusIDs as a tea.Cmd, marking each
// selected id pending first so staleGitStatusIDs won't pick it again before
// this fetch resolves. Returns nil if nothing needs checking. Used by both
// Init() (once, at startup — every session is "never checked", so this
// covers all of them) and the StatusTickMsg handler (every ~2s thereafter).
func (m *Model) fetchStaleGitStatusCmd() tea.Cmd {
	ids := m.staleGitStatusIDs()
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		m.gitStatusPending[id] = true
	}
	return fetchGitStatusCmd(m.backend, ids)
}

// fetchPRStatusCmd computes PR status for ids off the event-loop goroutine —
// each is a network-bound `gh pr view` call, mirroring fetchGitStatusCmd.
func fetchPRStatusCmd(backend Backend, ids []string) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		status := fetchStatusFanOut(ids, func(id string) prStatusInfo {
			info, ok := backend.PRStatus(id)
			return prStatusInfo{info: info, ok: ok, checkedAt: now}
		})
		return PRStatusMsg{Status: status}
	}
}

// stalePRStatusIDs returns every session with a PR attached whose cached
// status is missing or older than its jittered prStatusStaleThreshold,
// mirroring staleGitStatusIDs.
func (m *Model) stalePRStatusIDs() []string {
	var ids []string
	for _, s := range m.allSessions() {
		if s.PR == "" || m.prStatusPending[s.ID] {
			continue
		}
		st, ok := m.prStatus[s.ID]
		if !ok || time.Since(st.checkedAt) > prStatusStaleThreshold(s.ID) {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// fetchStalePRStatusCmd wraps stalePRStatusIDs as a tea.Cmd, mirroring
// fetchStaleGitStatusCmd. Returns nil if nothing needs checking.
func (m *Model) fetchStalePRStatusCmd() tea.Cmd {
	ids := m.stalePRStatusIDs()
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		m.prStatusPending[id] = true
	}
	return fetchPRStatusCmd(m.backend, ids)
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
	m.projects = m.cfg.OrderedProjectNames()
	if m.activeProj >= len(m.projects) {
		m.activeProj = 0
	}
	if m.pickerCursor >= len(m.projects) {
		m.pickerCursor = 0
	}
}

// newProjectForm builds the add-project form, pre-filling name/repo from the
// current working directory when it looks usable — the common case is
// running moomux from inside the repo you want to add, and most users don't
// want to type or remember its absolute path.
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
		emojiChoices: projectEmojiChoices,
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "/" {
		pf.inputs[0].SetValue(filepath.Base(cwd))
		pf.inputs[1].SetValue(cwd)
	}
	pf.inputs[0].Focus()
	return pf
}

func (m *Model) editProjectForm(name string, p config.Project) projectForm {
	pf := newProjectForm()
	pf.inputs[0].SetValue(name)
	pf.inputs[1].SetValue(p.Repo)
	pf.inputs[2].SetValue(p.BaseBranch)
	pf.inputs[3].SetValue(p.BranchPrefix)
	pf.emojiIdx = 0
	for i, e := range projectEmojiPalette {
		if e == p.Emoji {
			pf.emojiIdx = i + 1
			break
		}
	}
	if p.Emoji != "" && pf.emojiIdx == 0 {
		// p.Emoji isn't one of the palette's glyphs — keep it as its own
		// selectable entry (right after "auto") instead of collapsing it
		// into the auto sentinel, which would silently discard it the next
		// time this project is saved without touching the emoji field.
		pf.emojiChoices = append([]string{"auto", p.Emoji}, projectEmojiPalette...)
		pf.emojiIdx = 1
	}
	pf.inputs[0].Blur()
	pf.inputs[1].Focus()
	pf.focus = 1
	if p.PromptAgent {
		pf.agentIdx = askAgentIdx
	} else {
		pf.agentIdx = m.agentNameIndex(p.AgentName())
		pf.dangerous = p.Dangerous
	}
	pf.noWorktree = p.NoWorktree
	return pf
}

// projectSessionCount returns how many sessions the active project has,
// archived or not — a project can only be removed once it has none.
func (m *Model) projectSessionCount() int {
	if len(m.projects) == 0 {
		return 0
	}
	return m.projectSessionCountFor(m.projects[m.activeProj])
}

// projectSessionCountFor is projectSessionCount for an arbitrary project
// name, used by the project picker to check a highlighted project that may
// not be the active one.
func (m *Model) projectSessionCountFor(proj string) int {
	n := 0
	for _, s := range m.allSessions() {
		if s.Project == proj {
			n++
		}
	}
	return n
}

// projectHasSessions reports whether the named project has any session
// matching the current archived/active view — used to skip empty projects
// when cycling. It must respect m.showArchived: a project with only
// archived sessions is "empty" while viewing the active list, else cycling
// lands you on a project whose list renders empty anyway.
func (m *Model) projectHasSessions(name string) bool {
	for _, s := range m.allSessions() {
		if s.Project == name && s.Archived == m.showArchived {
			return true
		}
	}
	return false
}

// nextNonEmptyProject returns the index to land on when cycling projects in
// the given direction (+1/-1), skipping projects with no sessions. If every
// other project is empty and the current one has sessions, it stays put. If
// every project including the current one is empty, it takes one step, so
// cycling never gets stuck — that's how you still reach an empty project on
// purpose.
func (m *Model) nextNonEmptyProject(dir int) int {
	n := len(m.projects)
	if n == 0 {
		return 0
	}
	i := m.activeProj
	for step := 0; step < n-1; step++ {
		i = (i + dir + n) % n
		if m.projectHasSessions(m.projects[i]) {
			return i
		}
	}
	if m.projectHasSessions(m.projects[m.activeProj]) {
		return m.activeProj
	}
	return (m.activeProj + dir + n) % n
}

// archivedCount returns how many archived sessions the active project has.
func (m *Model) archivedCount() int {
	if len(m.projects) == 0 {
		return 0
	}
	proj := m.projects[m.activeProj]
	n := 0
	for _, s := range m.allSessions() {
		if s.Project == proj && s.Archived {
			n++
		}
	}
	return n
}

// focusSession moves the cursor onto id, if it's in the current list. A
// no-op otherwise, which is what callers want after a refresh that may have
// filtered the session out (archived, deleted, or now in another project).
func (m *Model) focusSession(id string) {
	for i, s := range m.sessions {
		if s.ID == id {
			m.cursor = i
			return
		}
	}
}

// neighborSessionID returns the ID of whichever session should take over the
// selection once the one at m.cursor drops out of the current view (deleted,
// or archived/restored past the showArchived filter) — whichever is directly
// below it, or above if it was last. Call it at the moment the delete/archive
// actually fires (before the mutation's own async result arrives), and thread
// the result through the resulting message (SessionDeletedMsg.NextID /
// SessionArchivedMsg.NextID) rather than recomputing it once that message
// lands: killing a session's tmux pane can flip its tmux-alive state, which
// resorts the live list on its own, independently of the deletion itself.
// refreshSessions' own selectedID-preserving logic can't anchor through that
// resort because the previously-selected session is the one disappearing —
// so without pinning the neighbor up front, the cursor can visibly hop twice
// for one delete: once from the tmux-alive resort, once from the list
// shrinking.
func (m *Model) neighborSessionID() string {
	if m.cursor < 0 || m.cursor >= len(m.sessions) {
		return ""
	}
	if m.cursor+1 < len(m.sessions) {
		return m.sessions[m.cursor+1].ID
	}
	if m.cursor-1 >= 0 {
		return m.sessions[m.cursor-1].ID
	}
	return ""
}

// refreshSessionsFocusing refreshes the session list and, if nextID is set,
// lands the cursor there instead of wherever refreshSessions' own
// (deleted/archived-session-relative) fallback would leave it. nextID should
// come pre-pinned from neighborSessionID at the moment the delete/archive
// was fired — see SessionDeletedMsg for why recomputing it here, from
// whatever m.sessions has become by the time this message arrives, isn't
// good enough.
func (m *Model) refreshSessionsFocusing(nextID string) {
	m.refreshSessionsAndSync()
	if nextID == "" {
		return
	}
	m.focusSession(nextID)
	if m.mode == ModeMultiView && m.activeProj < len(m.projects) {
		m.leaveSingleProjectContext(m.projects[m.activeProj])
	}
}

func (m *Model) refreshSessions() {
	if len(m.projects) == 0 {
		m.sessions = nil
		return
	}
	var selectedID string
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		selectedID = m.sessions[m.cursor].ID
	}

	// Sessions with a live tmux window float to the top of the active
	// project's list regardless of order otherwise.
	proj := m.projects[m.activeProj]
	all := m.allSessions()
	out := make([]session.Session, 0, len(all))
	for _, s := range all {
		if s.Project == proj && s.Archived == m.showArchived {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return m.tmuxAlive[out[i].ID] && !m.tmuxAlive[out[j].ID]
	})
	m.sessions = out

	if selectedID != "" {
		m.focusSession(selectedID)
	}
	if m.cursor >= len(m.sessions) {
		if len(m.sessions) == 0 {
			m.cursor = 0
		} else {
			m.cursor = len(m.sessions) - 1
		}
	}
}

func (m *Model) Init() tea.Cmd {
	// refreshStatusCmd (normally the routine 2s refresh) is fired here too so
	// the startup tmux-alive check runs immediately rather than waiting for
	// the first tick — see the StatusRefreshedMsg case for what happens once
	// it resolves.
	return tea.Batch(listenStatus(m.statusCh), tickFlash(), refreshStatusCmd(m), tickTmux(), checkUpdateCmd(m.Version), tickUpdateCheck())
}

// updateCheckInterval is how often a long-running session re-polls GitHub
// Releases, so a session left open for days still notices new versions
// instead of only checking once at startup.
const updateCheckInterval = 1 * time.Hour

// checkUpdateCmd asynchronously checks GitHub Releases for a version newer
// than current. It's a background nicety, not a feature — any failure
// (offline, GitHub down, rate-limited) or a "dev" build with no real
// version to compare just means no message comes back.
func checkUpdateCmd(current string) tea.Cmd {
	return func() tea.Msg {
		latest, err := updatecheck.Latest(context.Background())
		if err != nil || !updatecheck.Newer(current, latest) {
			return nil
		}
		return UpdateAvailableMsg{Version: strings.TrimPrefix(latest, "v")}
	}
}

// runUpdateCmd shells out to the same command the footer/help already tell
// users to run by hand. Homebrew-only: a go install/git clone build has no
// self-update path, so this just fails with brew's own error in that case,
// which flashError surfaces as-is.
func runUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("sh", "-c", "brew update && brew upgrade moomux").CombinedOutput()
		if err != nil {
			return UpdateAppliedMsg{Err: fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))}
		}
		return UpdateAppliedMsg{}
	}
}

// tickUpdateCheck schedules the next recheck; see UpdateCheckTickMsg handling
// in Update() for what fires when it lands.
func tickUpdateCheck() tea.Cmd {
	return tea.Tick(updateCheckInterval, func(t time.Time) tea.Msg { return UpdateCheckTickMsg{} })
}

// listenStatus waits for the next snapshot, then folds in every other
// snapshot already queued behind it before handing one message to Update.
//
// The watchers run independently and each tick costs the TUI a full
// handler pass plus a re-render, so a burst — three watchers coming due
// together, or a flurry of filesystem events under an active agent — would
// otherwise be processed one whole pass at a time for state that the last
// snapshot in the burst already supersedes.
func listenStatus(ch <-chan watcher.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return StatusChannelClosedMsg{}
		}
		for {
			select {
			case next, ok := <-ch:
				if !ok {
					// Deliver what we have; the next listenStatus reads the
					// closed channel and reports it.
					return StatusTickMsg{Snap: snap}
				}
				snap = mergeSnapshots(snap, next)
			default:
				return StatusTickMsg{Snap: snap}
			}
		}
	}
}

// mergeSnapshots folds newer into older, newer winning per path. That
// matches how Update applies a single snapshot — merge into m.states, never
// replace — so coalescing a burst leaves exactly the state the snapshots
// would have produced one at a time. Neither input map is mutated: they
// belong to the watcher goroutines that built them.
func mergeSnapshots(older, newer watcher.Snapshot) watcher.Snapshot {
	states := make(map[string]watcher.State, len(older.States)+len(newer.States))
	for p, st := range older.States {
		states[p] = st
	}
	for p, st := range newer.States {
		states[p] = st
	}
	out := watcher.Snapshot{States: states, PollTime: newer.PollTime, Err: errors.Join(older.Err, newer.Err)}
	if out.PollTime.Before(older.PollTime) {
		out.PollTime = older.PollTime
	}
	return out
}

// tmuxRefreshInterval is how often the tmux-alive map and any missing
// prompts are refreshed. This used to ride on the watcher snapshot stream,
// which meant a `tmux list-sessions` subprocess (~4.5ms of fork and exec)
// per snapshot from any of the three watchers — several a second at idle,
// and up to ten a second while an agent's filesystem writes drove the
// debounced rescans. Whether a tmux session is alive has nothing to do with
// an agent touching a JSON file, so it gets its own timer.
var tmuxRefreshInterval = 2 * time.Second

func tickTmux() tea.Cmd {
	return tea.Tick(tmuxRefreshInterval, func(t time.Time) tea.Msg { return TmuxTickMsg{} })
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
