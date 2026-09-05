//go:build !darwin

package watcher

import (
	"context"
	"time"
)

// Run polls at Interval (default 2s) until ctx is canceled. Each tick
// re-queries the DB and re-scans MarkerDir together (see SQLiteWatcher's
// doc comment) and produces one Snapshot on out.
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
