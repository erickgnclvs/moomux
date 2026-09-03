//go:build darwin

package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestDarwinSQLiteWatcherMarkerIsEventDriven proves a needs-input marker
// appearing after a session has already been watching gets picked up well
// inside the debounce window rather than waiting out a long Interval. With
// the old poll-only Run, this would only see it after the 10s Interval and
// would time out.
func TestDarwinSQLiteWatcherMarkerIsEventDriven(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "sqlite3")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "state.sqlite")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	markerDir := t.TempDir()

	w := &SQLiteWatcher{DB: dbPath, Query: "irrelevant", MarkerDir: markerDir, Interval: 10 * time.Second}
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

	writeJSON(t, filepath.Join(markerDir, "sess.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "needs-input",
	})

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case snap := <-ch:
			if snap.States["/tmp/wt-a"] == NeedsInput {
				return
			}
		case <-deadline:
			t.Fatal("did not observe new marker within 500ms; watcher is not event-driven")
		case <-ctx.Done():
			t.Fatal("context canceled before observing marker")
		}
	}
}
