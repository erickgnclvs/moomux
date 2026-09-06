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
	if classify(rawSession{Busy: &bf}) != Done {
		t.Fatal("busy=false should be Done")
	}
}

func TestClassifyStatusFields(t *testing.T) {
	if classify(rawSession{Status: "idle"}) != Done {
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

	w := &DirWatcher{Dir: dir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.States["/tmp/wt-a"] != Working {
			t.Fatalf("wt-a = %v", snap.States["/tmp/wt-a"])
		}
		if snap.States["/tmp/wt-b"] != Done {
			t.Fatalf("wt-b = %v", snap.States["/tmp/wt-b"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestWatcherRunDoesNotMutateReceiverInterval(t *testing.T) {
	// Run defaulted a zero Interval by writing back to w.Interval, which is
	// racy if the same *DirWatcher were ever shared across goroutines (and
	// is inconsistent with SQLiteWatcher.Run, which already uses a local).
	w := &DirWatcher{Dir: t.TempDir()}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go w.Run(ctx, ch)

	// Waiting for the first snapshot guarantees Run has already run past
	// its interval-defaulting step (any receiver write already happened
	// before this channel send) without racing the check against it.
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timed out waiting for snapshot")
	}
	if w.Interval != 0 {
		t.Fatalf("Interval = %v, want unchanged zero value", w.Interval)
	}
}

func TestWatcherMissingDir(t *testing.T) {
	w := &DirWatcher{Dir: "/nonexistent/moomux/test", Interval: 10 * time.Millisecond}
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
	if Done.String() != "done" {
		t.Fatal()
	}
	if Parked.String() != "parked" {
		t.Fatal()
	}
}

func TestClassifyNeedsInput(t *testing.T) {
	if got := classify(rawSession{Status: "needs-input"}); got != NeedsInput {
		t.Fatalf("status needs-input: got %v", got)
	}
}

func TestWatcherTickNeedsInputWinsOverStaleBusy(t *testing.T) {
	// The claudehook marker file and Claude's own <pid>.json can disagree
	// within the same tick (e.g. a Notification hook fired but the native
	// status file hasn't caught up yet) — NeedsInput must win so a stale
	// "busy" write never hides it.
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "pid.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "busy",
	})
	writeJSON(t, filepath.Join(dir, "sess.notify.json"), map[string]any{
		"cwd":    "/tmp/wt-a",
		"status": "needs-input",
	})

	w := &DirWatcher{Dir: dir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if got := snap.States["/tmp/wt-a"]; got != NeedsInput {
			t.Fatalf("got %v, want NeedsInput", got)
		}
	case <-ctx.Done():
		t.Fatal("no snapshot received")
	}
}

func TestClassifyUnrecognizedIsUnknown(t *testing.T) {
	// A status we don't recognize is not evidence of idleness; Unknown loses
	// the max-merge against any real signal instead of masquerading as Done.
	if got := classify(rawSession{Status: "garbled"}); got != Unknown {
		t.Fatalf("got %v", got)
	}
	if got := classify(rawSession{}); got != Unknown {
		t.Fatalf("empty session: got %v", got)
	}
}

func TestWatcherSurfacesParseErrors(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "ok.json"), map[string]any{
		"cwd": "/tmp/wt-a", "status": "busy",
	})
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{half-writ"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &DirWatcher{Dir: dir, Interval: 10 * time.Millisecond}
	ch := make(chan Snapshot, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go w.Run(ctx, ch)

	select {
	case snap := <-ch:
		if snap.Err == nil {
			t.Fatal("unparsable file did not surface in Snapshot.Err")
		}
		if snap.States["/tmp/wt-a"] != Working {
			t.Fatalf("valid file not classified: %v", snap.States)
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

// TestStateJSONRoundTrip guards the wire contract: State is serialized by
// name, so re-ranking the iota (which the enum invites — see NeedsInput's
// comment) can't silently reassign what a second front end renders.
func TestStateJSONRoundTrip(t *testing.T) {
	for _, st := range []State{Unknown, Parked, Done, Working, NeedsInput} {
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatalf("marshal %v: %v", st, err)
		}
		if want := `"` + st.String() + `"`; string(b) != want {
			t.Errorf("marshal %v = %s, want %s", st, b, want)
		}
		var got State
		if err := json.Unmarshal(b, &got); err != nil || got != st {
			t.Errorf("round trip %v -> %s -> %v (err %v)", st, b, got, err)
		}
	}
	var got State
	if err := json.Unmarshal([]byte(`"bogus"`), &got); err == nil {
		t.Error("unmarshaling an unknown state name succeeded; want an error")
	}
}
