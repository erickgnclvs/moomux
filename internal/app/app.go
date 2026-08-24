// Package app glues config, session store, tmux, terminal and gitwt into a TUI Backend.
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/erickgnclvs/moomux/internal/browser"
	"github.com/erickgnclvs/moomux/internal/claudehook"
	"github.com/erickgnclvs/moomux/internal/codexhook"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/layout"
	"github.com/erickgnclvs/moomux/internal/prstatus"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/terminal"
	"github.com/erickgnclvs/moomux/internal/tmux"
	"github.com/erickgnclvs/moomux/internal/userscript"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

type App struct {
	Cfg          *config.Config
	CfgPath      string
	Store        *session.Store
	Tmux         *tmux.Client
	Terminal     terminal.TerminalOpener
	Git          *gitwt.Client
	PR           *prstatus.Client
	WorktreeRoot string
}

// agentCmd returns the CLI binary name for the given agent.
func agentCmd(agent string) string {
	switch agent {
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	default:
		return "claude"
	}
}

// dangerousFlag returns the CLI flag that skips permission prompts for the
// given agent, or "" if it doesn't have one (opencode).
func dangerousFlag(agent string) string {
	switch agent {
	case "codex":
		return "--yolo"
	case "claude":
		return "--dangerously-skip-permissions"
	default:
		return ""
	}
}

// buildAgentCmd returns the shell command that launches agent in its tmux
// pane, appending its dangerous flag when requested and supported.
func buildAgentCmd(agent string, dangerous bool) string {
	cmd := agentCmd(agent)
	if dangerous {
		if flag := dangerousFlag(agent); flag != "" {
			cmd += " " + flag
		}
	}
	return cmd
}

// newTmuxSession creates tmuxName's tmux session at cwd, using the pane
// layout defined by cwd's optional .moomux-panes.toml if present and valid,
// falling back to the default two-pane split otherwise — a malformed layout
// file only logs a warning, it never blocks session creation.
func (a *App) newTmuxSession(tmuxName, cwd, cmd, windowName string) error {
	spec, err := layout.Load(cwd)
	if err != nil {
		slog.Warn("pane layout invalid, using default layout", "path", cwd, "err", err)
		spec = nil
	}
	if spec == nil {
		return a.Tmux.NewSession(tmuxName, cwd, cmd, windowName)
	}
	return a.Tmux.NewSessionWithLayout(tmuxName, cwd, windowName, spec, cmd)
}

// agentInstallers are the per-agent writers that wire moomux's integrations
// into the user's global agent config: the "needs input" hooks, and the
// /kill, /tag, /spawn, and /reseed custom commands (so any session can park,
// tag, or spawn a delegated session without leaving the agent). Each maps an
// agent name to its installer; an agent with
// no entry (opencode, everywhere) doesn't support that integration yet — add a
// sibling package and a map entry to bring one in. changed reports whether the
// call actually wrote something new (see codexHooksHint).
//
// All of them are global rather than per-worktree/per-project: each agent's
// own trust model would otherwise force re-approving hooks — or
// re-discovering commands — on every new worktree (see
// claudehook.EnsureHooksInstalled and codexhook.EnsureHooks for why).
var agentInstallers = []struct {
	what string
	// hintOnChange marks the entry whose changed=true produces the user-facing
	// activation hint (see codexHooksHint); the custom commands need no
	// trust/review step, so they have nothing to say.
	hintOnChange bool
	byAgent      map[string]func(home string) (changed bool, err error)
}{
	{"needs-input hook", true, map[string]func(string) (bool, error){
		"claude": claudehook.EnsureHooksInstalled,
		"codex":  codexhook.EnsureHooks,
	}},
	{"kill command", false, map[string]func(string) (bool, error){
		"claude": claudehook.EnsureKillCommand,
		"codex":  codexhook.EnsureKillCommand,
	}},
	{"tag command", false, map[string]func(string) (bool, error){
		"claude": claudehook.EnsureTagCommand,
		"codex":  codexhook.EnsureTagCommand,
	}},
	{"spawn command", false, map[string]func(string) (bool, error){
		"claude": claudehook.EnsureSpawnCommand,
		"codex":  codexhook.EnsureSpawnCommand,
	}},
	{"reseed command", false, map[string]func(string) (bool, error){
		"claude": claudehook.EnsureReseedCommand,
		"codex":  codexhook.EnsureReseedCommand,
	}},
}

// installAgentSupport runs every agentInstallers entry that applies to agent,
// returning a hint (see codexHooksHint) if that changed something worth
// telling the user about. Every installer is idempotent, so this runs on
// session create *and* every open — that's what backfills sessions made
// before a given integration (or a newer hook event) existed, and a failure
// only warns rather than blocking the session.
//
// Deliberately not gated on the project using worktrees: every installer
// ignores the worktree path and writes to a fixed global location, so a
// plain/no-worktree project deserves them just as much as a worktree one.
func installAgentSupport(agent string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("agent support install failed", "agent", agent, "err", err)
		return ""
	}
	hint := ""
	for _, entry := range agentInstallers {
		install, ok := entry.byAgent[agent]
		if !ok {
			continue
		}
		changed, err := install(home)
		if err != nil {
			slog.Warn(entry.what+" install failed", "agent", agent, "err", err)
			continue
		}
		if changed && entry.hintOnChange {
			hint = codexHooksHint(agent)
		}
	}
	return hint
}

// InstallKnownCommands backfills custom commands for every agent referenced
// by a configured project or existing session. It intentionally skips hook
// installers: newly written hooks require an in-agent trust prompt, whose hint
// is only meaningful while creating or opening that agent's session.
//
// Running this at ordinary moomux startup makes command upgrades discoverable
// without requiring the user to reopen a particular session first.
func (a *App) InstallKnownCommands() {
	agents := make(map[string]bool)
	for _, project := range a.Cfg.Projects {
		agents[project.AgentName()] = true
	}
	for _, s := range a.Store.All() {
		agents[s.AgentName()] = true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("agent command install failed", "err", err)
		return
	}
	for _, agent := range []string{"claude", "codex", "opencode"} {
		if !agents[agent] {
			continue
		}
		for _, entry := range agentInstallers {
			if entry.hintOnChange {
				continue
			}
			install, ok := entry.byAgent[agent]
			if !ok {
				continue
			}
			if _, err := install(home); err != nil {
				slog.Warn(entry.what+" install failed", "agent", agent, "err", err)
			}
		}
	}
}

// ReseedWorktree re-runs the worktree-create userscripts for an existing
// session's worktree with MOOMUX_FORCE=1, so a template update (e.g. to
// wt-seed-env.sh's templates) can be re-applied without deleting and
// recreating the session — the CLI backend for `moomux reseed` / the
// /reseed slash command.
func (a *App) ReseedWorktree(s session.Session) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{err.Error()}
	}
	return userscript.RunWorktreeCreate(home, userscript.Env{
		Project:  s.Project,
		Worktree: s.WorktreePath,
		Repo:     a.Cfg.Projects[s.Project].Repo,
		Branch:   s.Branch,
		Force:    true,
	})
}

func validateAgent(agent string) error {
	switch agent {
	case "claude", "codex", "opencode":
		return nil
	default:
		return fmt.Errorf("unknown agent %q", agent)
	}
}

// TmuxSessionName returns the tmux session name for a moomux session. The
// short ID hash keeps names unique across projects — "moomux-<name>" alone
// collides when two projects each have a session with the same name, and a
// name-only scheme is also ambiguous ("a" + "b-c" vs "a-b" + "c"). Nothing
// ever parses this back; only uniqueness and greppability matter.
func TmuxSessionName(id, name string) string {
	sum := sha256.Sum256([]byte(id))
	return "moomux-" + name + "-" + hex.EncodeToString(sum[:2])
}

func WorktreeRootDefault() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "moomux", "worktrees")
}

// nextOpenCodePort returns the next available port for an OpenCode session.
// Starts at 4096 and increments past the highest AgentPort recorded across
// all sessions; AgentPort is only ever non-zero on OpenCode sessions, so
// this is effectively scoped to those without needing an explicit filter.
func (a *App) nextOpenCodePort() int {
	port := 4096
	for _, s := range a.Store.All() {
		if s.AgentPort >= port {
			port = s.AgentPort + 1
		}
	}
	return port
}

func (a *App) Projects() []string { return a.Cfg.OrderedProjectNames() }

// MoveProject shifts the project with the given name by delta positions (-1
// left, +1 right) in the manual project order and persists it. It's a no-op
// if the move would go out of bounds.
func (a *App) MoveProject(name string, delta int) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	order := a.Projects()
	idx := -1
	for i, n := range order {
		if n == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown project %q", name)
	}
	j := idx + delta
	if j < 0 || j >= len(order) {
		return nil
	}
	order[idx], order[j] = order[j], order[idx]
	prev := a.Cfg.Order
	a.Cfg.Order = order
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.Order = prev
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// Sessions returns every known session, reloading the store from disk first
// so sessions written by another moomux process (e.g. `moomux spawn`) show
// up on the TUI's next routine status tick instead of waiting for a
// mutating action (archive, delete, reorder) to trigger a reload as a side
// effect.
func (a *App) Sessions() []session.Session {
	_ = a.Store.Reload()
	all := a.Store.All()
	if a.Cfg.SortRecentFirst {
		session.SortByRecent(all)
	}
	return all
}

// sanitizeName collapses anything that isn't alphanumeric/-/_ to "-", so the
// result is safe as a git branch name, filesystem path component, and tmux
// session name all at once.
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "session"
	}
	return out
}

// deriveNameFromBranch turns a branch name like "feature/login-page" into a
// filesystem/tmux-safe session name like "login-page".
func deriveNameFromBranch(branch string) string {
	name := branch
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return sanitizeName(name)
}

// uniqueNameFromBranch derives a session name from branch and, if it already
// collides with an existing session in project, appends -2, -3, ... until free.
func (a *App) uniqueNameFromBranch(project, branch string) string {
	base := deriveNameFromBranch(branch)
	name := base
	for i := 2; ; i++ {
		if _, ok := a.Store.Get(session.MakeID(project, name)); !ok {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// tmuxSessionUsingWorktree returns the tmux session name of any tracked
// session whose worktree is path and whose tmux session is still alive, so
// callers can avoid force-removing a worktree out from under a live pane
// (see internal/tmux/tmux.go's PaneCwd doc comment for what that breaks).
// Empty result means no live session is using path.
func (a *App) tmuxSessionUsingWorktree(path string) (string, error) {
	for _, s := range a.Store.All() {
		if s.WorktreePath != path {
			continue
		}
		has, err := a.Tmux.HasSession(s.TmuxSession)
		if err != nil {
			return "", err
		}
		if has {
			return s.TmuxSession, nil
		}
	}
	return "", nil
}

// CreateSession's hint, when non-empty, is a user-facing instruction
// (e.g. "run: tmux attach -t ...") to show alongside success — it is
// not an error. When openTerminal is false, the tmux session is started
// detached and no terminal window is opened.
func (a *App) CreateSession(project, name, agent, existingBranch, ticket string, openTerminal, dangerous bool) (session.Session, string, error) {
	proj, ok := a.Cfg.Projects[project]
	if !ok {
		return session.Session{}, "", fmt.Errorf("unknown project %q", project)
	}
	if name == "" {
		if existingBranch == "" {
			return session.Session{}, "", fmt.Errorf("session name required")
		}
		name = a.uniqueNameFromBranch(project, existingBranch)
	} else {
		name = sanitizeName(name)
	}
	if agent == "" {
		agent = proj.AgentName()
	}
	if err := validateAgent(agent); err != nil {
		return session.Session{}, "", err
	}
	if _, exists := a.Store.Get(session.MakeID(project, name)); exists {
		// Same name means the same worktree path — creating it again would
		// hijack the existing session's checkout.
		return session.Session{}, "", fmt.Errorf("session %q already exists in project %q", name, project)
	}
	var wt string
	tmuxName := TmuxSessionName(session.MakeID(project, name), name)
	branch := ""
	var userscriptHint string

	if proj.UsesWorktree() {
		wt = filepath.Join(a.WorktreeRoot, project, name)
	} else {
		wt = proj.Repo
	}

	slog.Info("create session", "project", project, "name", name, "agent", agent, "worktree", wt, "branch", existingBranch)

	newBranch := existingBranch == ""
	if proj.UsesWorktree() {
		hasRemote := a.Git.HasRemote(proj.Repo, "origin")
		if existingBranch != "" {
			branch = existingBranch
			if hasRemote {
				_ = a.Git.Fetch(proj.Repo, existingBranch) // best-effort
			}
			// A branch that resolves to nothing is a typo far more often
			// than an intent to create it (that's what leaving this field
			// blank does). Fail before creating anything, with a message the
			// user can act on — git's own "fatal: invalid reference" doesn't
			// say which field was wrong or what to do about it.
			if !a.Git.BranchExists(proj.Repo, branch) && !a.Git.RemoteBranchExists(proj.Repo, branch) {
				return session.Session{}, "", fmt.Errorf("no branch %q in %s (checked local and origin) — fix the name, or clear the branch field to start a new branch off %s", branch, proj.Repo, proj.BaseBranch)
			}
		} else {
			branch = name
			if prefix := strings.TrimSuffix(proj.BranchPrefix, "/"); prefix != "" {
				branch = prefix + "/" + name
			}
		}
		if newBranch && hasRemote {
			_ = a.Git.Fetch(proj.Repo, proj.BaseBranch) // best-effort
		}
		var err error
		if !newBranch {
			if staleWT, ferr := a.Git.WorktreeForBranch(proj.Repo, branch); ferr == nil && staleWT != "" && staleWT != wt {
				clean, cerr := a.Git.IsWorktreeClean(staleWT)
				if cerr != nil {
					return session.Session{}, "", fmt.Errorf("check existing worktree %s: %w", staleWT, cerr)
				}
				if !clean {
					return session.Session{}, "", fmt.Errorf("branch %q is already checked out at %s with uncommitted changes", branch, staleWT)
				}
				if busy, terr := a.tmuxSessionUsingWorktree(staleWT); terr != nil {
					return session.Session{}, "", fmt.Errorf("check tmux sessions for %s: %w", staleWT, terr)
				} else if busy != "" {
					return session.Session{}, "", fmt.Errorf("branch %q is already checked out at %s, in use by tmux session %q", branch, staleWT, busy)
				}
				if err := a.Git.RemoveWorktree(proj.Repo, staleWT); err != nil {
					return session.Session{}, "", fmt.Errorf("remove stale worktree %s: %w", staleWT, err)
				}
				slog.Info("removed stale clean worktree for branch", "branch", branch, "path", staleWT)
			}
			err = a.Git.AddWorktreeExisting(proj.Repo, wt, branch)
		} else {
			err = a.Git.AddWorktree(proj.Repo, wt, branch, proj.BaseBranch)
		}
		if err != nil {
			slog.Error("git worktree add failed", "repo", proj.Repo, "path", wt, "branch", branch, "err", err)
			return session.Session{}, "", fmt.Errorf("git worktree add: %w", err)
		}
		slog.Info("worktree added", "path", wt, "branch", branch)
		if home, err := os.UserHomeDir(); err != nil {
			slog.Warn("userscript run skipped", "err", err)
		} else {
			for _, w := range userscript.RunWorktreeCreate(home, userscript.Env{
				Project:  project,
				Worktree: wt,
				Repo:     proj.Repo,
				Branch:   branch,
			}) {
				slog.Warn("userscript", "warning", w)
				userscriptHint = joinHints(userscriptHint, w)
			}
		}
	}
	hooksHint := installAgentSupport(agent)
	if agent == "claude" {
		if home, err := os.UserHomeDir(); err != nil {
			slog.Warn("claude trust write failed", "err", err)
		} else if err := claudehook.TrustDirectory(home, wt); err != nil {
			slog.Warn("claude trust write failed", "path", wt, "err", err)
		}
	}
	cmd := buildAgentCmd(agent, dangerous)
	agentPort := 0
	if agent == "opencode" {
		agentPort = a.nextOpenCodePort()
		cmd = fmt.Sprintf("opencode --port %d", agentPort)
	}

	if err := a.newTmuxSession(tmuxName, wt, cmd, name); err != nil {
		slog.Error("tmux new-session failed", "name", tmuxName, "cwd", wt, "err", err)
		return session.Session{}, "", fmt.Errorf("tmux new-session: %w", err)
	}
	slog.Info("tmux session created", "name", tmuxName)
	var tabID, hint string
	var opened bool
	if openTerminal {
		var err error
		tabID, hint, err = a.openTerminal("", tmuxName, name)
		if err != nil {
			// The worktree and tmux session already exist at this point;
			// failing would strand them outside the store. Degrade to a
			// manual-attach hint instead.
			slog.Error("terminal open failed", "tmux_session", tmuxName, "name", name, "err", err)
			hint = fmt.Sprintf("couldn't open a terminal (%v) — attach yourself: tmux attach -t %s", err, tmuxName)
		} else {
			opened = true
		}
		if hooksHint != "" {
			hint = joinHints(hooksHint, hint)
		}
		slog.Info("terminal opened", "tmux_session", tmuxName)
	} else {
		hint = joinHints(hooksHint, fmt.Sprintf("tmux session started in background — attach with: tmux attach -t %s", tmuxName))
	}
	hint = joinHints(hint, userscriptHint)

	s := session.Session{
		ID:           session.MakeID(project, name),
		Project:      project,
		Name:         name,
		Branch:       branch,
		NewBranch:    proj.UsesWorktree() && newBranch,
		WorktreePath: wt,
		TmuxSession:  tmuxName,
		CreatedAt:    time.Now().UTC(),
		Agent:        agent,
		Dangerous:    dangerous,
		AgentPort:    agentPort,
		Ticket:       ticket,
		TermTabID:    tabID,
	}
	// A terminal opened right here means the user is dropped straight into
	// the new session, same as OpenSession's attach — without this, a
	// never-since-reopened session sorts as if never opened at all and
	// sinks to the bottom under "most-recently-opened first".
	if opened {
		s.LastOpened = time.Now()
	}
	if err := a.Store.Put(s); err != nil {
		slog.Error("store put failed", "id", s.ID, "err", err)
		err = fmt.Errorf("store: %w", err)
		if hint != "" {
			// The tmux session is already running at this point (see
			// above); don't let the caller lose the manual-attach hint
			// just because persisting the session record also failed.
			err = fmt.Errorf("%w; %s", err, hint)
		}
		return s, hint, err
	}
	return s, hint, nil
}

// SendPrompt types text into a session's agent pane, for handing a freshly
// spawned session its initial task. No-op if prompt is empty.
func (a *App) SendPrompt(tmuxSession, prompt string) error {
	if prompt == "" {
		return nil
	}
	return a.Tmux.SendKeys(tmuxSession, prompt)
}

// paneStablePoll/paneStableChecks/paneReadyTimeout tune StartFirstPrompt's
// readiness wait: an agent CLI's startup render (splash, model warmup, hook
// setup) takes a variable amount of time, and typing into it before it's
// idle at its input box means the keystrokes land on a screen that never
// reads them — visible as text landing on the plain shell prompt, before the
// agent has even opened. Waiting for two consecutive identical captures is a
// generic proxy for "done redrawing, waiting on input" that doesn't need to
// know anything about the specific agent CLI's UI — but the idle shell
// prompt right after the launch command was typed looks just as "stable" as
// an idle agent input box does, so waitForPaneReady first waits for the pane
// to change at all (proving the agent process actually took over the
// terminal) before it starts checking for stability, using the whole
// readiness budget for that wait rather than a shorter carve-out of it.
const (
	paneStablePoll   = 300 * time.Millisecond
	paneStableChecks = 2
	paneReadyTimeout = 15 * time.Second
)

// waitForPaneReady blocks until tmuxSession's pane content has visibly
// changed from its pre-launch state and then stops changing across
// paneStableChecks consecutive polls, or paneReadyTimeout elapses overall —
// whichever comes first. Timing out during either phase is not an error: the
// caller proceeds best-effort regardless, same as the fixed-delay behavior
// this replaces.
func (a *App) waitForPaneReady(tmuxSession string) {
	deadline := time.Now().Add(paneReadyTimeout)
	before, _ := a.Tmux.CapturePane(tmuxSession)

	last := before
	changed := false
	for !changed && time.Now().Before(deadline) {
		time.Sleep(paneStablePoll)
		if cur, err := a.Tmux.CapturePane(tmuxSession); err == nil {
			changed = cur != before
			last = cur
		}
	}
	if !changed {
		// Never saw anything but the pre-launch state within the whole
		// budget — proceeding to check "stability" against it would just
		// immediately declare it stable, defeating the point of this wait.
		return
	}
	a.waitForPaneStable(tmuxSession, last, deadline)
}

// waitForPaneStable polls tmuxSession's pane, starting from the already-known
// seed content, until paneStableChecks consecutive captures come back
// identical (and non-empty) or deadline passes — whichever comes first.
// Shared by waitForPaneReady (waiting for startup rendering to finish) and
// StartFirstPrompt (waiting for the just-typed prompt to finish rendering
// before Enter is pressed) — both are the same "stop guessing a fixed delay,
// wait for the pane to actually stop changing" problem.
func (a *App) waitForPaneStable(tmuxSession, seed string, deadline time.Time) {
	last := seed
	stable := 0
	for {
		cur, err := a.Tmux.CapturePane(tmuxSession)
		if err == nil && cur == last && cur != "" {
			stable++
			if stable >= paneStableChecks {
				return
			}
		} else {
			stable = 0
		}
		last = cur
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(paneStablePoll)
	}
}

// StartFirstPrompt waits for a freshly created session's agent pane to
// finish starting up and types prompt into it. If autoSubmit is true, it
// waits for the pane to finish re-rendering the typed prompt (a fixed delay
// isn't reliable here — see waitForPaneStable) and then separately presses
// Enter to start the agent working on it right away; otherwise the prompt is
// left typed in the pane for the user to review and send themselves. No-op
// if prompt is empty.
func (a *App) StartFirstPrompt(tmuxSession, prompt string, autoSubmit bool) error {
	if prompt == "" {
		return nil
	}
	a.waitForPaneReady(tmuxSession)
	if err := a.Tmux.SendLiteral(tmuxSession, prompt); err != nil {
		return err
	}
	if !autoSubmit {
		return nil
	}
	a.waitForPaneStable(tmuxSession, "", time.Now().Add(paneReadyTimeout))
	return a.pressEnterUntilSubmitted(tmuxSession, prompt)
}

// enterConfirmPolls/enterConfirmRetries tune pressEnterUntilSubmitted: how
// long a single Enter press is given to actually clear the typed prompt
// before it's retried, and how many times it's retried.
const (
	enterConfirmPolls   = 5
	enterConfirmRetries = 2
)

// pressEnterUntilSubmitted presses Enter and confirms the just-typed prompt
// actually left the pane's input line, retrying if it didn't. waitForPaneReady
// can only prove the pane looked stable at some point after the agent took
// over — not that the plateau it caught was the agent's final, truly-idle
// input rather than an earlier pause in a multi-phase startup (splash screen,
// then connecting to MCP servers, then finally ready). Landing in an earlier
// plateau can put the Enter in the same narrow window the agent's own
// paste-safety heuristic swallows (see SendLiteral's doc comment), so instead
// of trusting the first press, check whether the prompt is still sitting
// there afterward and press again if so.
func (a *App) pressEnterUntilSubmitted(tmuxSession, prompt string) error {
	for attempt := 0; ; attempt++ {
		if err := a.Tmux.PressEnter(tmuxSession); err != nil {
			return err
		}
		submitted := false
		for i := 0; i < enterConfirmPolls; i++ {
			time.Sleep(paneStablePoll)
			cur, err := a.Tmux.CapturePane(tmuxSession)
			if err == nil && !strings.Contains(cur, prompt) {
				submitted = true
				break
			}
		}
		if submitted || attempt >= enterConfirmRetries {
			return nil
		}
	}
}

// MoveSession shifts the session with the given id by delta positions (-1
// up, +1 down) within its project's session list, and persists the new
// order. It's a no-op if the move would go out of bounds.
func (a *App) MoveSession(id string, delta int) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	peers := a.Store.ByProject(s.Project)
	idx := -1
	for i, p := range peers {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("unknown session %q", id)
	}
	j := idx + delta
	if j < 0 || j >= len(peers) {
		return nil
	}
	peers[idx], peers[j] = peers[j], peers[idx]
	return a.Store.Reorder(peers)
}

// statusGlyphs are the status prefixes titleGlyph applies, checked in order
// so a name that happens to start with one glyph doesn't get double-stripped
// by another (none currently overlap, but order stays explicit).
var statusGlyphs = []string{"● ", "⚠ ", "✓ "}

// stripStatusGlyph removes a leading status glyph titleGlyph previously
// applied, recovering the user-facing name underneath (which may be the
// session's stored name, or a name the user typed directly via tmux's own
// rename-window, e.g. Ctrl-B ,).
func stripStatusGlyph(name string) string {
	for _, g := range statusGlyphs {
		if trimmed, ok := strings.CutPrefix(name, g); ok {
			return trimmed
		}
	}
	return name
}

// titleGlyph prefixes name with a marker for the given status so terminals
// tracking the tmux window name as their tab title (see
// tmux.Client.ConfigureTitleTracking) show it at a glance.
func titleGlyph(st watcher.State, name string) string {
	switch st {
	case watcher.Working:
		return "● " + name
	case watcher.NeedsInput:
		return "⚠ " + name
	case watcher.Done:
		return "✓ " + name
	default:
		return name
	}
}

// SetSessionStatusTitle renames id's tmux window to reflect st, so its
// terminal tab title updates live as the session's status changes. If the
// user renamed the window directly (e.g. via tmux's own rename-window), that
// name is preserved and only the status prefix is updated.
func (a *App) SetSessionStatusTitle(id string, st watcher.State) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return nil
	}
	name := s.Name
	if current, err := a.Tmux.WindowName(s.TmuxSession); err == nil && current != "" {
		name = stripStatusGlyph(current)
	}
	return a.Tmux.SetWindowName(s.TmuxSession, titleGlyph(st, name))
}

func (a *App) SetSessionTags(id, ticket, pr string) (session.Session, error) {
	s, ok := a.Store.Get(id)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown session %q", id)
	}
	s.Ticket = ticket
	s.PR = pr
	if err := a.Store.Put(s); err != nil {
		return s, fmt.Errorf("store: %w", err)
	}
	return s, nil
}

// SessionForTmuxName finds the session running as tmux session tmuxName.
// Preferred over SessionForPath for identifying "the session we're in": it
// keys off the tmux session the caller's pane actually belongs to, which a
// stray `cd` earlier in the same shell can't change, unlike cwd.
func (a *App) SessionForTmuxName(tmuxName string) (session.Session, bool) {
	for _, s := range a.Store.All() {
		if s.TmuxSession == tmuxName {
			return s, true
		}
	}
	return session.Session{}, false
}

// SessionForPath finds the session whose worktree is path or an ancestor of
// path, letting `moomux tag` identify "the session we're in" from the
// caller's cwd without needing an explicit session ID.
func (a *App) SessionForPath(path string) (session.Session, bool) {
	path = filepath.Clean(path)
	for _, s := range a.Store.All() {
		wt := filepath.Clean(s.WorktreePath)
		if path == wt {
			return s, true
		}
		if rel, err := filepath.Rel(wt, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return s, true
		}
	}
	return session.Session{}, false
}

func (a *App) SetSessionPrompt(id, prompt string) (session.Session, error) {
	s, ok := a.Store.Get(id)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown session %q", id)
	}
	s.Prompt = prompt
	if err := a.Store.Put(s); err != nil {
		return s, fmt.Errorf("store: %w", err)
	}
	return s, nil
}

// RenameSession changes id's display name and, to keep what's on screen
// aligned, its live tmux session name too — leaving ID (the store's key),
// WorktreePath, and Branch untouched, since nothing else derives from Name
// after creation. If the window title is still the plain session name (no
// manual tmux rename-window), it's updated to match; a user's own manual
// rename is preserved, same as SetSessionStatusTitle.
func (a *App) RenameSession(id, newName string) (session.Session, error) {
	s, ok := a.Store.Get(id)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown session %q", id)
	}
	newName = sanitizeName(newName)
	if newName == s.Name {
		return s, nil
	}
	if _, exists := a.Store.Get(session.MakeID(s.Project, newName)); exists {
		return session.Session{}, fmt.Errorf("session %q already exists in project %q", newName, s.Project)
	}
	oldTmux := s.TmuxSession
	newTmux := TmuxSessionName(s.ID, newName)
	if has, err := a.Tmux.HasSession(oldTmux); err == nil && has {
		if current, err := a.Tmux.WindowName(oldTmux); err == nil {
			if stripped := stripStatusGlyph(current); stripped == s.Name {
				_ = a.Tmux.SetWindowName(oldTmux, current[:len(current)-len(stripped)]+newName)
			}
		}
		if err := a.Tmux.RenameSession(oldTmux, newTmux); err != nil {
			return session.Session{}, fmt.Errorf("tmux rename: %w", err)
		}
	}
	s.Name = newName
	s.TmuxSession = newTmux
	if err := a.Store.Put(s); err != nil {
		return s, fmt.Errorf("store: %w", err)
	}
	return s, nil
}

func (a *App) SetSessionAgent(id, agent string, dangerous bool) (session.Session, error) {
	if err := validateAgent(agent); err != nil {
		return session.Session{}, err
	}
	s, ok := a.Store.Get(id)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown session %q", id)
	}
	previous := s
	s.Agent = agent
	s.Dangerous = dangerous
	if err := a.Store.Put(s); err != nil {
		_ = a.Store.Put(previous)
		return session.Session{}, fmt.Errorf("store: %w", err)
	}
	return s, nil
}

// SetSessionArchived hides (or restores) a session from the default list
// without touching its tmux session or worktree — the reverse of
// DeleteSession, which is destructive.
func (a *App) SetSessionArchived(id string, archived bool) (session.Session, error) {
	return a.Store.SetArchived(id, archived)
}

// repairAgentSupport re-runs s's agent installers on open, backfilling
// sessions created before a given integration existed. See
// installAgentSupport — this only adds the "still a known project" guard.
func (a *App) repairAgentSupport(s session.Session) string {
	if _, ok := a.Cfg.Projects[s.Project]; !ok {
		return ""
	}
	return installAgentSupport(s.AgentName())
}

// codexHooksHint returns a message telling the user how to activate the
// codex hooks that were just installed or changed, or "" if agent isn't
// codex. Codex requires an explicit `/hooks` review before it will run a new
// or changed hook entry (trust is keyed by the file's path and per-entry
// content hash — see codexhook.EnsureHooks's doc comment) — without this
// nudge, needs-input would silently never activate for the new/changed
// entry and there'd be no indication why. Callers only call this when
// needsInputInstallers reported changed=true, so it naturally re-fires
// whenever a future moomux release adds another hook event, not just once
// ever.
func codexHooksHint(agent string) string {
	if agent != "codex" {
		return ""
	}
	return "Codex needs-input hooks were installed/updated in ~/.codex/hooks.json — run /hooks inside Codex once to trust them"
}

// joinHints combines two non-empty hint strings for display, e.g. a
// one-time educational note alongside a manual-attach fallback. Either may
// be empty.
func joinHints(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "; " + b
	}
}

func (a *App) OpenSession(id string) (string, error) {
	s, ok := a.Store.Get(id)
	if !ok {
		return "", fmt.Errorf("unknown session %q", id)
	}
	hooksHint := a.repairAgentSupport(s)
	has, err := a.Tmux.HasSession(s.TmuxSession)
	slog.Info("open session", "id", id, "tmux_session", s.TmuxSession, "worktree", s.WorktreePath, "tmux_has_session", has)
	if err != nil {
		slog.Error("HasSession error", "id", id, "err", err)
		return "", err
	}
	if has {
		if cwd, err := a.Tmux.PaneCwd(s.TmuxSession); err == nil && cwd != s.WorktreePath {
			slog.Info("tmux session cwd mismatch, recreating", "tmux_session", s.TmuxSession, "pane_cwd", cwd, "want", s.WorktreePath)
			if err := a.Tmux.KillSession(s.TmuxSession); err != nil {
				slog.Error("KillSession failed", "id", id, "err", err)
				return "", err
			}
			has = false
		}
	}
	if !has {
		if want := TmuxSessionName(s.ID, s.Name); s.TmuxSession != want {
			// Lazy migration to the collision-free naming scheme: rename
			// only while the tmux session is dead and being recreated
			// anyway, so a live session never loses its identifier.
			slog.Info("migrating tmux session name", "from", s.TmuxSession, "to", want)
			s.TmuxSession = want
			if err := a.Store.Put(s); err != nil {
				return "", fmt.Errorf("store tmux name: %w", err)
			}
		}
		slog.Info("tmux session absent, recreating", "tmux_session", s.TmuxSession, "cwd", s.WorktreePath)
		cmd := buildAgentCmd(s.AgentName(), s.Dangerous)
		if s.AgentName() == "opencode" {
			if s.AgentPort == 0 {
				s.AgentPort = a.nextOpenCodePort()
				if err := a.Store.Put(s); err != nil {
					return "", fmt.Errorf("store opencode port: %w", err)
				}
			}
			cmd = fmt.Sprintf("opencode --port %d", s.AgentPort)
		}
		if err := a.newTmuxSession(s.TmuxSession, s.WorktreePath, cmd, s.Name); err != nil {
			slog.Error("NewSession failed", "id", id, "tmux_session", s.TmuxSession, "cwd", s.WorktreePath, "err", err)
			return "", err
		}
	}
	a.Tmux.ConfigureTitleTracking(s.TmuxSession, s.Name)
	var tabID, hint string
	if browser.Remote() {
		// Over SSH, the desktop terminal (iTerm/kitty/etc.) lives on a
		// different machine than this process — jumping to or opening a
		// tab there would target the wrong host. The tmux session is
		// already up; just point the user at it.
		hint = fmt.Sprintf("tmux attach -t %s", s.TmuxSession)
	} else {
		tabID, hint, err = a.openTerminal(s.TermTabID, s.TmuxSession, s.Name)
		if err != nil {
			// The tmux session is up regardless; give the user a way in.
			slog.Error("Terminal.OpenSession failed", "id", id, "tmux_session", s.TmuxSession, "name", s.Name, "err", err)
			hint = fmt.Sprintf("couldn't open a terminal (%v) — attach yourself: tmux attach -t %s", err, s.TmuxSession)
		}
	}
	if hooksHint != "" {
		hint = joinHints(hooksHint, hint)
	}
	s.TermTabID = tabID
	s.LastOpened = time.Now()
	if err := a.Store.Put(s); err != nil {
		slog.Error("store last-opened failed", "id", id, "err", err)
	}
	slog.Info("session opened", "id", id)
	return hint, nil
}

// openTerminal opens tmuxSession in a terminal, reusing tabID if the
// terminal supports jumping back to a specific tab (currently just
// iTerm2). Returns the tab id to remember for next time — empty for
// terminals without tab addressing.
func (a *App) openTerminal(tabID, tmuxSession, name string) (newTabID, hint string, err error) {
	if reopener, ok := a.Terminal.(terminal.TabReopener); ok {
		return reopener.OpenTab(tabID, tmuxSession, name)
	}
	hint, err = a.Terminal.OpenSession(tmuxSession, name)
	return "", hint, err
}

// TmuxAliveAll returns id→alive for every stored session using a single
// tmux list-sessions call instead of one has-session subprocess per session.
func (a *App) TmuxAliveAll() map[string]bool {
	live := a.Tmux.LiveSessions()
	all := a.Store.All()
	result := make(map[string]bool, len(all))
	for _, s := range all {
		result[s.ID] = live[s.TmuxSession]
	}
	return result
}

// KillTmux kills the tmux session but keeps the moomux session entry (and
// its worktree) intact, so it can be re-opened later. Also closes the
// session's terminal tab, if the terminal supports addressing one (see
// terminal.TabCloser) and this session has one recorded — otherwise a dead
// tmux session leaves a stale, unresponsive tab behind. TermTabID is
// cleared on success so a later reopen creates a fresh tab instead of
// chasing a handle that's gone.
//
// The tmux session is killed before the terminal tab is closed. `moomux
// park` runs this method in a detached helper so it survives killing its
// own pane; closing the tab first can otherwise terminate the command and
// all of its descendants before the tmux kill is issued.
func (a *App) KillTmux(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	slog.Debug("KillTmux: starting", "id", id, "pid", os.Getpid(), "tmux_session", s.TmuxSession, "term_tab_id", s.TermTabID)
	has, err := a.Tmux.HasSession(s.TmuxSession)
	if err != nil {
		return err
	}
	if has {
		if err := a.Tmux.KillSession(s.TmuxSession); err != nil {
			return err
		}
	}
	if closer, ok := a.Terminal.(terminal.TabCloser); ok && s.TermTabID != "" {
		err := closer.CloseTab(s.TermTabID)
		slog.Debug("KillTmux: CloseTab returned", "id", id, "tab_id", s.TermTabID, "err", err)
		if err != nil {
			slog.Warn("close terminal tab failed", "id", id, "tab_id", s.TermTabID, "err", err)
		}
		s.TermTabID = ""
		if err := a.Store.Put(s); err != nil {
			slog.Warn("clear terminal tab id failed", "id", id, "err", err)
		}
	}
	return nil
}

func (a *App) validateProject(name string, p *config.Project) error {
	if name == "" {
		return fmt.Errorf("project name required")
	}
	if strings.ContainsAny(name, " \t/\\") {
		return fmt.Errorf("project name cannot contain spaces or slashes")
	}
	if _, exists := a.Cfg.Projects[name]; exists {
		return fmt.Errorf("project %q already exists", name)
	}
	if p.Repo == "" {
		return fmt.Errorf("repo path required")
	}
	// p.Agent == "" is a legitimate "use the default" value at rest (see
	// AgentName), so validate the resolved name rather than the raw field —
	// only a genuinely bogus value (e.g. a config typo) should be rejected.
	if err := validateAgent(p.AgentName()); err != nil {
		return err
	}
	if p.BaseBranch == "" {
		p.BaseBranch = "main"
	}
	p.Repo = config.ExpandHome(p.Repo)
	return nil
}

func (a *App) saveProject(name string, p config.Project) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	if a.Cfg.Projects == nil {
		a.Cfg.Projects = map[string]config.Project{}
	}
	a.Cfg.Projects[name] = p
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		delete(a.Cfg.Projects, name)
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (a *App) AddProject(name string, p config.Project) error {
	if err := a.validateProject(name, &p); err != nil {
		return err
	}
	if err := gitwt.IsRepo(p.Repo); err != nil {
		return err
	}
	p.Kind = "git"
	return a.saveProject(name, p)
}

// InitProjectAndAdd creates the directory (if missing), runs `git init` with the
// given base branch + an empty initial commit, then saves the project.
func (a *App) InitProjectAndAdd(name string, p config.Project) error {
	if err := a.validateProject(name, &p); err != nil {
		return err
	}
	if err := gitwt.Init(p.Repo, p.BaseBranch); err != nil {
		return err
	}
	p.Kind = "git"
	return a.saveProject(name, p)
}

// AddPlainProject saves a non-git project. Sessions run directly in the
// project folder; no branches, no worktrees, no per-session isolation.
func (a *App) AddPlainProject(name string, p config.Project) error {
	if err := a.validateProject(name, &p); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Repo, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", p.Repo, err)
	}
	p.Kind = "plain"
	p.BaseBranch = ""
	p.BranchPrefix = ""
	return a.saveProject(name, p)
}

func (a *App) UpdateProject(name string, updated config.Project) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	previous, ok := a.Cfg.Projects[name]
	if !ok {
		return fmt.Errorf("unknown project %q", name)
	}
	// updated.Agent == "" is a legitimate "use the default" value at rest
	// (see AgentName), same as in validateProject — validate the resolved
	// name rather than the raw field.
	if err := validateAgent(updated.AgentName()); err != nil {
		return err
	}
	if updated.Repo == "" {
		return fmt.Errorf("repo path required")
	}
	updated.Repo = config.ExpandHome(updated.Repo)
	updated.Kind = previous.Kind

	if updated.NoWorktree != previous.NoWorktree {
		// Existing sessions were created under the old mode; their
		// WorktreePath either is or isn't the repo itself, and deleting
		// them under the flipped mode would target the wrong path.
		for _, s := range a.Store.All() {
			if s.Project == name {
				return fmt.Errorf("cannot change worktree mode while project %q has sessions — delete them first", name)
			}
		}
	}

	if previous.IsPlain() {
		if err := os.MkdirAll(updated.Repo, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", updated.Repo, err)
		}
		updated.BaseBranch = ""
		updated.BranchPrefix = ""
		updated.NoWorktree = false
	} else {
		if err := gitwt.IsRepo(updated.Repo); err != nil {
			return err
		}
		if updated.BaseBranch == "" {
			updated.BaseBranch = "main"
		}
	}

	a.Cfg.Projects[name] = updated
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.Projects[name] = previous
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// SetTheme persists the chosen theme name and appearance override to config,
// following the same reload -> mutate -> save idiom as UpdateProject.
func (a *App) SetTheme(theme, appearance string) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	prevTheme, prevAppearance := a.Cfg.Theme, a.Cfg.Appearance
	a.Cfg.Theme = theme
	a.Cfg.Appearance = appearance
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.Theme = prevTheme
		a.Cfg.Appearance = prevAppearance
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// SetAutoSubmitDefault persists the remembered default for the new-session
// form's auto-submit toggle, following the same reload -> mutate -> save
// idiom as SetTheme.
func (a *App) SetAutoSubmitDefault(autoSubmit bool) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	prev := a.Cfg.AutoSubmitDefault
	a.Cfg.AutoSubmitDefault = autoSubmit
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.AutoSubmitDefault = prev
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// SetSortRecentFirst persists the session list's sort mode (manual Order vs.
// most-recently-opened first), following the same reload -> mutate -> save
// idiom as SetTheme.
func (a *App) SetSortRecentFirst(recentFirst bool) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	prev := a.Cfg.SortRecentFirst
	a.Cfg.SortRecentFirst = recentFirst
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.SortRecentFirst = prev
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// SetCompactDetail persists whether the detail panel trims itself to the
// fields most useful at a glance, following the same reload -> mutate ->
// save idiom as SetSortRecentFirst.
func (a *App) SetCompactDetail(compact bool) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	prev := a.Cfg.CompactDetail
	a.Cfg.CompactDetail = compact
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.CompactDetail = prev
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// SetAutoTmux persists whether moomux always relaunches itself inside a
// dedicated tmux session on startup, following the same reload -> mutate ->
// save idiom as SetSortRecentFirst. Previously this was only ever set once,
// interactively, by promptAutoTmux in main.go before the TUI starts; this
// gives it a second, always-available entry point via the settings screen.
func (a *App) SetAutoTmux(autoTmux bool) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	prev := a.Cfg.AutoTmux
	a.Cfg.AutoTmux = autoTmux
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.AutoTmux = prev
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (a *App) RemoveProject(name string) error {
	if err := config.Reload(a.CfgPath, a.Cfg); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	if _, ok := a.Cfg.Projects[name]; !ok {
		return fmt.Errorf("unknown project %q", name)
	}
	for _, s := range a.Store.All() {
		if s.Project == name {
			return fmt.Errorf("project %q still has sessions (incl. archived) — delete them first", name)
		}
	}
	saved := a.Cfg.Projects[name]
	delete(a.Cfg.Projects, name)
	if err := config.Save(a.CfgPath, a.Cfg); err != nil {
		a.Cfg.Projects[name] = saved
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// pathWithin reports whether path is strictly inside root.
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WorktreeStatus reports id's worktree as dirty (uncommitted changes) and/or
// unpushed (commits missing from its upstream, including no upstream at all).
// ok is false when status can't be determined (unknown session, or the
// worktree isn't a git repo), in which case dirty/unpushed are meaningless.
func (a *App) WorktreeStatus(id string) (dirty, unpushed, ok bool) {
	s, exists := a.Store.Get(id)
	if !exists {
		return false, false, false
	}
	clean, err := a.Git.IsWorktreeClean(s.WorktreePath)
	if err != nil {
		return false, false, false
	}
	hasUnpushed, err := a.Git.HasUnpushedCommits(s.WorktreePath)
	if err != nil {
		return !clean, false, true
	}
	return !clean, hasUnpushed, true
}

// ChangeSummary reports counts to back the delete-confirmation dialog's
// warning detail: how many files have uncommitted changes and how many
// commits are unpushed. Separate from WorktreeStatus (rather than folding
// the counts in there) since it's only fetched on-demand when the delete
// dialog opens, not on WorktreeStatus's routine per-session polling tick.
// ok is false when it can't be determined.
func (a *App) ChangeSummary(id string) (filesChanged, unpushedCommits int, ok bool) {
	s, exists := a.Store.Get(id)
	if !exists {
		return 0, 0, false
	}
	files, err := a.Git.FilesChangedCount(s.WorktreePath)
	if err != nil {
		return 0, 0, false
	}
	commits, err := a.Git.UnpushedCommitCount(s.WorktreePath)
	if err != nil {
		return files, 0, true
	}
	return files, commits, true
}

// PRStatus reports the merge/CI status of id's attached PR. ok is false when
// the session has no PR attached, or the lookup fails (gh not installed, not
// authenticated, or the PR can't be resolved).
func (a *App) PRStatus(id string) (prstatus.Info, bool) {
	s, exists := a.Store.Get(id)
	if !exists || s.PR == "" {
		return prstatus.Info{}, false
	}
	info, err := a.PR.Fetch(s.PR)
	if err != nil {
		return prstatus.Info{}, false
	}
	return info, true
}

// DeleteSession removes the session's worktree, branch, and store entry. The
// returned hint, when non-empty, is a user-facing message worth surfacing
// (currently: output printed by worktree-delete userscripts).
func (a *App) DeleteSession(id string) (string, error) {
	s, ok := a.Store.Get(id)
	if !ok {
		return "", fmt.Errorf("unknown session %q", id)
	}
	if has, _ := a.Tmux.HasSession(s.TmuxSession); has {
		if err := a.Tmux.KillSession(s.TmuxSession); err != nil {
			return "", fmt.Errorf("tmux kill-session: %w", err)
		}
	}
	var hint string
	if proj, ok := a.Cfg.Projects[s.Project]; ok {
		if proj.UsesWorktree() {
			if home, err := os.UserHomeDir(); err != nil {
				slog.Warn("userscript run skipped", "err", err)
			} else {
				for _, w := range userscript.RunWorktreeDelete(home, userscript.Env{
					Project:  s.Project,
					Worktree: s.WorktreePath,
					Repo:     proj.Repo,
					Branch:   s.Branch,
				}) {
					slog.Warn("userscript", "warning", w)
					hint = joinHints(hint, w)
				}
			}
			if err := a.Git.RemoveWorktree(proj.Repo, s.WorktreePath); err != nil {
				return hint, fmt.Errorf("remove worktree: %w", err)
			}
			if s.NewBranch && s.Branch != "" {
				_ = a.Git.DeleteBranch(proj.Repo, s.Branch)
			}
		}
	} else if pathWithin(a.WorktreeRoot, s.WorktreePath) {
		// Project gone from config (e.g. hand-edited TOML). Only clean up
		// paths moomux itself created — for plain/no-worktree sessions
		// WorktreePath is the user's real project folder, and deleting it
		// here would wipe their repo.
		_ = os.RemoveAll(s.WorktreePath)
	}
	return hint, a.Store.Delete(id)
}
