package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/curral/internal/app"
	"github.com/erickgnclvs/curral/internal/config"
	"github.com/erickgnclvs/curral/internal/gitwt"
	"github.com/erickgnclvs/curral/internal/iterm"
	"github.com/erickgnclvs/curral/internal/session"
	"github.com/erickgnclvs/curral/internal/tmux"
	"github.com/erickgnclvs/curral/internal/tui"
	"github.com/erickgnclvs/curral/internal/watcher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "curral:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", cfgPath, err)
	}
	if len(cfg.Projects) == 0 {
		if err := seedExampleConfig(cfgPath); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote example config to %s — edit it and re-run curral.\n", cfgPath)
		return nil
	}

	store := &session.Store{Path: session.DefaultPath()}
	if err := store.Load(); err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}

	a := &app.App{
		Cfg:          cfg,
		Store:        store,
		Tmux:         tmux.New(),
		ITerm:        iterm.New(),
		Git:          gitwt.New(),
		WorktreeRoot: app.WorktreeRootDefault(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	statusCh := make(chan watcher.Snapshot, 4)
	home, _ := os.UserHomeDir()
	w := &watcher.Watcher{
		Dir: filepath.Join(home, ".claude", "sessions"),
	}
	go w.Run(ctx, statusCh)

	m := tui.New(cfg, a, statusCh, cancel)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		cancel()
		return err
	}
	cancel()
	return nil
}

func seedExampleConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	example := `# curral configuration
# Add one [projects.<name>] section per repo you want to manage.

# [projects.eg_system]
# repo          = "~/Development/eg_system"
# branch_prefix = "erickgoncalves"   # optional — prepended to branch names
# base_branch   = "main"
`
	return os.WriteFile(path, []byte(example), 0o644)
}
