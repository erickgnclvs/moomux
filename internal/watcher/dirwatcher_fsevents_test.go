//go:build darwin

package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDarwinWatcherIsEventDriven proves the fsevents path reacts to a file
// change well inside the debounce window rather than waiting out a long
// poll Interval. With the old poll-only Run, this would only see the
// snapshot after the 10s Interval and would time out.
func TestDarwinWatcherIsEventDriven(t *testing.T) {
	dir := t.TempDir()
	w := &DirWatcher{Dir: dir, Interval: 10 * time.Second}
	ch := make(chan Snapshot, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go w.Run(ctx, ch)

	// drain the initial tick's empty snapshot
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial snapshot")
	}

	writeJSON(t, filepath.Join(dir, "1.json"), map[string]any{
		"cwd": "/tmp/wt-a", "status": "busy",
	})

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case snap := <-ch:
			if snap.States["/tmp/wt-a"] == Working {
				return
			}
		case <-deadline:
			t.Fatal("did not observe new file within 500ms; watcher is not event-driven")
		case <-ctx.Done():
			t.Fatal("context canceled before observing change")
		}
	}
}

// TestDarwinWatcherPollsUntilDirExists covers the fallback path: the watch
// target doesn't exist yet, so Run must keep polling until it does, then
// pick up filesystem events once the watch is established.
func TestDarwinWatcherPollsUntilDirExists(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sessions")
	w := &DirWatcher{Dir: dir, Interval: 50 * time.Millisecond}
	ch := make(chan Snapshot, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go w.Run(ctx, ch)

	time.Sleep(150 * time.Millisecond)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "1.json"), map[string]any{
		"cwd": "/tmp/wt-b", "status": "busy",
	})

	for {
		select {
		case snap := <-ch:
			if snap.States["/tmp/wt-b"] == Working {
				return
			}
		case <-ctx.Done():
			t.Fatal("never observed session created after dir appeared")
		}
	}
}
