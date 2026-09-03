// Command uishot renders a moomux TUI screen to stdout as raw ANSI, using a
// fake backend and canned sample data — no real projects, git repos, or tmux
// sessions required. Pair it with scripts/screenshot.sh, which wraps the
// ANSI capture in a pty (so lipgloss emits color), converts it to HTML, and
// renders that HTML to a PNG with a headless browser.
//
// See CLAUDE.md ("UI changes") for when to run this.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// screens maps a scenario name to the key sequence that drives a freshly
// created Model from the list screen into that scenario. "list" needs no
// keys. Each entry is sent as a tea.KeyMsg: the named keys below map to
// their special tea.KeyType, anything else is typed as literal runes (so a
// whole word like "demo/Documents/foo" types into the focused input in one
// step).
var screens = map[string][]string{
	"list": {},
	// Same as "list" but with enough sessions to force scrolling in the
	// narrow stacked layout — see TestNarrowStackedDetailGrowsBeforeListClips.
	"long-list": {},
	// Same data as "long-list", cursor walked down past the first screenful
	// so both scrollHint chevrons are showing at once (⌃ above, ⌄ below) —
	// "long-list" alone only ever shows the ⌄ one, since it starts at the top.
	"long-list-scrolled": {"down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down"},
	"new-session":        {"n"},
	// Branch field focused, so its hint (the field that most often needs
	// correcting) is the one on screen — 2 tabs from the project selector.
	"new-session-branch": {"n", "tab", "tab"},
	// 9 tabs from the project selector lands on the thinking selector (see
	// newFormFieldCount) — the field right after the new model selector,
	// exercising both new rows' scroll-into-view and hint text at once.
	"new-session-model": {"n", "tab", "tab", "tab", "tab", "tab", "tab", "tab", "tab", "tab"},
	// 7 tabs from the project selector lands on the agent selector, which the
	// active sample project ("demo") defaults to codex; one "right" cycles it
	// to opencode, then a final tab lands on the model row, which for
	// opencode is a free-text input instead of the claude/codex selector —
	// exercises that per-agent branch.
	"new-session-model-opencode": {"n", "tab", "tab", "tab", "tab", "tab", "tab", "tab", "right", "tab"},
	// The form preselects the active project, so no "right" press is needed
	// to pick one; 3 tabs from there lands on the first-prompt textarea (see
	// newFormFieldCount) — both Enter and ctrl+j insert a newline there,
	// since that field is the one exception to Enter submitting the form.
	"new-session-multiline": {"n", "tab", "tab", "tab", "first line", "ctrl+j", "second line"},
	// Submitting against a backend whose CreateSession fails: the form stays
	// open with everything typed still in it, plus the wrapped error. The
	// trailing tab moves off the prompt textarea first, since Enter there
	// inserts a newline instead of submitting.
	"new-session-error":     {"n", "tab", "myfeat", "tab", "tab", "a first prompt that must survive the failure", "tab", "enter"},
	"new-session-wide-line": {"n", "tab", "tab", "tab", "this is a single long line typed into the first prompt field that should only wrap once it actually reaches the right edge of the box on a wide terminal"},
	// Adding/editing a project only happens inside the picker now (P/E were
	// removed from the main list), so these open it first.
	"new-project": {"/", "n"},
	"tag":         {"t"},
	// Same session as "list" but with a PR added alongside its existing
	// ticket — the case that motivated CompactDetail: a session tagged with
	// both blows up the detail pane. "compact-detail" is the same data with
	// the setting turned on, for a direct before/after comparison.
	"detail-ticket-and-pr": {},
	"compact-detail":       {},
	// Same before/after comparison, but in ModeMultiView's 3-project
	// side-by-side layout — "tab tab" moves focus to the 3rd column, the one
	// with the ticket+PR session, matching the single-column scenarios above.
	"multiview-detail-ticket-and-pr": {"tab", "tab"},
	"multiview-compact-detail":       {"tab", "tab"},
	"project-picker":                 {"/"},
	// Empty query browses every session across every project (see
	// matchSessions), demonstrating the default "nothing typed yet" state.
	"search": {"f"},
	// "old" only matches "old-spike", the archived sample session, showing
	// its dim "archived" tag in a filtered result.
	"search-typed": {"f", "old"},
	// Picks "spare" (the sessionless sample project) from the picker,
	// confirming multi-view pins it in alongside "demo" instead of it
	// staying invisible (see multiViewEligibleProjects/multiPinned).
	"multi-view-pin-empty-project": {"/", "down", "enter"},
	"edit-session":                 {"e"},
	"edit-project":                 {"/", "e"},
	// 3 tabs walks focus from repo (1) through base branch (2) and branch
	// prefix (3) to land on the emoji selector, showing its focused/[glyph]
	// state rather than the unfocused default the plain "edit-project" and
	// "new-project" scenarios capture.
	"edit-project-emoji": {"/", "e", "tab", "tab", "tab"},
	"confirm-delete":     {"d"},
	// Same dialog, but caught mid-flight: the "d" press's fetchGitStatusCmd
	// (the live re-check kicked off when the dialog opens) is deliberately
	// left undrained by renderScreen, so this captures the "checking git
	// status…" loading note before it resolves.
	"confirm-delete-checking": {"d"},
	// "demo" has sample sessions, so D there flashes the blocked error; the
	// confirm screen is only reachable on the sessionless "spare" project
	// (tab switches to it).
	"confirm-delete-project": {"tab", "D"},
	"delete-project-blocked": {"D"},
	"archived":               {"A"},
	"help":                   {"?"},
	"help-bottom":            {"?"},
	// needs-input has no keys of its own; renderScreen feeds it a
	// StatusTickMsg marking the first sample session watcher.NeedsInput.
	"needs-input": {},
	// Submits the new-project form with a path under ~/Documents that isn't
	// a git repo, landing on the "skip git" choice screen with its macOS
	// Files-and-Folders warning (see internal/tui/tcc.go). "$HOME" is
	// expanded to the real home dir at runtime so the warning actually
	// triggers regardless of machine. "ctrl+u" clears each field's cwd
	// prefill (see newProjectForm) before typing over it.
	"project-init-choice": {"/", "n", "ctrl+u", "demo2", "tab", "ctrl+u", "$HOME/Documents/projects", "enter"},
	// no-projects-startup is the actual first screen a zero-projects config
	// renders (tui.New's zero-projects branch auto-opens the add-project
	// form before any key is pressed) — no keys, since that's the point.
	"no-projects-startup": {},
	// no-projects starts from the same zero-projects config; esc backs out
	// of that auto-opened form to the empty list, to show its own
	// empty-state hint (for whoever backs out without adding one yet).
	"no-projects": {"esc"},
	// project-picker-emptied deletes the only (sessionless) project from
	// inside the picker itself, landing back on the picker's own "no
	// projects yet" render — a path the shared demo/spare sample data can't
	// reach (demo always has sessions, so it can never pass the delete
	// guard), hence the dedicated single-project config below.
	"project-picker-emptied": {"/", "d", "y"},
	"settings":               {"s"},
	// Theme is the settings screen's second row (index 1): one "down" from
	// sort mode, then enter drills into the existing theme picker.
	"theme-picker": {"s", "down", "enter"},
	// "down" moves the cursor onto "bugfix-timeout", the sample session with
	// a PR attached, so its detail panel shows the PR status row (see
	// renderScreen's prStatus wiring below).
	"pr-status": {"down"},
	// ModeMultiView is the default now (see tui.New), so "list" above already
	// captures it; these confirm normal session key bindings (delete/tag/
	// archived) still work and render their dialog correctly on top of it.
	"multi-view-delete":   {"d"},
	"multi-view-tag":      {"t"},
	"multi-view-archived": {"A"},
	// No keys of its own; renderScreen sets m.UpdateVersion directly to show
	// the footer's "update available" notice (checkUpdateCmd never runs in
	// this harness, since Init() is never called).
	"update-available": {},
}

var namedKeys = map[string]tea.KeyType{
	"tab":       tea.KeyTab,
	"shift+tab": tea.KeyShiftTab,
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEsc,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"pgdown":    tea.KeyPgDown,
	"ctrl+u":    tea.KeyCtrlU,
	"ctrl+j":    tea.KeyCtrlJ,
}

func keyMsgFor(s string) tea.KeyMsg {
	if kt, ok := namedKeys[s]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive sends msg through Update, then synchronously runs any returned
// tea.Cmd and feeds its resulting message back in — needed for scenarios
// like project-init-choice where the form submission dispatches an async
// backend call (AddProject) whose result message drives the mode switch.
func drive(m *tui.Model, msg tea.Msg) {
	_, cmd := m.Update(msg)
	runCmd(m, cmd)
}

func runCmd(m *tui.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(m, c)
		}
		return
	}
	_, next := m.Update(msg)
	runCmd(m, next)
}

type fakeBackend struct {
	sessions []session.Session
	// cfg, when set, is mutated by RemoveProject so scenarios that need to
	// screenshot the state *after* a removal (e.g. project-picker-emptied)
	// see it reflected in m.projects on the next refresh — every other
	// backend method here is a no-op since no other scenario needs its
	// project/session data to actually change.
	cfg *config.Config
	// worktreeStatus, keyed by session id, backs WorktreeStatus for scenarios
	// that need to show the dirty/unpushed delete warning; a missing entry
	// means "unknown" (ok=false), matching every other scenario's default.
	worktreeStatus map[string]struct{ dirty, unpushed bool }
	// changeSummary, keyed by session id, backs ChangeSummary for scenarios
	// that need to show the delete dialog's file/commit-count detail; a
	// missing entry means "unknown" (ok=false).
	changeSummary map[string]struct{ filesChanged, unpushedCommits int }
	// prStatus, keyed by session id, backs PRStatus for scenarios that need
	// to show the detail panel's PR status row; a missing entry means
	// "unknown" (ok=false), matching every other scenario's default.
	prStatus map[string]prstatus.Info
	// createErr, when set, makes CreateSession fail — for scenarios that
	// show what the new-session form looks like after a failed create.
	createErr error
}

func (f *fakeBackend) CreateSession(project, name, agent, existingBranch, ticket string, openTerminal bool, dangerous *bool, baseBranch, model, thinking string) (session.Session, string, error) {
	return session.Session{}, "", f.createErr
}
func (f *fakeBackend) StartFirstPrompt(tmuxSession, prompt string, autoSubmit bool) error {
	return nil
}
func (f *fakeBackend) OpenSession(id string) (string, error)   { return "", nil }
func (f *fakeBackend) DeleteSession(id string) (string, error) { return "", nil }
func (f *fakeBackend) WorktreeStatus(id string) (dirty, unpushed, ok bool) {
	st, present := f.worktreeStatus[id]
	if !present {
		return false, false, false
	}
	return st.dirty, st.unpushed, true
}
func (f *fakeBackend) ChangeSummary(id string) (filesChanged, unpushedCommits int, ok bool) {
	st, present := f.changeSummary[id]
	if !present {
		return 0, 0, false
	}
	return st.filesChanged, st.unpushedCommits, true
}
func (f *fakeBackend) PRStatus(id string) (prstatus.Info, bool) {
	info, present := f.prStatus[id]
	return info, present
}
func (f *fakeBackend) KillTmux(id string) error                                { return nil }
func (f *fakeBackend) SetSessionStatusTitle(id string, st watcher.State) error { return nil }
func (f *fakeBackend) MoveSession(id string, delta int) error                  { return nil }
func (f *fakeBackend) MoveProject(name string, delta int) error                { return nil }
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) SetSessionAgent(id, agent string, dangerous bool) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) RenameSession(id, newName string) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) SetSessionPrompt(id, prompt string) (session.Session, error) {
	return session.Session{}, nil
}
func (f *fakeBackend) SetSessionArchived(id string, archived bool) (session.Session, error) {
	return session.Session{}, nil
}

// TmuxAliveAll reports every sample session as alive so effectiveState
// doesn't force them all to "parked" — that would hide whatever State a
// scenario sets via a StatusTickMsg (see the "needs-input" scenario).
func (f *fakeBackend) TmuxAliveAll() map[string]bool {
	alive := make(map[string]bool, len(f.sessions))
	for _, s := range f.sessions {
		alive[s.ID] = true
	}
	return alive
}
func (f *fakeBackend) Sessions() []session.Session                           { return f.sessions }
func (f *fakeBackend) Projects() []string                                    { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) error        { return gitwt.ErrNotGitRepo }
func (f *fakeBackend) InitProjectAndAdd(name string, p config.Project) error { return nil }
func (f *fakeBackend) AddPlainProject(name string, p config.Project) error   { return nil }
func (f *fakeBackend) UpdateProject(name string, p config.Project) error     { return nil }
func (f *fakeBackend) RemoveProject(name string) error {
	if f.cfg != nil {
		delete(f.cfg.Projects, name)
	}
	return nil
}
func (f *fakeBackend) SetTheme(theme, appearance string) error {
	if f.cfg != nil {
		f.cfg.Theme = theme
		f.cfg.Appearance = appearance
	}
	return nil
}

func (f *fakeBackend) SetAutoSubmitDefault(autoSubmit bool) error {
	if f.cfg != nil {
		f.cfg.AutoSubmitDefault = autoSubmit
	}
	return nil
}

func (f *fakeBackend) SetCompactDetail(compact bool) error {
	if f.cfg != nil {
		f.cfg.CompactDetail = compact
	}
	return nil
}

func (f *fakeBackend) SetSortRecentFirst(recentFirst bool) error {
	if f.cfg != nil {
		f.cfg.SortRecentFirst = recentFirst
	}
	return nil
}

func (f *fakeBackend) SetAutoTmux(autoTmux bool) error {
	if f.cfg != nil {
		f.cfg.AutoTmux = autoTmux
	}
	return nil
}

func sampleSessions() []session.Session {
	now := time.Now().UTC()
	return []session.Session{
		{
			ID:           "demo:feature-auth",
			Project:      "demo",
			Name:         "feature-auth",
			Branch:       "feature/auth",
			WorktreePath: "/tmp/demo/feature-auth",
			TmuxSession:  "moomux-feature-auth",
			CreatedAt:    now,
			Agent:        "claude",
			Ticket:       "https://tracker.example/TICK-123",
			Prompt:       "add JWT-based auth to the login flow, including refresh token rotation, session revocation on logout, and rate limiting on the token endpoint so we don't get hammered by retries",
		},
		{
			ID:           "demo:bugfix-timeout",
			Project:      "demo",
			Name:         "bugfix-timeout",
			Branch:       "bugfix/timeout",
			WorktreePath: "/tmp/demo/bugfix-timeout",
			TmuxSession:  "moomux-bugfix-timeout",
			CreatedAt:    now,
			Agent:        "codex",
			PR:           "https://github.com/example/repo/pull/42",
		},
		{
			ID:           "demo:old-spike",
			Project:      "demo",
			Name:         "old-spike",
			Branch:       "spike/old-idea",
			WorktreePath: "/tmp/demo/old-spike",
			TmuxSession:  "moomux-old-spike",
			CreatedAt:    now,
			Agent:        "claude",
			Archived:     true,
		},
	}
}

// renderScreen drives a freshly created Model through the key sequence
// registered for screenName against canned sample data, returning its final
// rendered view. It's the piece scripts/screenshot.sh's pty/HTML/Chromium
// pipeline wraps, and the piece that's practical to cover with a Go test.
func renderScreen(screenName string, width, height int, theme, appearance string) (string, error) {
	keys, ok := screens[screenName]
	if !ok {
		return "", fmt.Errorf("unknown screen %q (want one of: %s)", screenName, screenNames())
	}

	cfg := &config.Config{Projects: map[string]config.Project{
		"demo": {
			Kind: "git", Repo: "/tmp/demo", BaseBranch: "main",
			BranchPrefix: "feature", Agent: "codex",
		},
		"spare": {
			Kind: "git", Repo: "/tmp/spare", BaseBranch: "main",
			BranchPrefix: "feature", Agent: "claude",
		},
	}}
	sessions := sampleSessions()
	switch screenName {
	case "long-list", "long-list-scrolled":
		now := time.Now().UTC()
		sessions = nil
		for i := 0; i < 25; i++ {
			name := fmt.Sprintf("session-%02d", i)
			sessions = append(sessions, session.Session{
				ID:           "demo:" + name,
				Project:      "demo",
				Name:         name,
				WorktreePath: "/tmp/demo/" + name,
				TmuxSession:  "moomux-" + name,
				CreatedAt:    now,
				Agent:        "claude",
			})
		}
	case "no-projects-startup", "no-projects":
		cfg = &config.Config{Projects: map[string]config.Project{}}
		sessions = nil
	case "detail-ticket-and-pr":
		sessions[0].PR = "https://github.com/example/repo/pull/5478"
	case "compact-detail":
		sessions[0].PR = "https://github.com/example/repo/pull/5478"
		cfg.CompactDetail = true
	case "multiview-detail-ticket-and-pr", "multiview-compact-detail":
		now := time.Now().UTC()
		cfg = &config.Config{Projects: map[string]config.Project{
			"eg_system": {Kind: "git", Repo: "/tmp/eg_system", BaseBranch: "main"},
			"dev_setup": {Kind: "git", Repo: "/tmp/dev_setup", BaseBranch: "main"},
			"moomux":    {Kind: "git", Repo: "/tmp/moomux", BaseBranch: "main"},
		}, Order: []string{"eg_system", "dev_setup", "moomux"}}
		sessions = []session.Session{
			{ID: "eg_system:one", Project: "eg_system", Name: "express-reference-number", WorktreePath: "/tmp/eg_system/one", TmuxSession: "moomux-eg-one", CreatedAt: now, Agent: "claude"},
			{ID: "dev_setup:one", Project: "dev_setup", Name: "show-ports", WorktreePath: "/tmp/dev_setup/one", TmuxSession: "moomux-dev-one", CreatedAt: now, Agent: "claude"},
			{
				ID: "moomux:one", Project: "moomux", Name: "compact-detail-section",
				WorktreePath: "/tmp/moomux/one", TmuxSession: "moomux-moomux-one", CreatedAt: now, Agent: "claude",
				Ticket: "https://tracker.example/TICK-42", PR: "https://github.com/example/repo/pull/5478",
			},
		}
		if screenName == "multiview-compact-detail" {
			cfg.CompactDetail = true
		}
	case "project-picker-emptied":
		cfg = &config.Config{Projects: map[string]config.Project{
			"solo": {Kind: "git", Repo: "/tmp/solo", BaseBranch: "main"},
		}}
		sessions = nil
	}
	cfg.Theme = theme
	cfg.Appearance = appearance
	be := &fakeBackend{sessions: sessions, cfg: cfg}
	if screenName == "new-session-error" {
		be.createErr = errors.New("no branch \"merchant-physcal\" in /tmp/demo (checked local and origin) — fix the name, or clear the branch field to start a new branch off main")
	}
	if screenName == "confirm-delete" && len(sessions) > 0 {
		// Must be set before the initial drive() below: git status is
		// fetched for every session up front (regardless of agent state —
		// it's no longer gated on being parked), so this needs to be in
		// place before that sweep runs in order to demonstrate the
		// dirty/unpushed delete warning on the session the "d" key sequence
		// lands the cursor on.
		be.worktreeStatus = map[string]struct{ dirty, unpushed bool }{
			sessions[0].ID: {dirty: true, unpushed: true},
		}
		be.changeSummary = map[string]struct{ filesChanged, unpushedCommits int }{
			sessions[0].ID: {filesChanged: 3, unpushedCommits: 2},
		}
	}
	if screenName == "pr-status" && len(sessions) > 1 {
		// Must be set before drive() below, for the same reason as
		// confirm-delete's worktreeStatus above: PR status is swept for
		// every PR-attached session up front.
		be.prStatus = map[string]prstatus.Info{
			sessions[1].ID: {State: "OPEN", Mergeable: "CONFLICTING", CI: "FAILING"},
		}
	}
	if screenName == "detail-ticket-and-pr" || screenName == "compact-detail" {
		be.prStatus = map[string]prstatus.Info{
			sessions[0].ID: {State: "OPEN", Mergeable: "MERGEABLE", CI: "PASSING"},
		}
	}
	if screenName == "multiview-detail-ticket-and-pr" || screenName == "multiview-compact-detail" {
		be.prStatus = map[string]prstatus.Info{
			"moomux:one": {State: "OPEN", Mergeable: "MERGEABLE", CI: "PASSING"},
		}
	}
	// Closed immediately: nothing in this synthetic harness ever sends on it,
	// and closing lets drive() safely run a StatusTickMsg's/StatusRefreshedMsg's
	// returned batch (which re-arms listenStatus(statusCh)) without blocking
	// forever on an open, empty channel.
	statusCh := make(chan watcher.Snapshot)
	close(statusCh)
	tui.ApplySettings(cfg)
	m := tui.New(cfg, be, (&app.App{}).AgentOptions(), statusCh, func() {})
	m.Version = "dev"
	if screenName == "update-available" {
		m.Version = "0.5.3"
		m.UpdateVersion = "0.5.4"
	}
	// tui.New() no longer calls TmuxAliveAll() synchronously (that now
	// happens async, via Init(), so a slow tmux server can't block the real
	// app's first render) — this harness never calls Init() at all, so
	// without this every sample session would default to "not alive" and
	// read as Parked regardless of the State a scenario sets below. This
	// same StatusRefreshedMsg also triggers the startup git-status sweep
	// (see model.go's tmuxCheckedOnce), fetching every session up front from
	// whatever be.worktreeStatus already holds.
	drive(m, tui.StatusRefreshedMsg{TmuxAlive: be.TmuxAliveAll()})

	home, _ := os.UserHomeDir()

	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	if screenName == "needs-input" {
		m.Update(tui.StatusTickMsg{Snap: watcher.Snapshot{
			States: map[string]watcher.State{sessions[0].WorktreePath: watcher.NeedsInput},
		}})
	}
	for _, k := range keys {
		msg := keyMsgFor(strings.ReplaceAll(k, "$HOME", home))
		if screenName == "confirm-delete-checking" {
			// Plain Update, not drive(): leaves the "d" press's
			// fetchGitStatusCmd undrained so the dialog is caught showing
			// its "checking git status…" note rather than the resolved
			// result.
			m.Update(msg)
			continue
		}
		drive(m, msg)
	}
	if screenName == "help-bottom" {
		// The overlay viewport gets its content and height on the first View.
		// Initialize it before paging down so narrow screenshots can inspect
		// the command tips at the bottom rather than only the list's first rows.
		m.View()
		for range 5 {
			drive(m, keyMsgFor("pgdown"))
		}
	}

	return m.View(), nil
}

func main() {
	screen := flag.String("screen", "list", fmt.Sprintf("screen to render: %s", screenNames()))
	width := flag.Int("width", 100, "terminal width")
	height := flag.Int("height", 32, "terminal height")
	theme := flag.String("theme", "", "theme name (default, terminal, gruvbox, catppuccin)")
	appearance := flag.String("appearance", "", "appearance override (light, dark; empty = auto)")
	flag.Parse()

	out, err := renderScreen(*screen, *width, *height, *theme, *appearance)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uishot: %s\n", err)
		os.Exit(1)
	}

	fmt.Print(out)
}

func screenNames() string {
	names := make([]string, 0, len(screens))
	for name := range screens {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}
