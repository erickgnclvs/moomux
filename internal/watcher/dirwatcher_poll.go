//go:build !darwin

package watcher

import (
	"context"
	"time"
)

// Run polls Dir every Interval (default 2s) until ctx is canceled. Each tick
// produces one Snapshot on out. This is the fallback path used on every
// platform except macOS, which watches Dir for filesystem events instead
// (see dirwatcher_fsevents.go).
func (w *DirWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	interval := w.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	w.tick(ctx, out)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out)
		}
	}
}
