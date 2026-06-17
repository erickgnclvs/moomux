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

// set by goreleaser via ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("moomux %s (%s) built %s\n", version, commit, date)
		return
	}
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
	multi := buildWatcher(home)
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

func buildWatcher(home string) watcher.Watcher {
	return &watcher.MultiWatcher{Watchers: []watcher.Watcher{
		// Claude Code: JSON session files in ~/.claude/sessions/
		&watcher.DirWatcher{Dir: filepath.Join(home, ".claude", "sessions")},
		// Codex: activity tracked in SQLite DB (~/.codex/state_N.sqlite)
		&watcher.SQLiteWatcher{
			DB:    filepath.Join(home, ".codex", "state_*.sqlite"),
			Query: "SELECT cwd, MAX(updated_at_ms) FROM threads GROUP BY cwd",
		},
		// OpenCode: activity tracked in SQLite DB (~/.local/share/opencode/opencode.db)
		&watcher.SQLiteWatcher{
			DB:    filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
			Query: "SELECT directory, MAX(time_updated) FROM session GROUP BY directory",
		},
	}}
}
