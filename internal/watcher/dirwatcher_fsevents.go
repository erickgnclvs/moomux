//go:build darwin

package watcher

import (
	"context"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// fallbackInterval is a low-frequency safety-net poll once the fsnotify
	// watch is established — insurance against a missed kqueue event, not
	// the primary signal, so it doesn't reintroduce poll latency.
	fallbackInterval = 5 * time.Second
	// debounceWindow coalesces a burst of events (an agent writing several
	// session files back to back) into one rescan, so Run still emits one
	// snapshot per change rather than one per file.
	debounceWindow = 100 * time.Millisecond
)

// Run watches Dir for filesystem events (kqueue) instead of polling it on a
// timer, rescanning and emitting one Snapshot per change (debounced) or per
// fallback tick. If Dir doesn't exist yet, or a watcher can't be created, it
// polls at Interval (default 2s) until the watch can be established.
func (w *DirWatcher) Run(ctx context.Context, out chan<- Snapshot) {
	pollInterval := w.Interval
	if pollInterval == 0 {
		pollInterval = 2 * time.Second
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		w.pollUntilCanceled(ctx, out, pollInterval)
		return
	}
	defer fsw.Close()

	w.tick(ctx, out)

	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for fsw.Add(w.Dir) != nil {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, out)
		}
	}
	t.Stop()

	fallback := time.NewTicker(fallbackInterval)
	defer fallback.Stop()
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
		case _, ok := <-fsw.Events:
			if !ok {
				return
			}
			if debounce == nil {
				debounce = time.AfterFunc(debounceWindow, func() { w.tick(ctx, out) })
			} else {
				debounce.Reset(debounceWindow)
			}
		case _, ok := <-fsw.Errors:
			if !ok {
				return
			}
		case <-fallback.C:
			w.tick(ctx, out)
		}
	}
}

func (w *DirWatcher) pollUntilCanceled(ctx context.Context, out chan<- Snapshot, interval time.Duration) {
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
