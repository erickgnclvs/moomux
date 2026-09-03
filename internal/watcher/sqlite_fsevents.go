//go:build darwin

package watcher

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// sqliteDebounce coalesces a burst of filesystem events (e.g. a SQLite
// checkpoint touching both the DB and its -wal file) into one extra tick.
const sqliteDebounce = 100 * time.Millisecond

// Run polls at Interval, same as everywhere else — see SQLiteWatcher's doc
// comment for why that periodic tick can't be dropped even on darwin. What
// this adds is watching the DB's directory and MarkerDir for changes and
// firing an extra out-of-cycle tick (debounced) on any event, so a session
// going busy, or a needs-input marker appearing, is reflected immediately
// instead of waiting out the rest of the current interval. A watch that
// can't be established yet (directory doesn't exist) is retried on every
// scheduled tick; until it succeeds, behavior is identical to the poll-only
// path.
func (w *SQLiteWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	activeAge := w.ActiveAge
	if activeAge == 0 {
		activeAge = 10 * time.Second
	}
	interval := w.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		w.pollOnly(ctx, out, activeAge, interval)
		return
	}
	defer fsw.Close()

	watchDirs := []string{filepath.Dir(w.DB)}
	if w.MarkerDir != "" {
		watchDirs = append(watchDirs, w.MarkerDir)
	}
	watched := make([]bool, len(watchDirs))
	tryWatch := func() {
		for i, dir := range watchDirs {
			if !watched[i] && fsw.Add(dir) == nil {
				watched[i] = true
			}
		}
	}
	tryWatch()

	t := time.NewTicker(interval)
	defer t.Stop()
	w.tick(ctx, out, activeAge)

	var debounce *time.Timer
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tryWatch()
			w.tick(ctx, out, activeAge)
		case _, ok := <-fsw.Events:
			if !ok {
				return
			}
			if debounce == nil {
				debounce = time.AfterFunc(sqliteDebounce, func() { w.tick(ctx, out, activeAge) })
			} else {
				debounce.Reset(sqliteDebounce)
			}
		case _, ok := <-fsw.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *SQLiteWatcher) pollOnly(ctx context.Context, out chan<- Snapshot, activeAge, interval time.Duration) {
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
