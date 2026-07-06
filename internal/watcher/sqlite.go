package watcher

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SQLiteWatcher polls an SQLite database for agent activity.
// Query must return two columns: (path TEXT, updated_ms INTEGER) where
// updated_ms is a Unix timestamp in milliseconds.
type SQLiteWatcher struct {
	DB        string        // exact path or glob (e.g. ~/.codex/state_*.sqlite)
	Query     string        // SELECT path_col, updated_ms_col FROM ... GROUP BY path_col
	ActiveAge time.Duration // within this age = Working; default 10s
	Interval  time.Duration // poll interval; default 2s
}

func (w *SQLiteWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	activeAge := w.ActiveAge
	if activeAge == 0 {
		activeAge = 10 * time.Second
	}
	interval := w.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	w.tick(ctx, out, activeAge)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out, activeAge)
		}
	}
}

func (w *SQLiteWatcher) tick(ctx context.Context, out chan<- Snapshot, activeAge time.Duration) {
	snap := Snapshot{States: map[string]State{}, PollTime: time.Now()}

	dbPaths, err := filepath.Glob(w.DB)
	if err != nil {
		snap.Err = fmt.Errorf("glob %s: %w", w.DB, err)
		send(ctx, out, snap)
		return
	}
	if len(dbPaths) == 0 {
		// No matching DB yet (e.g. agent hasn't started); not an error.
		send(ctx, out, snap)
		return
	}

	var queryErrs []error
	now := time.Now()
	for _, dbPath := range dbPaths {
		rows, err := querySQLite(dbPath, w.Query)
		if err != nil {
			queryErrs = append(queryErrs, fmt.Errorf("query %s: %w", dbPath, err))
			continue
		}
		for path, updatedMs := range rows {
			age := now.Sub(time.UnixMilli(updatedMs))
			if age <= activeAge {
				snap.States[path] = Working
			} else {
				snap.States[path] = Waiting
			}
		}
	}
	if len(queryErrs) > 0 {
		snap.Err = errors.Join(queryErrs...)
	}
	send(ctx, out, snap)
}

// querySQLite runs a query via the sqlite3 CLI and returns map[path]updated_ms.
// It returns an error if the subprocess fails, so callers can distinguish a
// transient query failure from a genuinely empty result set.
func querySQLite(dbPath, query string) (map[string]int64, error) {
	out, err := exec.Command("sqlite3", "-separator", "\t", dbPath, query).Output()
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}
		result[strings.TrimSpace(parts[0])] = ms
	}
	return result, nil
}
