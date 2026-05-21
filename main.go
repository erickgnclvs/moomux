package main

import (
	"context"
	"fmt"
	"log/slog"
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

	store := &session.Store{Path: session.DefaultPath()}
	if err := store.Load(); err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".local", "share", "moomux")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "moomux.log")
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(lf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		defer lf.Close()
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
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
	multi := buildWatcher(home, store.All())
	go multi.Run(ctx, statusCh)

	m := tui.New(cfg, a, statusCh, cancel)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		cancel()
		return err
	}
	cancel()
	return nil
}

func buildWatcher(home string, sessions []session.Session) watcher.Watcher {
	watchers := []watcher.Watcher{
		&watcher.DirWatcher{Dir: filepath.Join(home, ".claude", "sessions")},
		&watcher.DirWatcher{Dir: filepath.Join(home, ".codex", "sessions")},
	}

	var ocEntries []watcher.OpenCodeEntry
	for _, s := range sessions {
		if s.AgentName() == "opencode" && s.AgentPort > 0 {
			ocEntries = append(ocEntries, watcher.OpenCodeEntry{
				WorktreePath: s.WorktreePath,
				URL:          fmt.Sprintf("http://127.0.0.1:%d", s.AgentPort),
			})
		}
	}
	if len(ocEntries) > 0 {
		watchers = append(watchers, &watcher.OpenCodeWatcher{Entries: ocEntries})
	}

	return &watcher.MultiWatcher{Watchers: watchers}
}
