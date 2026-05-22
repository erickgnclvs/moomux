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

func WorktreeRootDefault() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "moomux", "worktrees")
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

func (a *App) CreateSession(project, name string) (session.Session, error) {
	proj, ok := a.Cfg.Projects[project]
	if !ok {
		return session.Session{}, fmt.Errorf("unknown project %q", project)
	}
	wt := filepath.Join(a.WorktreeRoot, project, name)
	tmuxName := "moomux-" + name
	branch := ""

	slog.Info("create session", "project", project, "name", name, "worktree", wt, "branch", branch)

	if proj.IsPlain() {
		if err := os.MkdirAll(wt, 0o755); err != nil {
			slog.Error("mkdir session dir failed", "path", wt, "err", err)
			return session.Session{}, fmt.Errorf("mkdir session dir: %w", err)
		}
	} else {
		branch = name
		if proj.BranchPrefix != "" {
			branch = proj.BranchPrefix + "/" + name
		}
		if a.Git.HasRemote(proj.Repo, "origin") {
			_ = a.Git.Fetch(proj.Repo, proj.BaseBranch) // best-effort
		}
		if err := a.Git.AddWorktree(proj.Repo, wt, branch, proj.BaseBranch); err != nil {
			slog.Error("git worktree add failed", "repo", proj.Repo, "path", wt, "branch", branch, "err", err)
			return session.Session{}, fmt.Errorf("git worktree add: %w", err)
		}
		slog.Info("worktree added", "path", wt, "branch", branch)
	}
	if err := a.Tmux.NewSession(tmuxName, wt, "claude", name); err != nil {
		slog.Error("tmux new-session failed", "name", tmuxName, "cwd", wt, "err", err)
		return session.Session{}, fmt.Errorf("tmux new-session: %w", err)
	}
	slog.Info("tmux session created", "name", tmuxName)
	if err := a.Terminal.OpenSession(tmuxName, name); err != nil {
		slog.Error("terminal open failed", "tmux_session", tmuxName, "name", name, "err", err)
		return session.Session{}, fmt.Errorf("terminal open: %w", err)
	}
	slog.Info("terminal opened", "tmux_session", tmuxName)

	s := session.Session{
		ID:           session.MakeID(project, name),
		Project:      project,
		Name:         name,
		Branch:       branch,
		WorktreePath: wt,
		TmuxSession:  tmuxName,
		CreatedAt:    time.Now().UTC(),
	}
	if err := a.Store.Put(s); err != nil {
		slog.Error("store put failed", "id", s.ID, "err", err)
		return s, fmt.Errorf("store: %w", err)
	}
	return s, nil
}

func (a *App) OpenSession(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	has, err := a.Tmux.HasSession(s.TmuxSession)
	slog.Info("open session", "id", id, "tmux_session", s.TmuxSession, "worktree", s.WorktreePath, "tmux_has_session", has)
	if err != nil {
		slog.Error("HasSession error", "id", id, "err", err)
		return err
	}
	if !has {
		slog.Info("tmux session absent, recreating", "tmux_session", s.TmuxSession, "cwd", s.WorktreePath)
		if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, "claude", s.Name); err != nil {
			slog.Error("NewSession failed", "id", id, "tmux_session", s.TmuxSession, "cwd", s.WorktreePath, "err", err)
			return err
		}
	}
	a.Tmux.ConfigureTitleTracking(s.TmuxSession, s.Name)
	if err := a.Terminal.OpenSession(s.TmuxSession, s.Name); err != nil {
		slog.Error("Terminal.OpenSession failed", "id", id, "tmux_session", s.TmuxSession, "name", s.Name, "err", err)
		return err
	}
	slog.Info("session opened", "id", id)
	return nil
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

// AddPlainProject saves a non-git project. Each session is just a subdirectory
// under WorktreeRoot; no branches, no worktrees.
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
		_ = a.Tmux.KillSession(s.TmuxSession)
	}
	if proj, ok := a.Cfg.Projects[s.Project]; ok {
		if proj.IsPlain() {
			_ = os.RemoveAll(s.WorktreePath)
		} else {
			_ = a.Git.RemoveWorktree(proj.Repo, s.WorktreePath)
		}
	} else {
		_ = os.RemoveAll(s.WorktreePath)
	}
	return a.Store.Delete(id)
}
