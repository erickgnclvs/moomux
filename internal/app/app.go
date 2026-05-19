// Package app glues config, session store, tmux, iterm and gitwt into a TUI Backend.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/erickgnclvs/curral/internal/config"
	"github.com/erickgnclvs/curral/internal/gitwt"
	"github.com/erickgnclvs/curral/internal/iterm"
	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/tmux"
)

type App struct {
	Cfg          *config.Config
	Store        *session.Store
	Tmux         *tmux.Client
	ITerm        *iterm.Client
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
	branch := name
	if proj.BranchPrefix != "" {
		branch = proj.BranchPrefix + "/" + name
	}
	wt := filepath.Join(a.WorktreeRoot, project, name)
	tmuxName := "curral-" + name

	if err := a.Git.Fetch(proj.Repo, proj.BaseBranch); err != nil {
		return session.Session{}, fmt.Errorf("git fetch: %w", err)
	}
	if err := a.Git.AddWorktree(proj.Repo, wt, branch, proj.BaseBranch); err != nil {
		return session.Session{}, fmt.Errorf("git worktree add: %w", err)
	}
	if err := a.Tmux.NewSession(tmuxName, wt, "claude"); err != nil {
		return session.Session{}, fmt.Errorf("tmux new-session: %w", err)
	}
	if err := a.ITerm.OpenTab(tmuxName); err != nil {
		return session.Session{}, fmt.Errorf("iterm open tab: %w", err)
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
		if err := a.Tmux.NewSession(s.TmuxSession, s.WorktreePath, "claude"); err != nil {
			return err
		}
	}
	return a.ITerm.OpenTab(s.TmuxSession)
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

func (a *App) DeleteSession(id string) error {
	s, ok := a.Store.Get(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	if has, _ := a.Tmux.HasSession(s.TmuxSession); has {
		_ = a.Tmux.KillSession(s.TmuxSession)
	}
	proj, ok := a.Cfg.Projects[s.Project]
	if ok {
		_ = a.Git.RemoveWorktree(proj.Repo, s.WorktreePath)
	}
	return a.Store.Delete(id)
}
