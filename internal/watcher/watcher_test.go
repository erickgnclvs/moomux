package watcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyBusy(t *testing.T) {
	b := true
	if classify(rawSession{Busy: &b}) != Working {
		t.Fatal("busy=true should be Working")
	}
	bf := false
	if classify(rawSession{Busy: &bf}) != Waiting {
		t.Fatal("busy=false should be Waiting")
	}
}

func TestClassifyStatusFields(t *testing.T) {
	if classify(rawSession{Status: "idle"}) != Waiting {
		t.Fatal("status idle")
	}
	if classify(rawSession{Status: "busy"}) != Working {
		t.Fatal("status busy")
	}
	if classify(rawSession{State: "busy"}) != Working {
		t.Fatal("state busy")
	}
}

func TestWatcherTickEmitsSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "1.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "busy",
	})
	writeJSON(t, filepath.Join(dir, "2.json"), map[string]any{
		"cwd":    "/tmp/wt-b",
		"status": "idle",
	})

	w := &Watcher{Dir: dir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-a"] != Working {
			t.Fatalf("wt-a = %v", snap.States["/tmp/wt-a"])
		}
		if snap.States["/tmp/wt-b"] != Waiting {
			t.Fatalf("wt-b = %v", snap.States["/tmp/wt-b"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestWatcherMissingDir(t *testing.T) {
	w := &Watcher{Dir: "/nonexistent/curral/test", Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)
	select {
	case snap := <-ch:
		if len(snap.States) != 0 {
			t.Fatalf("expected empty snapshot, got %v", snap.States)
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestStateString(t *testing.T) {
	if Working.String() != "working" {
		t.Fatal()
	}
	if Waiting.String() != "waiting" {
		t.Fatal()
	}
	if Parked.String() != "parked" {
		t.Fatal()
	}
}
