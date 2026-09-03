package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

// testAgentOptions is the agent/model/thinking-level table every test Model
// is constructed with — a fixture standing in for what App.AgentOptions
// would serve in production, kept in lockstep with it by
// TestAgentOptionsMatchesTUIFixture in agent_options_test.go.
var testAgentOptions = []config.AgentOption{
	{Name: "claude", Models: []string{"default", "sonnet", "opus", "fable"}, Thinking: []string{"default", "think", "think hard", "think harder", "ultrathink"}},
	{Name: "codex", Models: []string{"default", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}, Thinking: []string{"default", "minimal", "low", "medium", "high", "xhigh"}},
	{Name: "opencode", Thinking: []string{"default", "think", "think hard", "think harder", "ultrathink"}},
}

type fakeBackend struct {
	sessions []session.Session

	moveSessionCalls []moveSessionCall
	moveSessionErr   error

	moveProjectCalls []moveProjectCall
	moveProjectErr   error

	createCalls []createCall
	createErr   error
	createHint  string

	firstPromptCalls []sendPromptCall
	firstPromptErr   error

	deleteCalls []string
	deleteErr   error

	openCalls []string
	openErr   error
	openHint  string

	killCalls []string

	tagCalls []tagCall
	tagErr   error

	sessionAgentCalls []sessionAgentCall
	sessionAgentErr   error

	renameCalls []renameCall
	renameErr   error

	archiveCalls []archiveCall
	archiveErr   error

	addProjectCalls  []projectCall
	addProjectErr    error
	initProjectCalls []projectCall
	initProjectErr   error
	plainCalls       []projectCall
	plainErr         error

	updateProjectCalls []projectCall
	updateProjectErr   error

	removeProjectCalls []string
	removeProjectErr   error

	setThemeCalls []setThemeCall
	setThemeErr   error

	setAutoSubmitDefaultCalls []bool
	setAutoSubmitDefaultErr   error

	setSortRecentFirstCalls []bool
	setSortRecentFirstErr   error

	setAutoTmuxCalls []bool
	setAutoTmuxErr   error

	setCompactDetailCalls []bool
	setCompactDetailErr   error

	// worktreeStatus, keyed by session id, backs WorktreeStatus. A missing
	// entry means "unknown" (ok=false) rather than "clean".
	worktreeStatus      map[string]gitStatusInfo
	worktreeStatusCalls []string

	// changeSummary, keyed by session id, backs ChangeSummary. A missing
	// entry means "unknown" (ok=false).
	changeSummary      map[string]changeSummary
	changeSummaryCalls []string

	// prStatus, keyed by session id, backs PRStatus. A missing entry means
	// "unknown" (ok=false), mirroring worktreeStatus.
	prStatus      map[string]prStatusInfo
	prStatusCalls []string

	// tmuxAlive backs TmuxAliveAll; nil (the zero value) reads as "nothing
	// alive", same as the map[string]bool{} every other test relies on.
	tmuxAlive map[string]bool
}

type setThemeCall struct{ theme, appearance string }

type createCall struct {
	project, name, agent, branch, ticket string
	openTerminal                         bool
	dangerous                            *bool
	baseBranch, model, thinking          string
}
type sendPromptCall struct {
	tmuxSession, prompt string
	autoSubmit          bool
}
type tagCall struct{ id, ticket, pr string }
type sessionAgentCall struct {
	id, agent string
	dangerous bool
}
type renameCall struct{ id, newName string }
type archiveCall struct {
	id       string
	archived bool
}
type projectCall struct {
	name string
	p    config.Project
}

type moveSessionCall struct {
	id    string
	delta int
}

type moveProjectCall struct {
	name  string
	delta int
}

func (f *fakeBackend) CreateSession(project, name, agent, existingBranch, ticket string, openTerminal bool, dangerous *bool, baseBranch, model, thinking string) (session.Session, string, error) {
	f.createCalls = append(f.createCalls, createCall{project, name, agent, existingBranch, ticket, openTerminal, dangerous, baseBranch, model, thinking})
	if f.createErr != nil {
		return session.Session{}, "", f.createErr
	}
	label := name
	if label == "" {
		label = existingBranch
	}
	s := session.Session{ID: session.MakeID(project, label), Project: project, Name: label, Agent: agent, Dangerous: dangerous != nil && *dangerous, Ticket: ticket}
	f.sessions = append(f.sessions, s)
	return s, f.createHint, nil
}
func (f *fakeBackend) StartFirstPrompt(tmuxSession, prompt string, autoSubmit bool) error {
	f.firstPromptCalls = append(f.firstPromptCalls, sendPromptCall{tmuxSession, prompt, autoSubmit})
	return f.firstPromptErr
}
func (f *fakeBackend) OpenSession(id string) (string, error) {
	f.openCalls = append(f.openCalls, id)
	return f.openHint, f.openErr
}
func (f *fakeBackend) DeleteSession(id string) (string, error) {
	f.deleteCalls = append(f.deleteCalls, id)
	if f.deleteErr == nil {
		for i, s := range f.sessions {
			if s.ID == id {
				f.sessions = append(f.sessions[:i:i], f.sessions[i+1:]...)
				break
			}
		}
	}
	return "", f.deleteErr
}
func (f *fakeBackend) WorktreeStatus(id string) (dirty, unpushed, ok bool) {
	f.worktreeStatusCalls = append(f.worktreeStatusCalls, id)
	st, present := f.worktreeStatus[id]
	if !present {
		return false, false, false
	}
	return st.dirty, st.unpushed, st.ok
}
func (f *fakeBackend) ChangeSummary(id string) (filesChanged, unpushedCommits int, ok bool) {
	f.changeSummaryCalls = append(f.changeSummaryCalls, id)
	st, present := f.changeSummary[id]
	if !present {
		return 0, 0, false
	}
	return st.filesChanged, st.unpushedCommits, st.ok
}
func (f *fakeBackend) PRStatus(id string) (prstatus.Info, bool) {
	f.prStatusCalls = append(f.prStatusCalls, id)
	st, present := f.prStatus[id]
	if !present {
		return prstatus.Info{}, false
	}
	return st.info, st.ok
}
func (f *fakeBackend) KillTmux(id string) error {
	f.killCalls = append(f.killCalls, id)
	return nil
}
func (f *fakeBackend) SetSessionStatusTitle(id string, st watcher.State) error { return nil }
func (f *fakeBackend) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	f.tagCalls = append(f.tagCalls, tagCall{id, ticket, pr})
	if f.tagErr != nil {
		return session.Session{}, f.tagErr
	}
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Ticket, f.sessions[i].PR = ticket, pr
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Ticket: ticket, PR: pr}, nil
}
func (f *fakeBackend) SetSessionPrompt(id, prompt string) (session.Session, error) {
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Prompt = prompt
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Prompt: prompt}, nil
}
func (f *fakeBackend) SetSessionAgent(id, agent string, dangerous bool) (session.Session, error) {
	f.sessionAgentCalls = append(f.sessionAgentCalls, sessionAgentCall{id, agent, dangerous})
	if f.sessionAgentErr != nil {
		return session.Session{}, f.sessionAgentErr
	}
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Agent = agent
			f.sessions[i].Dangerous = dangerous
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Agent: agent, Dangerous: dangerous}, nil
}
func (f *fakeBackend) RenameSession(id, newName string) (session.Session, error) {
	f.renameCalls = append(f.renameCalls, renameCall{id, newName})
	if f.renameErr != nil {
		return session.Session{}, f.renameErr
	}
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Name = newName
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Name: newName}, nil
}
func (f *fakeBackend) SetSessionArchived(id string, archived bool) (session.Session, error) {
	f.archiveCalls = append(f.archiveCalls, archiveCall{id, archived})
	if f.archiveErr != nil {
		return session.Session{}, f.archiveErr
	}
	for i, s := range f.sessions {
		if s.ID == id {
			f.sessions[i].Archived = archived
			return f.sessions[i], nil
		}
	}
	return session.Session{ID: id, Archived: archived}, nil
}
func (f *fakeBackend) MoveSession(id string, delta int) error {
	f.moveSessionCalls = append(f.moveSessionCalls, moveSessionCall{id: id, delta: delta})
	return f.moveSessionErr
}
func (f *fakeBackend) MoveProject(name string, delta int) error {
	f.moveProjectCalls = append(f.moveProjectCalls, moveProjectCall{name: name, delta: delta})
	return f.moveProjectErr
}
func (f *fakeBackend) TmuxAliveAll() map[string]bool { return f.tmuxAlive }
func (f *fakeBackend) Sessions() []session.Session   { return f.sessions }
func (f *fakeBackend) Projects() []string            { return nil }
func (f *fakeBackend) AddProject(name string, p config.Project) error {
	f.addProjectCalls = append(f.addProjectCalls, projectCall{name, p})
	return f.addProjectErr
}
func (f *fakeBackend) InitProjectAndAdd(name string, p config.Project) error {
	f.initProjectCalls = append(f.initProjectCalls, projectCall{name, p})
	return f.initProjectErr
}
func (f *fakeBackend) AddPlainProject(name string, p config.Project) error {
	f.plainCalls = append(f.plainCalls, projectCall{name, p})
	return f.plainErr
}
func (f *fakeBackend) UpdateProject(name string, p config.Project) error {
	f.updateProjectCalls = append(f.updateProjectCalls, projectCall{name, p})
	return f.updateProjectErr
}
func (f *fakeBackend) RemoveProject(name string) error {
	f.removeProjectCalls = append(f.removeProjectCalls, name)
	return f.removeProjectErr
}

func (f *fakeBackend) SetTheme(theme, appearance string) error {
	f.setThemeCalls = append(f.setThemeCalls, setThemeCall{theme, appearance})
	return f.setThemeErr
}

func (f *fakeBackend) SetAutoSubmitDefault(autoSubmit bool) error {
	f.setAutoSubmitDefaultCalls = append(f.setAutoSubmitDefaultCalls, autoSubmit)
	return f.setAutoSubmitDefaultErr
}

func (f *fakeBackend) SetSortRecentFirst(recentFirst bool) error {
	f.setSortRecentFirstCalls = append(f.setSortRecentFirstCalls, recentFirst)
	return f.setSortRecentFirstErr
}

func (f *fakeBackend) SetAutoTmux(autoTmux bool) error {
	f.setAutoTmuxCalls = append(f.setAutoTmuxCalls, autoTmux)
	return f.setAutoTmuxErr
}

func (f *fakeBackend) SetCompactDetail(compact bool) error {
	f.setCompactDetailCalls = append(f.setCompactDetailCalls, compact)
	return f.setCompactDetailErr
}

// TestLinkHitsResolveClicks renders a full frame and asserts that clicking
// on the printed ticket/PR icon glyphs resolves to the session's URLs, and
// that clicking one column outside the icon range does not.
func TestLinkHitsResolveClicks(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one", Ticket: "https://ticket.example/1", PR: "https://pr.example/1"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24

	frame := m.View()
	lines := strings.Split(frame, "\n")

	findCol := func(icon string) (line, col int) {
		for li, l := range lines {
			if idx := strings.Index(l, icon); idx >= 0 {
				return li, lipgloss.Width(l[:idx])
			}
		}
		t.Fatalf("icon %q not found in rendered frame:\n%s", icon, frame)
		return -1, -1
	}

	ticketLine, ticketCol := findCol(iconTicket)
	prLine, prCol := findCol(iconPR)

	if got, _ := m.linkAt(ticketCol, ticketLine); got != be.sessions[0].Ticket {
		t.Errorf("click on ticket icon at (%d,%d) = %q, want %q", ticketCol, ticketLine, got, be.sessions[0].Ticket)
	}
	if got, _ := m.linkAt(prCol, prLine); got != be.sessions[0].PR {
		t.Errorf("click on pr icon at (%d,%d) = %q, want %q", prCol, prLine, got, be.sessions[0].PR)
	}
	if got, _ := m.linkAt(ticketCol-1, ticketLine); got != "" {
		t.Errorf("click one column left of ticket icon resolved to %q, want empty", got)
	}
}

func TestTruncatedDetailURLsRemainClickable(t *testing.T) {
	ticketURL := "https://tickets.example.com/org/project/issues/12345"
	prURL := "https://github.com/org/project/pull/67890"
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{
			ID:      "demo:one",
			Project: "demo",
			Name:    "one",
			Ticket:  ticketURL,
			PR:      prURL,
		},
	}}
	m := New(cfg, be, testAgentOptions, make(chan watcher.Snapshot), func() {})
	m.width, m.height = 80, 24

	frame := m.View()
	lines := strings.Split(frame, "\n")

	assertLink := func(visibleTail, wantURL string) {
		t.Helper()
		for line, rendered := range lines {
			if idx := strings.Index(rendered, visibleTail); idx >= 0 {
				col := lipgloss.Width(rendered[:idx])
				if got, _ := m.linkAt(col, line); got != wantURL {
					t.Fatalf("click on detail URL tail %q at (%d,%d) = %q, want %q\n%s", visibleTail, col, line, got, wantURL, frame)
				}
				return
			}
		}
		t.Fatalf("detail URL tail %q not found:\n%s", visibleTail, frame)
	}

	assertLink("issues/12345", ticketURL)
	assertLink("pull/67890", prURL)
}

func TestClippedDetailURLsDoNotLeaveClickTargets(t *testing.T) {
	m := newTestModel(&fakeBackend{sessions: []session.Session{
		{
			ID:      "demo:one",
			Project: "demo",
			Name:    "one",
			Ticket:  "https://tickets.example/123",
			PR:      "https://github.com/example/repo/pull/456",
		},
	}})

	// No "DETAIL" title text, but its 2-row gap is still reserved (to stay
	// aligned with the list pane's "SESSIONS" title next to it): blank,
	// blank, project, status, name, agent, ticket, pr — height 6 cuts off
	// before either link row, height 8 fits both.
	_, clippedHits := m.renderDetail(36, 6)
	if len(clippedHits) != 0 {
		t.Fatalf("clipped detail returned link hits: %+v", clippedHits)
	}

	_, visibleHits := m.renderDetail(36, 8)
	if len(visibleHits) != 2 {
		t.Fatalf("visible detail returned %d link hits, want 2: %+v", len(visibleHits), visibleHits)
	}
}

// TestLinkClickOverSSHCopiesInsteadOfOpening asserts that clicking a
// ticket/PR icon while browser.Remote() is true copies the URL to the
// clipboard (via OSC 52) instead of shelling out to `open` — since `open`
// would launch a browser on the remote machine rather than the user's own,
// and moomux's mouse tracking means the terminal never gets a chance to
// handle the tap as a link itself. The copy happens synchronously inside
// Update() rather than via a returned tea.Cmd: a Cmd runs in its own
// goroutine concurrently with bubbletea's render loop, and both writing to
// os.Stdout at once can interleave and corrupt the OSC 52 escape sequence
// before the terminal ever sees a well-formed one.
func TestLinkClickOverSSHCopiesInsteadOfOpening(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001")

	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one", Ticket: "https://ticket.example/1"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.View() // populate m.linkHits

	var hit resolvedLinkHit
	for _, h := range m.linkHits {
		if h.url == be.sessions[0].Ticket {
			hit = h
		}
	}
	updated, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: hit.x0, Y: hit.y})
	m2 := updated.(*Model)

	if cmd != nil {
		t.Errorf("expected no async command for the copy path, got one")
	}
	if m2.flashKind != "info" || m2.flash != "copied "+be.sessions[0].Ticket {
		t.Errorf("flash = (%q, %q), want (\"info\", %q)", m2.flashKind, m2.flash, "copied "+be.sessions[0].Ticket)
	}
}

// TestSessionRowClickSelectsWithoutOpening asserts that a left click
// anywhere on a session's row (not just its ticket/PR icons) moves the
// cursor to that session but does NOT open/attach it — a stray click used to
// launch a tmux attach unexpectedly, which is disruptive enough that
// tap-to-open was removed in favor of tap-to-select only.
func TestSessionRowClickSelectsWithoutOpening(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{
		sessions: []session.Session{
			{ID: "demo:one", Project: "demo", Name: "one"},
			{ID: "demo:two", Project: "demo", Name: "two"},
		},
		openHint: "run: tmux attach -t moomux-two",
	}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.mode = ModeList // exercising the plain-list click path, not multi-view's
	m.View()          // populate m.rowHits

	var hit resolvedRowHit
	for _, h := range m.rowHits {
		if h.sessionID == "demo:two" {
			hit = h
		}
	}
	if hit.sessionID == "" {
		t.Fatalf("no row hit found for demo:two, hits: %+v", m.rowHits)
	}

	run(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: hit.x0, Y: hit.y})

	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (demo:two)", m.cursor)
	}
	if len(be.openCalls) != 0 {
		t.Errorf("openCalls = %v, want none (row click no longer opens)", be.openCalls)
	}
}

// TestSessionRowClickOutsideListDoesNotSelect asserts that a click landing
// outside every row's coordinates (e.g. on the border or an empty area) is a
// no-op rather than resolving to whichever row happens to be nearest.
func TestSessionRowClickOutsideListDoesNotSelect(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.View()

	if id, ok := m.sessionRowAt(-1, -1); ok {
		t.Errorf("sessionRowAt(-1,-1) = (%q, true), want ok=false", id)
	}

	run(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: -1, Y: -1})
	if len(be.openCalls) != 0 {
		t.Errorf("openCalls = %v, want none", be.openCalls)
	}
}

// TestMouseWheelIsNoop asserts that mouse wheel events are ignored entirely.
// Moshi (the mobile SSH client) forwards a two-finger swipe as a wheel
// event indistinguishable from a desktop mouse wheel, and swipe-to-scroll
// on the session list read as broken/surprising on mobile — so wheel
// events don't move the cursor or anything else, on any client.
func TestMouseWheelIsNoop(t *testing.T) {
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one"},
		{ID: "demo:two", Project: "demo", Name: "two"},
	}}
	m := newTestModel(be)

	run(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.cursor != 0 {
		t.Fatalf("after wheel down: cursor = %d, want 0 (unchanged)", m.cursor)
	}

	run(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.cursor != 0 {
		t.Fatalf("after wheel up: cursor = %d, want 0 (unchanged)", m.cursor)
	}
}

// TestRemoteLinksToggleOverridesAutoDetection covers the R toggle: for
// transports browser.Remote() has no signal for (e.g. mosh without Moshi's
// MOSHI_CLIENT env var), a user needs to be able to force copy mode from
// inside the running session.
func TestRemoteLinksToggleOverridesAutoDetection(t *testing.T) {
	// Sandbox against the developer's own remote-session signals (e.g. this
	// suite may itself run inside Moshi/SSH) so the "no signal" assertion
	// below isn't at the mercy of ambient env vars.
	t.Setenv("SSH_TTY", "")
	t.Setenv("MOSHI_CLIENT", "")
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})

	// No SSH env set and not forced: isRemote() says false.
	if m.isRemote() {
		t.Errorf("with no SSH env and forceCopyLinks off: isRemote() = true, want false")
	}

	// Toggle on: forces isRemote() to true even with no SSH env.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = updated.(*Model)
	if !m.forceCopyLinks || !m.isRemote() {
		t.Errorf("after one R press: forceCopyLinks = %v, isRemote() = %v, want true/true", m.forceCopyLinks, m.isRemote())
	}

	// Toggle off again: falls back to auto-detection.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = updated.(*Model)
	if m.forceCopyLinks || m.isRemote() {
		t.Errorf("after two R presses: forceCopyLinks = %v, isRemote() = %v, want false/false", m.forceCopyLinks, m.isRemote())
	}
}

// TestTmuxRowClickAlwaysCopies asserts a click on the detail panel's tmux
// row copies the "tmux attach" command to the clipboard even when not
// remote — unlike ticket/PR rows it isn't a URL, so browser.Open would
// reject it outright, and opening a browser wouldn't make sense either way.
func TestTmuxRowClickAlwaysCopies(t *testing.T) {
	// See TestRemoteLinksToggleOverridesAutoDetection for why these are
	// cleared: this test needs a genuinely local (non-remote) environment.
	t.Setenv("SSH_TTY", "")
	t.Setenv("MOSHI_CLIENT", "")
	cfg := &config.Config{Projects: map[string]config.Project{"demo": {Repo: "/tmp/demo"}}}
	be := &fakeBackend{sessions: []session.Session{
		{ID: "demo:one", Project: "demo", Name: "one", TmuxSession: "moomux-one"},
	}}
	statusCh := make(chan watcher.Snapshot)
	m := New(cfg, be, testAgentOptions, statusCh, func() {})
	m.width, m.height = 80, 24
	m.View() // populate m.linkHits

	if m.isRemote() {
		t.Fatalf("expected local (non-remote) environment for this test")
	}

	wantURL := "tmux attach -t moomux-one"
	var hit resolvedLinkHit
	for _, h := range m.linkHits {
		if h.url == wantURL {
			hit = h
		}
	}
	if hit.url == "" {
		t.Fatalf("no link hit found for tmux command %q, hits: %+v", wantURL, m.linkHits)
	}

	updated, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: hit.x0, Y: hit.y})
	m2 := updated.(*Model)

	if cmd != nil {
		t.Errorf("expected no async command for the copy path, got one")
	}
	if m2.flashKind != "info" || m2.flash != "copied "+wantURL {
		t.Errorf("flash = (%q, %q), want (\"info\", %q)", m2.flashKind, m2.flash, "copied "+wantURL)
	}
}
