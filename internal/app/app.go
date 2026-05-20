// Package app glues config, session store, tmux, terminal and gitwt into a TUI Backend.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/erickgnclvs/curral/internal/config"
	"github.com/erickgnclvs/curral/internal/gitwt"
	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/terminal"
	"github.com/erickgnclvs/curral/internal/tmux"
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
	return filepath.Join(home, ".local", "share", "curral", "worktrees")
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
	tmuxName := "curral-" + name
	branch := ""

	if proj.IsPlain() {
		if err := os.MkdirAll(wt, 0o755); err != nil {
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
			return session.Session{}, fmt.Errorf("git worktree add: %w", err)
		}
	}
	if err := a.Tmux.NewSession(tmuxName, wt, "claude", name); err != nil {
		return session.Session{}, fmt.Errorf("tmux new-session: %w", err)
	}
	if err := a.Terminal.OpenSession(tmuxName, branch); err != nil {
		return session.Session{}, fmt.Errorf("terminal open: %w", err)
	}

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
	if err != nil {
		return err
	}
	if !has {
		if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, "claude", s.Name); err != nil {
			return err
		}
	}
	return a.Terminal.OpenSession(s.TmuxSession, s.Branch)
}

// TmuxAlive reports whether the tmux session backing this curral session
// is currently running. Errors are treated as "not alive" so a flaky tmux
// CLI never paints a stale Working/Waiting dot.
func (a *App) TmuxAlive(id string) bool {
	s, ok := a.Store.Get(id)
	if !ok {
		return false
	}
	has, err := a.Tmux.HasSession(s.TmuxSession)
	if err != nil {
		return false
	}
	return has
}

// KillTmux kills the tmux session but keeps the curral session entry
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
