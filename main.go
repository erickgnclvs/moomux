package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erickgnclvs/moomux/internal/app"
	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/gitwt"
	"github.com/erickgnclvs/moomux/internal/session"
	"github.com/erickgnclvs/moomux/internal/terminal"
	"github.com/erickgnclvs/moomux/internal/tmux"
	"github.com/erickgnclvs/moomux/internal/tui"
	"github.com/erickgnclvs/moomux/internal/watcher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "moomux:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", cfgPath, err)
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := seedExampleConfig(cfgPath); err != nil {
			return err
		}
	}

	store := &session.Store{Path: session.DefaultPath()}
	if err := store.Load(); err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}

	a := &app.App{
		Cfg:          cfg,
		CfgPath:      cfgPath,
		Store:        store,
		Tmux:         tmux.New(),
		Terminal:     terminal.Detect(),
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
	example := `# moomux configuration
# Add one [projects.<name>] section per repo you want to manage.

# [projects.eg_system]
# repo          = "~/Development/eg_system"
# branch_prefix = "erickgoncalves"   # optional — prepended to branch names
# base_branch   = "main"
`
	return os.WriteFile(path, []byte(example), 0o644)
}
