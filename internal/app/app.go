// Package app glues config, session store, tmux, terminal and gitwt into a TUI Backend.
package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/terminal"
	"github.com/erickgnclvs/moomux/internal/tmux"
)

type App struct {
	Cfg          *config.Config
	CfgPath      string
	Store        *session.Store
	Tmux         *tmux.Client
	Terminal     terminal.TerminalOpener
	Git          *gitwt.Client
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

func WorktreeRootDefault() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "moomux", "worktrees")
}

// nextOpenCodePort returns the next available port for an OpenCode session.
// Starts at 4096 and increments past any port already in use by existing OpenCode sessions.
func (a *App) nextOpenCodePort() int {
	port := 4096
	for _, s := range a.Store.All() {
		if s.AgentName() == "opencode" && s.AgentPort >= port {
			port = s.AgentPort + 1
		}
	}
	return port
}

func (a *App) Projects() []string {
	out := make([]string, 0, len(a.Cfg.Projects))
	for k := range a.Cfg.Projects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *App) Sessions() []session.Session { return a.Store.All() }

// deriveNameFromBranch turns a branch name like "feature/login-page" into a
// filesystem/tmux-safe session name like "login-page".
func deriveNameFromBranch(branch string) string {
	name := branch
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
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

// CreateSession's hint, when non-empty, is a user-facing instruction
// (e.g. "run: tmux attach -t ...") to show alongside success — it is
// not an error.
func (a *App) CreateSession(project, name, agent, existingBranch, ticket string) (session.Session, string, error) {
	proj, ok := a.Cfg.Projects[project]
	if !ok {
		return session.Session{}, "", fmt.Errorf("unknown project %q", project)
	}
	if name == "" {
		if existingBranch == "" {
			return session.Session{}, "", fmt.Errorf("session name required")
		}
		name = a.uniqueNameFromBranch(project, existingBranch)
	}
	if agent == "" {
		agent = proj.AgentName()
	}
	var wt string
	tmuxName := "moomux-" + name
	branch := ""

	if proj.IsPlain() {
		wt = proj.Repo
	} else {
		wt = filepath.Join(a.WorktreeRoot, project, name)
	}

	slog.Info("create session", "project", project, "name", name, "agent", agent, "worktree", wt, "branch", existingBranch)

	if !proj.IsPlain() {
		fetchTarget := proj.BaseBranch
		if existingBranch != "" {
			branch = existingBranch
			fetchTarget = existingBranch
		} else {
			branch = name
			if proj.BranchPrefix != "" {
				branch = proj.BranchPrefix + "/" + name
			}
		}
		if a.Git.HasRemote(proj.Repo, "origin") {
			_ = a.Git.Fetch(proj.Repo, fetchTarget) // best-effort
		}
		var err error
		if existingBranch != "" {
			err = a.Git.AddWorktreeExisting(proj.Repo, wt, branch)
		} else {
			err = a.Git.AddWorktree(proj.Repo, wt, branch, proj.BaseBranch)
		}
		if err != nil {
			slog.Error("git worktree add failed", "repo", proj.Repo, "path", wt, "branch", branch, "err", err)
			return session.Session{}, "", fmt.Errorf("git worktree add: %w", err)
		}
		slog.Info("worktree added", "path", wt, "branch", branch)
	}
	cmd := agentCmd(agent)
	agentPort := 0
	if agent == "opencode" {
		agentPort = a.nextOpenCodePort()
		cmd = fmt.Sprintf("opencode --port %d", agentPort)
	}

	if err := a.Tmux.NewSession(tmuxName, wt, cmd, name); err != nil {
		slog.Error("tmux new-session failed", "name", tmuxName, "cwd", wt, "err", err)
		return session.Session{}, "", fmt.Errorf("tmux new-session: %w", err)
	}
	slog.Info("tmux session created", "name", tmuxName)
	hint, err := a.Terminal.OpenSession(tmuxName, name)
	if err != nil {
		slog.Error("terminal open failed", "tmux_session", tmuxName, "name", name, "err", err)
		return session.Session{}, "", fmt.Errorf("terminal open: %w", err)
	}
	slog.Info("terminal opened", "tmux_session", tmuxName)

	s := session.Session{
		ID:           session.MakeID(project, name),
		Project:      project,
		Name:         name,
		Branch:       branch,
		NewBranch:    !proj.IsPlain() && existingBranch == "",
		WorktreePath: wt,
		TmuxSession:  tmuxName,
		CreatedAt:    time.Now().UTC(),
		Agent:        agent,
		AgentPort:    agentPort,
		Ticket:       ticket,
	}
	if err := a.Store.Put(s); err != nil {
		slog.Error("store put failed", "id", s.ID, "err", err)
		return s, "", fmt.Errorf("store: %w", err)
	}
	return s, hint, nil
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

// SetSessionArchived hides (or restores) a session from the default list
// without touching its tmux session or worktree — the reverse of
// DeleteSession, which is destructive.
func (a *App) SetSessionArchived(id string, archived bool) (session.Session, error) {
	return a.Store.SetArchived(id, archived)
}

func (a *App) OpenSession(id string) (string, error) {
	s, ok := a.Store.Get(id)
	if !ok {
		return "", fmt.Errorf("unknown session %q", id)
	}
	has, err := a.Tmux.HasSession(s.TmuxSession)
	slog.Info("open session", "id", id, "tmux_session", s.TmuxSession, "worktree", s.WorktreePath, "tmux_has_session", has)
	if err != nil {
		slog.Error("HasSession error", "id", id, "err", err)
		return "", err
	}
	if !has {
		slog.Info("tmux session absent, recreating", "tmux_session", s.TmuxSession, "cwd", s.WorktreePath)
		cmd := agentCmd(s.AgentName())
		if s.AgentName() == "opencode" && s.AgentPort > 0 {
			cmd = fmt.Sprintf("opencode --port %d", s.AgentPort)
		}
		if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, cmd, s.Name); err != nil {
			slog.Error("NewSession failed", "id", id, "tmux_session", s.TmuxSession, "cwd", s.WorktreePath, "err", err)
			return "", err
		}
	}
	a.Tmux.ConfigureTitleTracking(s.TmuxSession, s.Name)
	hint, err := a.Terminal.OpenSession(s.TmuxSession, s.Name)
	if err != nil {
		slog.Error("Terminal.OpenSession failed", "id", id, "tmux_session", s.TmuxSession, "name", s.Name, "err", err)
		return "", err
	}
	slog.Info("session opened", "id", id)
	return hint, nil
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

// KillTmux kills the tmux session but keeps the moomux session entry
// (and its worktree) intact, so it can be re-opened later.
func (a *App) KillTmux(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	has, err := a.Tmux.HasSession(s.TmuxSession)
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	return a.Tmux.KillSession(s.TmuxSession)
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
	if p.BaseBranch == "" {
		p.BaseBranch = "main"
	}
	p.Repo = expandHome(p.Repo)
	return nil
}

func (a *App) saveProject(name string, p config.Project) error {
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

func (a *App) RemoveProject(name string) error {
	if _, ok := a.Cfg.Projects[name]; !ok {
		return fmt.Errorf("unknown project %q", name)
	}
	for _, s := range a.Store.All() {
		if s.Project == name {
			return fmt.Errorf("project %q has active sessions — delete them first", name)
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

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

func (a *App) DeleteSession(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	if has, _ := a.Tmux.HasSession(s.TmuxSession); has {
		if err := a.Tmux.KillSession(s.TmuxSession); err != nil {
			return fmt.Errorf("tmux kill-session: %w", err)
		}
	}
	if proj, ok := a.Cfg.Projects[s.Project]; ok {
		if !proj.IsPlain() {
			_ = a.Git.RemoveWorktree(proj.Repo, s.WorktreePath)
			if s.NewBranch && s.Branch != "" {
				_ = a.Git.DeleteBranch(proj.Repo, s.Branch)
			}
		}
	} else {
		_ = os.RemoveAll(s.WorktreePath)
	}
	return a.Store.Delete(id)
}
